// TanStack Query hooks: the mobile app's data layer. Each hook names its
// query key honestly and mirrors the web client's surface exactly, same
// routes, same shapes. Mutations invalidate exactly the queries they touch.
//
// Laws that hold across every hook here:
//   - No mock data. A hook that cannot reach the backend errors; a screen
//     that cannot render honest data shows the empty/loading state.
//   - Streams (prompt turns, metro) are subscriptions, not queries: they
//     are managed by useSyncExternalStore or local state + close handles,
//     never polled.

import { useEffect, useRef, useState } from 'react';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { useAppStore } from '@/stores/app';
import {
  exportApi,
  meApi,
  notificationsApi,
  planApi,
  projectsApi,
  promptsApi,
  sessionsApi,
  testInfoApi,
  triageApi,
  usageApi,
  openMetroStream,
  type MetroEvent,
  type PlanTask,
  type Session,
  type StreamEvent,
} from '@/lib/api';
import { getToken } from '@/lib/auth';

// ── Auth bootstrap ─────────────────────────────────────

/** True once SecureStore answered, the router gates on this, never on a
 *  guess, so a cold start never flashes the signed-out screens at a
 *  signed-in user. */
export function useAuthBootstrap() {
  const [ready, setReady] = useState(false);
  const [signedIn, setSignedIn] = useState(false);
  useEffect(() => {
    let alive = true;
    getToken().then((t) => {
      if (!alive) return;
      setSignedIn(t !== null);
      setReady(true);
    });
    return () => {
      alive = false;
    };
  }, []);
  return { ready, signedIn };
}

// ── Projects ───────────────────────────────────────────

export function useProjects() {
  return useQuery({ queryKey: ['projects'], queryFn: () => projectsApi.list() });
}

/** The project the build/diff/device tabs operate on: the pinned choice, or
 *  the most recent when nothing is pinned. The pin lives in the app store,
 *  one source of truth, persisted, so a tap on the dashboard loads every
 *  tab at once. */
export function useActiveProject() {
  const { data: projects } = useProjects();
  const pinnedId = useAppStore((s) => s.activeProjectId);
  if (!projects || projects.length === 0) return null;
  return projects.find((p) => p.id === pinnedId) ?? projects[0];
}

/** Pin the active project. The projects screen calls this on tap. */
export function useSetActiveProject() {
  return useAppStore((s) => s.setActiveProjectId);
}

export function useCreateProject() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (name: string) => projectsApi.create(name),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['projects'] }),
  });
}

// ── Plan tasks ────────────────────────────────────────

/** Tasks from the persisted plan (plan_json on the project row). The stream
 *  appends fresher states as they arrive; this is the reload source. */
export function usePlanTasks(projectId: number | null) {
  return useQuery({
    queryKey: ['plan', projectId],
    enabled: projectId != null,
    queryFn: () => planApi.get(projectId!),
  });
}

// ── Prompt streaming ──────────────────────────────────

export interface TurnState {
  phase: 'idle' | 'streaming' | 'waiting' | 'done' | 'error';
  text: string;
  tasks: PlanTask[];
  toolCalls: { name: string; status: string }[];
  error?: string;
  /** The prompt id a pending question belongs to (ask_user pause). */
  questionPromptId?: number;
  question?: string;
}

const initialTurn: TurnState = { phase: 'idle', text: '', tasks: [], toolCalls: [] };

function reduceEvent(state: TurnState, ev: StreamEvent): TurnState {
  let data: Record<string, unknown> = {};
  try {
    data = JSON.parse(ev.data) as Record<string, unknown>;
  } catch {
    return state;
  }
  switch (ev.kind) {
    case 'text.delta':
      return { ...state, text: state.text + String(data.text ?? '') };
    case 'thinking.delta':
      return state; // thinking is kept server-side; the UI shows text
    case 'tool_call':
      return {
        ...state,
        toolCalls: [
          ...state.toolCalls,
          { name: String(data.name ?? ''), status: String(data.status ?? '') },
        ],
      };
    case 'task_update': {
      const task: PlanTask = {
        id: String(data.id ?? ''),
        description: String(data.description ?? ''),
        status: String(data.status ?? 'pending'),
        result: data.result ? String(data.result) : undefined,
      };
      const without = state.tasks.filter((t) => t.id !== task.id);
      return { ...state, tasks: [...without, task] };
    }
    case 'question':
      return {
        ...state,
        phase: 'waiting',
        question: String(data.question ?? ''),
        questionPromptId: Number(data.prompt_id ?? 0),
      };
    case 'error':
      return { ...state, phase: 'error', error: String(data.message ?? 'the turn failed') };
    case 'done':
      return { ...state, phase: 'done' };
    default:
      return state;
  }
}

/**
 * Drives a plan or build turn. Returns the live turn state and actions:
 * send() starts a turn, answer() feeds an ask_user reply. The stream is a
 * subscription, never polled, closed on unmount.
 */
