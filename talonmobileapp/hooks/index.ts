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
  normalizePlan,
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
  type PlanData,
  type PlanTask,
  type Session,
  type StreamEvent,
  type TurnEvidence,
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
  /** target rides along when the event itself says what was acted on — a
   *  guardrail rejection's error line, a decision's title. The label map can
   *  only know the tool name. */
  toolCalls: { name: string; status: string; target?: string }[];
  error?: string;
  /** The prompt id a pending question belongs to (ask_user pause). */
  questionPromptId?: number;
  question?: string;
  /** The choices the ask_user frame carried. The question card renders these
   *  instead of a guessed pair — the model asked for MWA or an email link,
   *  not Yes/No. Empty when the backend sent none. */
  questionChoices?: string[];
  /** The parsed plan from the `plan` frame (web's PlanData). Canonical reply
   *  of a plan turn: when it lands, text/thinking are replaced by the plan
   *  message so raw narration and JSON envelopes never render over it. */
  plan?: PlanData;
  planMessage?: string;
  /** Messages steered into THIS running turn (H2). Rendered as marked rows
   *  in the live tail — a delivery failure lands on its own row, never
   *  silently dropped. Cleared with the turn. */
  steers?: { id: number; text: string; error?: string }[];
  /** Messages typed while a plan call ran, parked until the call settles —
   *  the web's plan queue. Visible rows, delivered one per settled turn. */
  queue?: { text: string }[];
  /** Whether the in-flight turn was SENT as a plan call: decides steer vs
   *  park for what the composer submits while it streams. */
  planStream?: boolean;
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
  /** Evidence the done frame carried (files/verified/policy/skips/rounds/
   *  remaining/cost), mapped by reduceEvent on 'done'. The live twin of the
   *  restored marker's verify row — the web footer's fields, the same claims
   *  in the same words. Undefined until a done frame lands; absent keys on
   *  the frame itself claim nothing (the marker's absence rule, mirrored). */
  evidence?: TurnEvidence;
}

const initialTurn: TurnState = { phase: 'idle', text: '', thinking: '', output: '', tasks: [], toolCalls: [] };

