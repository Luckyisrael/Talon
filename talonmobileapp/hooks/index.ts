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
import React from 'react';
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

// The backend's phase names, in words a person reads. Mirrors the web's
// PHASE_LABELS (frontend/components/ide/AIChat.tsx) so both clients name the
// same stage the same way — a phase that reads as VERIFY_STATIC on the wire
// should not say different things on different screens.
const PHASE_LABELS: Record<string, string> = {
  INTAKE: 'Reading your request',
  PLAN: 'Planning',
  CONTEXT_BUILD: 'Scanning the project',
  RESEARCH: 'Looking things up',
  GENERATE: 'Writing code',
  VERIFY_STATIC: 'Checking types',
  VERIFY_RUNTIME: 'Running it on the device',
  DONE: 'Done',
  FAILED_NEEDS_USER: 'Needs you',
};

function phaseLabel(phase: string): string {
  if (!phase) return '';
  return PHASE_LABELS[phase] ?? phase.toLowerCase().replace(/_/g, ' ');
}

export interface TurnState {
  phase: 'idle' | 'streaming' | 'waiting' | 'done' | 'error';
  text: string;
  /** The model's reasoning, streamed live. This is the bulk of a turn's visible
   *  work — a build call emits hundreds of thinking deltas against tens of text
   *  deltas — and discarding them (as this reducer used to) is exactly the
   *  blank screen during a build: the provider is producing, the wire is
   *  delivering, and the UI shows nothing but a spinner. The web renders these
   *  as their own row; the mobile must too. */
  thinking: string;
  /** Live stdout/stderr from the agent's commands (typecheck, lint, build).
   *  Bounded to a tail — the full log would grow without limit in device
   *  memory; the tail is what tells the user what is happening right now. */
  output: string;
  tasks: PlanTask[];
  toolCalls: { name: string; status: string }[];
  error?: string;
  /** The prompt id a pending question belongs to (ask_user pause). */
  questionPromptId?: number;
  question?: string;
  /** What the agent is doing right now, from the phase event — "Planning",
   *  "Writing code". Without it a long model call shows a blind spinner while
   *  the web client names the phase; the backend sends this every round. */
  activity?: string;
  /** The id of the prompt in flight. The phase event carries it; it is how the
   *  live tail knows to retire — once loadHistory returns turns whose
   *  prompt_id matches, the durable log has absorbed this turn and the
   *  optimistic rows hand off to it. DSH does the same handoff by rpcId. */
  promptId?: number;
  /** The prompt the user just sent, echoed as a bubble at the tail. It is the
   *  optimistic part of the turn: it shows immediately and retires with the
   *  rest of the tail when history absorbs the prompt. */
  userEcho?: string;
}

const initialTurn: TurnState = { phase: 'idle', text: '', thinking: '', output: '', tasks: [], toolCalls: [] };

function reduceEvent(state: TurnState, ev: StreamEvent): TurnState {
  let data: Record<string, unknown> = {};
  try {
    data = JSON.parse(ev.data) as Record<string, unknown>;
  } catch {
    return state;
  }
  switch (ev.kind) {
    case 'phase':
      // The backend names the stage every round (GENERATE, VERIFY_STATIC...).
      // The web shows it as the loading label; without this case the mobile
      // app dropped the event entirely and showed a blind "Working" spinner
      // for the whole duration of a model call. The prompt id arrives on the
      // first phase event of the turn and is the live tail's retirement signal.
      return {
        ...state,
        activity: phaseLabel(String(data.phase ?? '')),
        promptId: state.promptId ?? (Number(data.prompt_id ?? 0) || undefined),
      };
    case 'text.delta':
      return { ...state, text: state.text + String(data.text ?? '') };
    case 'thinking.delta':
      // Reasoning is most of what a turn produces while the user watches. It
      // used to be dropped here ("kept server-side"), which meant the entire
      // thinking phase rendered as nothing — the blank screen during a build.
      // Accumulate it and render it as its own live row, as the web does.
      return { ...state, thinking: state.thinking + String(data.text ?? '') };
    case 'tool_call':
      return {
        ...state,
        toolCalls: [
          ...state.toolCalls,
          { name: String(data.name ?? ''), status: String(data.status ?? '') },
        ],
      };
    case 'verify':
      // A verification result (typecheck, lint, boot). The web renders this as
      // a trace row; without it the mobile shows a tool running and then
      // nothing about whether it passed — the outcome of the work is exactly
      // what "everything that is happening" means.
      return {
        ...state,
        toolCalls: [
          ...state.toolCalls,
          {
            name: `Checked ${String(data.stage ?? '')}`.trim(),
            status: String(data.status ?? ''),
          },
        ],
      };
    case 'diag':
      // TEMP DIAGNOSTIC — transport stage markers from openStream. Surfaced on
      // the build screen so we can see where the SSE connection stalls.
      return { ...state, output: (state.output + '\n' + String(data ?? '')).slice(-2000) };
    case 'command_output':
      // Live stdout/stderr from run_command, typecheck, lint and build_apk.
      // Bounded like the web's trace tail: unbounded accumulation of a full
      // build log in device memory is a stutter, not a feature.
      return {
        ...state,
        output: (state.output + String(data.text ?? '')).slice(-2000),
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

  // TEMP DIAGNOSTIC — counting what actually reaches the app. The symptom is a
  // static screen with the fallback "Working" label, which means not even phase
  // events arrive mid-turn. This counts raw SSE events and remembers the last
  // few, so the screen can show us instead of us guessing. Remove once live
  // streaming works.
  const [diag, setDiag] = React.useState<{ count: number; kinds: string[]; lastData: string }>({
    count: 0,
    kinds: [],
    lastData: '',
  });

  function applyEvents(onEvent: (ev: StreamEvent) => void) {
    return (ev: StreamEvent) => {
      onEvent(ev);
      setDiag((prev) => ({
        count: prev.count + 1,
        kinds: [...prev.kinds, ev.kind].slice(-8),
        lastData: ev.data.slice(-120),
      }));
      setTurn((prev) => reduceEvent(prev, ev));
    };
  }

  async function send(mode: 'plan' | 'build', prompt: string, attachmentIds: number[] = [], explicitProjectId?: number) {
    const pid = explicitProjectId ?? projectId;
    if (pid == null) return;
    closeRef.current?.();
    // The prompt becomes the optimistic tail: it renders at the bottom of the
    // transcript immediately and retires with the rest of the turn once the
    // durable log absorbs it. Nothing is cleared — the transcript above is
    // untouched, which is why sending never makes the conversation vanish.
    setTurn({ ...initialTurn, phase: 'streaming', userEcho: prompt });
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

  return { turn, send, answer, answerPrompt, stop, diag };
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

/** useLiveSession asks the server which device session is running for the
 *  project. This is the cross-client sync path: a session started from the web
 *  IDE has an id this phone never stored, so local state alone would report
 *  "no session" while a device is live. Polled, not subscribed — the same
 *  rhythm as useDeviceSession, so both tabs converge on the same row.
 *  onLive receives null when the server reports idle (204). */
export function useLiveSession(
  projectId: number | null,
  onLive: (session: Session | null) => void,
) {
  const cb = React.useRef(onLive);
  cb.current = onLive;
  const { data } = useQuery({
    queryKey: ['live-session', projectId],
    enabled: projectId != null,
    refetchInterval: 5000,
    queryFn: () => (projectId == null ? null : sessionsApi.live(projectId)),
  });
  React.useEffect(() => {
    cb.current(data ?? null);
  }, [data]);
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