export function usePromptStream(projectId: number | null) {
  const [turn, setTurn] = useState<TurnState>(initialTurn);
  const closeRef = useRef<(() => void) | null>(null);
  const qc = useQueryClient();

  useEffect(() => () => closeRef.current?.(), []);

  function applyEvents(onEvent: (ev: StreamEvent) => void) {
    return (ev: StreamEvent) => {
      onEvent(ev);
      setTurn((prev) => reduceEvent(prev, ev));
    };
  }

  async function send(mode: 'plan' | 'build', prompt: string, attachmentIds: number[] = [], explicitProjectId?: number) {
    const pid = explicitProjectId ?? projectId;
    if (pid == null) return;
    closeRef.current?.();
    setTurn({ ...initialTurn, phase: 'streaming' });
    closeRef.current = await promptsApi.stream(
      pid,
      mode,
      prompt,
      applyEvents(() => {}),
      (err) => setTurn((prev) => ({ ...prev, phase: 'error', error: err.message })),
      attachmentIds,
    );
  }

  async function answer(text: string) {
    const pid = turn.questionPromptId;
    if (!pid) return;
    return answerPrompt(pid, text);
  }

  async function answerPrompt(promptId: number, text: string) {
    setTurn((prev) => ({ ...prev, phase: 'streaming', question: undefined, questionPromptId: undefined }));
    closeRef.current?.();
    closeRef.current = await promptsApi.answer(
      promptId,
      text,
      applyEvents(() => {}),
      (err) => setTurn((prev) => ({ ...prev, phase: 'error', error: err.message })),
    );
  }

  function stop() {
    closeRef.current?.();
    closeRef.current = null;
    setTurn((prev) => ({ ...prev, phase: 'done' }));
    qc.invalidateQueries({ queryKey: ['projects'] });
  }

  return { turn, send, answer, answerPrompt, stop };
}

// ── Device session ────────────────────────────────────

/** The device session for the active project. There is no list route by
 *  design: the client holds the id of the session it created (the web IDE
 *  does the same). The id persists in the query cache; the row is polled
 *  every 5s because state is the truth (live → paused → destroyed). */
export function useDeviceSession(sessionId: number | null) {
  return useQuery({
    queryKey: ['session', sessionId],
    enabled: sessionId != null,
    refetchInterval: 5000,
    queryFn: () => sessionsApi.get(sessionId!),
  });
}

export function useCreateSession() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (projectId: number) => sessionsApi.create(projectId),
    onSuccess: (s: Session) => {
      qc.setQueryData(['session', s.project_id, 'id'], s.id);
      qc.setQueryData(['session', s.id], s);
    },
  });
}

export function useDestroySession() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: number) => sessionsApi.destroy(id),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['session'] }),
  });
}

// ── Metro + test info ─────────────────────────────────

/** Live metro events for the preview badge; a subscription, not a poll. */
export function useMetro(projectId: number | null) {
  const [events, setEvents] = useState<MetroEvent[]>([]);
  const closeRef = useRef<(() => void) | null>(null);

  useEffect(() => {
    if (projectId == null) return;
    let alive = true;
    openMetroStream(projectId, (ev) => {
      if (alive) setEvents((prev) => [...prev.slice(-49), ev]);
    }).then((close) => {
      if (alive) closeRef.current = close;
      else close();
    });
    return () => {
      alive = false;
      closeRef.current?.();
      closeRef.current = null;
    };
  }, [projectId]);

  return events;
}

export function useTestInfo(projectId: number | null) {
  return useQuery({
    queryKey: ['test-info', projectId],
    enabled: projectId != null,
    queryFn: () => testInfoApi.get(projectId!),
  });
}

// ── Triage / usage / notifications / profile ───────────

export function useTriage(projectId: number | null) {
  return useQuery({
    queryKey: ['triage', projectId],
    enabled: projectId != null,
    queryFn: () => triageApi.get(projectId!),
  });
}

export function useSaveTriage(projectId: number) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (input: { decision: string; cutover_date: string; exposure_cents: number; notes: string }) =>
      triageApi.save(projectId, input),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['triage', projectId] }),
  });
}

export function useUsage() {
  return useQuery({ queryKey: ['usage'], queryFn: () => usageApi.get() });
}

export function useNotifications() {
  return useQuery({ queryKey: ['notifications'], queryFn: () => notificationsApi.list() });
}

export function useMe() {
  return useQuery({ queryKey: ['me'], queryFn: () => meApi.get() });
}

export function useSetPushToken() {
  return useMutation({ mutationFn: (onesignalId: string) => meApi.setPushToken(onesignalId) });
}

// ── Export ────────────────────────────────────────────

export function useStartExport(projectId: number) {
  return useMutation({
    mutationFn: (whatsNew: string) => exportApi.start(projectId, whatsNew),
  });
}

export function useExportJob(jobId: number | null) {
  return useQuery({
    queryKey: ['export', jobId],
    enabled: jobId != null,
    refetchInterval: (query) =>
      query.state.data && ['queued', 'running'].includes(query.state.data.status) ? 3000 : false,
    queryFn: () => exportApi.get(jobId!),
  });
}