// Exported as a test seam (mobile row 46): the done-frame mapping is pure
// state math and must be pin-able without a live SSE round trip.
export function reduceEvent(state: TurnState, ev: StreamEvent): TurnState {
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
    case 'tool_call': {
      const name = String(data.name ?? '');
      // write_adr / write_memory render as the decision card the `decision`
      // frame carries, not as a bare tool row — the same suppression the web
      // applies, so one recorded decision never shows twice.
      if (name === 'write_adr' || name === 'write_memory') return state;
      return {
        ...state,
        toolCalls: [...state.toolCalls, { name, status: String(data.status ?? '') }],
      };
    }
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
        questionChoices: Array.isArray(data.choices)
          ? (data.choices as unknown[]).filter((c): c is string => typeof c === 'string')
          : [],
      };
    case 'plan': {
      // The parsed plan is the canonical reply (the web's plan case): it
      // replaces the streamed narration, so the model's private reasoning or
      // raw JSON envelope never renders over the plan the user approves.
      const plan = normalizePlan(data.plan);
      const msg = typeof data.message === 'string' && data.message ? data.message : 'Plan ready.';
      return { ...state, plan, planMessage: msg, text: msg, thinking: '' };
    }
    case 'guardrail_reject': {
      // A guardrail refused an action: a visible failed row, the web's
      // "Blocked" trace line. Dropped (the old default), the model was
      // stopped and the screen said nothing at all.
      const firstLine = typeof data.error === 'string' && data.error ? data.error.split('\n')[0] : '';
      return {
        ...state,
        toolCalls: [
          ...state.toolCalls,
          {
            name: 'Blocked',
            status: 'error',
            target: firstLine || (typeof data.tool === 'string' ? data.tool : ''),
          },
        ],
      };
    }
    case 'decision': {
      // The agent recorded an architecture decision or a durable memory —
      // the web's decision card, as one trace row carrying its title.
      const variant = data.variant === 'adr' ? 'ADR' : 'memory';
      const head =
        typeof data.decision === 'string' && data.decision
          ? data.decision
          : typeof data.content === 'string'
            ? data.content
            : '';
      return {
        ...state,
        toolCalls: [
          ...state.toolCalls,
          { name: `Recorded ${variant}`, status: 'completed', target: head.split('\n')[0] || undefined },
        ],
      };
    }
    case 'error':
      return { ...state, phase: 'error', error: String(data.message ?? 'the turn failed') };
    case 'done': {
      // The done frame is the live twin of the turn_end marker (rows 44/45):
      // the same outcome fields at the same instant. Map them so the footer
      // claims files/rounds/policy now instead of waiting for the history
      // refetch — and ABSENT keys stay undefined, claiming nothing (the
      // marker's absence rule, mirrored). Failure frames without an outcome
      // carry only {status} → evidence renders as nothing.
      const evidence: TurnEvidence = {
        filesTouched: Array.isArray(data.files_touched) ? (data.files_touched as string[]) : [],
        verified: typeof data.verified === 'boolean' ? data.verified : undefined,
        verifiedBestEffort: typeof data.verified_best_effort === 'boolean' ? data.verified_best_effort : undefined,
        verifyPolicy: typeof data.verify_policy === 'string' ? data.verify_policy : undefined,
        skippedGates: Array.isArray(data.skipped_gates) ? (data.skipped_gates as string[]) : [],
        rounds: typeof data.rounds === 'number' ? data.rounds : undefined,
        remainingTasks: Array.isArray(data.remaining_tasks) ? (data.remaining_tasks as string[]) : undefined,
        costUsd: typeof data.cost_usd === 'number' ? data.cost_usd : undefined,
      };
      // Adopt the frame's message when the turn streamed no words: a turn
      // that ends in tools alone still said something (web's narration
      // adoption, AIChat done handler).
      const adopted = state.text === '' && typeof data.message === 'string' && data.message ? data.message : undefined;
      const status = String(data.status ?? 'success');
      // A pause is not the end of the turn: on 'waiting' the ask_user card
      // owns the conversation and must survive the frame that closes the
      // stream — the answer continues THIS turn (web keeps its identity the
      // same way). On 'failed' the turn died: phase error, never the header's
      // "turn finished" over a failure — and the banner's resume detection
      // reads the failed status from history right after.
      if (status === 'waiting' && state.phase === 'waiting') {
        return { ...state, evidence, ...(adopted ? { text: adopted } : {}) };
      }
      if (status === 'failed') {
        const message =
          state.error ?? (typeof data.message === 'string' && data.message ? data.message : 'the turn failed');
        return { ...state, phase: 'error', error: message, evidence };
      }
      return { ...state, phase: 'done', evidence, ...(adopted ? { text: adopted } : {}) };
    }
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
  // Steer rows key on a local id so two in-flight deliveries never mark each
  // other's failure.
  const steerSeqRef = useRef(0);
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
    // A parked plan-queue entry rides through the reset (the delivery effect
    // shifts it first): the next parked message waits for THIS turn to end,
    // one per turn, exactly like the web's planQueueRef.
    setTurn((prev) => ({
      ...initialTurn,
      phase: 'streaming',
      userEcho: prompt,
      queue: prev.queue ?? [],
      planStream: mode === 'plan',
    }));
    closeRef.current = await promptsApi.stream(
      pid,
      mode,
      prompt,
      applyEvents(() => {}),
      (err) => setTurn((prev) => ({ ...prev, phase: 'error', error: err.message })),
      attachmentIds,
    );
  }

  // Deliver a message to the running build (H2 steer, the web's steerRunning):
  // the text lands in the loop's next model request and the row below marks it
  // as a redirect, not a new prompt. Returns false when the phase event has not
  // named this turn yet — the draft stays in the composer rather than being
  // swallowed by a POST that cannot address its target.
  function steer(raw: string): boolean {
    const message = raw.trim();
    const pid = turn.promptId;
    if (!message || pid == null || pid <= 0) return false;
    const id = (steerSeqRef.current += 1);
    setTurn((prev) => ({ ...prev, steers: [...(prev.steers ?? []), { id, text: message }] }));
    void promptsApi.steer(pid, message).catch((err: unknown) => {
      const text = err instanceof Error ? err.message : 'steer failed';
      setTurn((prev) => ({
        ...prev,
        steers: (prev.steers ?? []).map((s) => (s.id === id ? { ...s, error: text } : s)),
      }));
    });
    return true;
  }

  // Park what was typed while a plan call ran: a visible row in the live tail
  // (honest that it has not gone out yet) that the delivery effect fires as
  // its own plan turn the moment the call settles. One in-flight model request
  // cannot be interrupted with new input — this replaces the silent drop.
  function parkQueue(raw: string) {
    const message = raw.trim();
    if (!message) return;
    setTurn((prev) => ({ ...prev, queue: [...(prev.queue ?? []), { text: message }] }));
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

  // Resume the interrupted turn — the web's resume(), same route: the server
  // replays the transcript and the agent picks up where it stopped. No echo,
  // this continues the existing turn rather than starting one. A parked queue
  // rides through (the web's planQueueRef is not cleared by resume either).
  async function resume(promptId: number) {
    closeRef.current?.();
    setTurn((prev) => ({ ...initialTurn, phase: 'streaming', queue: prev.queue ?? [] }));
    closeRef.current = await promptsApi.resume(
      promptId,
      applyEvents(() => {}),
      (err) => setTurn((prev) => ({ ...prev, phase: 'error', error: err.message })),
    );
  }

  // Discard the interrupted turn so a reload stops offering it — the banner's
  // other button, same route as the web's discard.
  async function discard(promptId: number) {
    await promptsApi.discard(promptId);
    qc.invalidateQueries({ queryKey: ['projects'] });
  }

  function stop() {
    closeRef.current?.();
    closeRef.current = null;
    setTurn((prev) => ({ ...prev, phase: 'done' }));
    qc.invalidateQueries({ queryKey: ['projects'] });
  }

  // Deliver what was typed while a plan call was running — the web's
  // delivery effect, same trigger: the call has settled (done, error, or
  // stop), so the composer is free again. One message per turn: the shift
  // and the send land together, so the next parked message waits for THIS
  // delivery to end. 'waiting' never delivers — the question owns the
  // conversation until it is answered.
  useEffect(() => {
    if (turn.phase !== 'done' && turn.phase !== 'error') return;
    const next = (turn.queue ?? [])[0];
    if (!next || projectId == null) return;
    setTurn((prev) => ({ ...prev, queue: (prev.queue ?? []).slice(1) }));
    void send('plan', next.text, [], projectId);
  }, [turn.phase, turn.queue, projectId, send]);

  return { turn, send, answer, answerPrompt, resume, discard, stop, steer, parkQueue, diag };
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
