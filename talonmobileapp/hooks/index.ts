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
  type ConversationBlock,
  type MetroEvent,
  type PlanData,
  type PlanTask,
  type Session,
  type StreamEvent,
  type TurnEvidence,
} from '@/lib/api';
import { getToken } from '@/lib/auth';
import { SHOW_TRIAGE } from '@/lib/flags';
import { OUTPUT_TAIL, seedFromBlocks, type TimelineItem } from '@/lib/timeline';

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
  /** L51 / FM-THINKa — is thinking what arrived LAST? Set by thinking.delta,
   *  cleared by every content frame other than another thinking delta (the
   *  seam around reduceEvent's switch). The ThinkingRow's label reads this:
   *  "Thinking… <t>" while true, "Thought for <t>" once false — the only
   *  honest signal, since any guess from unrelated state is wrong in both
   *  directions (frozen "Thinking…" over a 60 s tool call, or a finished
   *  stamp mid-burst). */
  thinkingActive?: boolean;
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
   *  the frame itself claim nothing (the marker's absence rule, mirrored).
   *  L54 / FM-VRFYa: the done mapping additionally requires something worth
   *  claiming — a verdict or work reported — before evidence exists at all,
   *  so a no-checks frame cannot mount the false "checks did not pass" row. */
  evidence?: TurnEvidence;
  /** L52 / FM-TLKa — the chronological timeline. Every visible row the turn
   *  produces (thinking segments, narration, tools, decisions, skills, output)
   *  in ARRIVAL order; the render source for the live tail. The legacy
   *  thinking/toolCalls/text fields keep updating beside it unchanged (the
   *  L51 signal and the P-series contracts), but nothing renders from them.
   *  Undefined only for test fixtures that predate it — every reducer path
   *  materializes it. */
  timeline?: TimelineItem[];
  /** Monotonic key mint for timeline rows: appended exactly once per new
   *  item, never reused, so a drop (the plan case clears thinking/narration)
   *  can never mint a duplicate React key. */
  timelineSeq?: number;
}

const initialTurn: TurnState = {
  phase: 'idle',
  text: '',
  thinking: '',
  thinkingActive: false,
  output: '',
  tasks: [],
  toolCalls: [],
  timeline: [],
  timelineSeq: 0,
};

// ── Timeline helpers (L52 / FM-TLKa) ────────────────────────────────────────
// The timeline is the ordered render source. Two invariants live here:
//   · a NEW row mints its key from timelineSeq (monotonic, never reused);
//   · a trailing segment of the same kind folds (thinking + narration text
//     accumulate in place; command output extends in place) — except a
//     thinking segment the settle pass already froze, which reopens as a
//     fresh arrival (burst → act → burst reads as two moments).
type DistributiveOmit<T, K extends PropertyKey> = T extends unknown ? Omit<T, K> : never;
type NewItem = DistributiveOmit<TimelineItem, 'key'>;
type TimelineAppend = { timeline: TimelineItem[]; timelineSeq: number };

function tl(state: TurnState): TimelineItem[] {
  return state.timeline ?? [];
}

function mint(state: TurnState, item: NewItem): TimelineAppend {
  const seq = state.timelineSeq ?? 0;
  return {
    timeline: [...tl(state), { ...item, key: `tl-${seq}` } as TimelineItem],
    timelineSeq: seq + 1,
  };
}

function replaceTail(state: TurnState, item: TimelineItem): TimelineAppend {
  const cur = tl(state);
  return { timeline: [...cur.slice(0, -1), item], timelineSeq: state.timelineSeq ?? cur.length };
}

// Exported as a test seam (mobile row 46): the done-frame mapping is pure
// state math and must be pin-able without a live SSE round trip.
export function reduceEvent(state: TurnState, ev: StreamEvent): TurnState {
  let next = reduceFrame(state, ev);
  // L51 / FM-THINKa — thinking is the active activity only while thinking is
  // what arrived last: any content frame except another thinking delta ends
  // the burst. `phase` frames are exempt (they name the round, not output);
  // a frame that returns state unchanged (the suppressed write_adr row)
  // still ends it — the model acted.
  if (ev.kind !== 'thinking.delta' && ev.kind !== 'phase' && next.thinkingActive) {
    next = { ...next, thinkingActive: false };
  }
  // L52 / FM-TLKa — the same settle rule the label reads: a live thinking
  // segment freezes to "Thought for <t>" the moment the model acts on it.
  // Run with the seam above so one content frame does both.
  if (ev.kind !== 'thinking.delta' && ev.kind !== 'phase') {
    const cur = tl(next);
    if (cur.some((i) => i.kind === 'thinking' && i.streaming)) {
      next = {
        ...next,
        timeline: cur.map((i) => (i.kind === 'thinking' && i.streaming ? { ...i, streaming: false } : i)),
      };
    }
  }
  return next;
}

function reduceFrame(state: TurnState, ev: StreamEvent): TurnState {
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
    case 'text.delta': {
      // Narration folds into its trailing segment (or opens one): the words
      // the user asked to see while the model works, at the position they
      // arrived — L52's timeline, not a fixed slot above the tools.
      const chunk = String(data.text ?? '');
      const cur = tl(state);
      const last = cur[cur.length - 1];
      if (last?.kind === 'narration') {
        return {
          ...state,
          text: state.text + chunk,
          ...replaceTail(state, { ...last, text: last.text + chunk }),
        };
      }
      return { ...state, text: state.text + chunk, ...mint(state, { kind: 'narration', text: chunk }) };
    }
    case 'thinking.delta': {
      // Reasoning is most of what a turn produces while the user watches. It
      // used to be dropped here ("kept server-side"), which meant the entire
      // thinking phase rendered as nothing — the blank screen during a build.
      // Accumulate it and render it as its own live row, as the web does.
      //
      // L52 / FM-TLKa: a burst appends to its TRAILING live segment — or
      // opens a new one when the arrival position says so (a tool ran in
      // between, or the previous segment settled). The wall of text stays
      // one tap away either way (L51); this only decides WHERE the segment
      // sits in the chronology.
      const chunk = String(data.text ?? '');
      const cur = tl(state);
      const last = cur[cur.length - 1];
      if (last?.kind === 'thinking' && last.streaming) {
        return {
          ...state,
          thinking: state.thinking + chunk,
          thinkingActive: true,
          ...replaceTail(state, { ...last, text: last.text + chunk, streaming: true }),
        };
      }
      return {
        ...state,
        thinking: state.thinking + chunk,
        thinkingActive: true,
        ...mint(state, { kind: 'thinking', text: chunk, streaming: true }),
      };
    }
    case 'tool_call': {
      const name = String(data.name ?? '');
      // write_adr / write_memory render as the decision card the `decision`
      // frame carries, not as a bare tool row — the same suppression the web
      // applies, so one recorded decision never shows twice.
      if (name === 'write_adr' || name === 'write_memory') return state;
      const status = String(data.status ?? '');
      const id = typeof data.id === 'string' && data.id ? data.id : undefined;
      const cur = tl(state);
      // L52b: a paired running→completed frame settles ONE row in place —
      // keyed by the call id — instead of appending a second (the phantom
      // spinner the fixed-slot tail showed). An id-less frame never
      // collides with a keyed one: it appends. The legacy toolCalls field
      // keeps appending every frame exactly as its contracts pin it.
      if (id) {
        const idx = cur.findIndex((i) => i.kind === 'tool' && i.id === id);
        const prev = idx >= 0 ? cur[idx] : undefined;
        if (prev?.kind === 'tool') {
          return {
            ...state,
            toolCalls: [...state.toolCalls, { name, status }],
            timeline: [...cur.slice(0, idx), { ...prev, status, name: name || prev.name }, ...cur.slice(idx + 1)],
          };
        }
      }
      const next = mint(state, { kind: 'tool', id, name, status });
      return {
        ...state,
        toolCalls: [...state.toolCalls, { name, status }],
        timeline: next.timeline,
        timelineSeq: next.timelineSeq,
      };
    }
    case 'verify': {
      // A verification result (typecheck, lint, boot). The web renders this as
      // a trace row; without it the mobile shows a tool running and then
      // nothing about whether it passed — the outcome of the work is exactly
      // what "everything that is happening" means.
      const name = `Checked ${String(data.stage ?? '')}`.trim();
      const status = String(data.status ?? '');
      const cur = tl(state);
      // Same upsert as a paired tool frame, keyed by stage: the sweep's
      // running→completed frames settle one row.
      const idx = cur.findIndex((i) => i.kind === 'tool' && i.name === name);
      const prev = idx >= 0 ? cur[idx] : undefined;
      if (prev?.kind === 'tool') {
        return {
          ...state,
          toolCalls: [
            ...state.toolCalls,
            { name, status },
          ],
          timeline: [...cur.slice(0, idx), { ...prev, status }, ...cur.slice(idx + 1)],
        };
      }
      const next = mint(state, { kind: 'tool', name, status });
      return {
        ...state,
        toolCalls: [...state.toolCalls, { name, status }],
        timeline: next.timeline,
        timelineSeq: next.timelineSeq,
      };
    }
    case 'command_output': {
      // Live stdout/stderr from run_command, typecheck, lint and build_apk.
      // Bounded like the web's trace tail: unbounded accumulation of a full
      // build log in device memory is a stutter, not a feature.
      const chunk = String(data.text ?? '');
      const cur = tl(state);
      const last = cur[cur.length - 1];
      if (last?.kind === 'output') {
        return {
          ...state,
          output: (state.output + chunk).slice(-OUTPUT_TAIL),
          ...replaceTail(state, { ...last, text: (last.text + chunk).slice(-OUTPUT_TAIL) }),
        };
      }
      // L52d: output opens its own row right behind the running tool that
      // produced it — interleaved, never hoisted to the screen's bottom.
      return {
        ...state,
        output: (state.output + chunk).slice(-OUTPUT_TAIL),
        ...mint(state, { kind: 'output', text: chunk.slice(-OUTPUT_TAIL) }),
      };
    }
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
      // L52a: the plan card is the turn — it moves to the TOP, so the
      // timeline drops the envelope's thinking/narration and keeps the
      // message as one closing narration row beneath it.
      // FM-ANSQa(ii) / L56: a plan-less `plan` frame is a QUESTION reply
      // (planAsksQuestion's waiting shape — message present, plan absent).
      // Normalizing the absence produced an empty-but-truthy PlanData, so an
      // empty PlanCard with "Approve & build" rendered over the question and
      // approve would ship an empty PLAN.md into a build. The web guards
      // `d.plan ? … : null`; no plan key, no card — the message still lands
      // as the canonical narration.
      const plan = data.plan ? normalizePlan(data.plan) : undefined;
      const msg = typeof data.message === 'string' && data.message ? data.message : 'Plan ready.';
      const kept = tl(state).filter((i) => i.kind !== 'thinking' && i.kind !== 'narration');
      const seq = state.timelineSeq ?? 0;
      return {
        ...state,
        plan,
        planMessage: msg,
        text: msg,
        thinking: '',
        timeline: [...kept, { key: `tl-${seq}`, kind: 'narration', text: msg }],
        timelineSeq: seq + 1,
      };
    }
    case 'guardrail_reject': {
      // A guardrail refused an action: a visible failed row, the web's
      // "Blocked" trace line. Dropped (the old default), the model was
      // stopped and the screen said nothing at all.
      const firstLine = typeof data.error === 'string' && data.error ? data.error.split('\n')[0] : '';
      const blocked: NewItem = {
        kind: 'tool',
        name: 'Blocked',
        status: 'error',
        target: firstLine || (typeof data.tool === 'string' ? data.tool : ''),
      };
      const next = mint(state, blocked);
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
        timeline: next.timeline,
        timelineSeq: next.timelineSeq,
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
      // L52c: a decision lands at ITS position with the full text behind a
      // tap — the decision itself renders once, in the chronology.
      const decided: NewItem = {
        kind: 'decision',
        name: `Recorded ${variant}`,
        target: head.split('\n')[0] || undefined,
        summary: head || undefined,
      };
      const next = mint(state, decided);
      return {
        ...state,
        toolCalls: [
          ...state.toolCalls,
          { name: `Recorded ${variant}`, status: 'completed', target: head.split('\n')[0] || undefined },
        ],
        timeline: next.timeline,
        timelineSeq: next.timelineSeq,
      };
    }
    case 'skills_group': {
      // The skill bodies the orchestrator injected this round — a chip row,
      // so "the model is using your skill library" is visible, not
      // implied. The wire carries the group as a bare JSON array.
      const names = (Array.isArray(data) ? data : []).filter((s): s is string => typeof s === 'string');
      if (names.length === 0) return state;
      const next = mint(state, { kind: 'skill', title: names.join(', ') });
      return { ...state, timeline: next.timeline, timelineSeq: next.timelineSeq };
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
      const filesTouched = Array.isArray(data.files_touched) ? (data.files_touched as string[]) : [];
      const skippedGates = Array.isArray(data.skipped_gates) ? (data.skipped_gates as string[]) : [];
      const verifyPolicy = typeof data.verify_policy === 'string' ? data.verify_policy : undefined;
      const remainingTasks = Array.isArray(data.remaining_tasks) ? (data.remaining_tasks as string[]) : undefined;
      const costUsd = typeof data.cost_usd === 'number' ? data.cost_usd : undefined;
      const rounds = typeof data.rounds === 'number' ? data.rounds : undefined;
      const rawVerified = typeof data.verified === 'boolean' ? data.verified : undefined;
      // L54 / FM-VRFYa — a verdict only exists when checks could have run:
      // files touched, or a policy that classified the sweep. `verified:false`
      // with nothing behind it is not a failed check, it is a claim the
      // backend never made — the "checks did not pass · 1 round" that came
      // out on every no-changes turn. Rounds alone are a count, never a
      // verdict: with no verdict and nothing else worth reporting there is
      // no evidence row at all.
      const hasWork = filesTouched.length > 0 || skippedGates.length > 0 || !!verifyPolicy;
      const verdict = hasWork || rawVerified === true ? rawVerified : undefined;
      const claimable =
        verdict !== undefined ||
        hasWork ||
        (remainingTasks?.length ?? 0) > 0 ||
        costUsd !== undefined;
      const evidence: TurnEvidence | undefined = claimable
        ? {
            filesTouched,
            verified: verdict,
            verifiedBestEffort:
              verdict !== undefined && typeof data.verified_best_effort === 'boolean'
                ? data.verified_best_effort
                : undefined,
            verifyPolicy,
            skippedGates,
            rounds,
            remainingTasks,
            costUsd,
          }
        : undefined;
      // Adopt the frame's message when the turn streamed no words: a turn
      // that ends in tools alone still said something (web's narration
      // adoption, AIChat done handler). The timeline carries the adopted
      // words as its closing row — nothing renders from `text` anymore.
      const adopted = state.text === '' && typeof data.message === 'string' && data.message ? data.message : undefined;
      const adoption = adopted
        ? (() => {
            const n = mint(state, { kind: 'narration', text: adopted });
            return { timeline: n.timeline, timelineSeq: n.timelineSeq };
          })()
        : {};
      const status = String(data.status ?? 'success');
      // A pause is not the end of the turn: on 'waiting' the ask_user card
      // owns the conversation and must survive the frame that closes the
      // stream — the answer continues THIS turn (web keeps its identity the
      // same way). On 'failed' the turn died: phase error, never the header's
      // "turn finished" over a failure — and the banner's resume detection
      // reads the failed status from history right after.
      if (status === 'waiting' && state.phase === 'waiting') {
        return { ...state, evidence, ...(adopted ? { text: adopted, ...adoption } : {}) };
      }
      if (status === 'failed') {
        const message =
          state.error ?? (typeof data.message === 'string' && data.message ? data.message : 'the turn failed');
        return { ...state, phase: 'error', error: message, evidence };
      }
      return { ...state, phase: 'done', evidence, ...(adopted ? { text: adopted, ...adoption } : {}) };
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

  async function answerPrompt(promptId: number, text: string, seedBlocks?: ConversationBlock[]) {
    // L53c / FM-RSUMa: an answer from the RESTORED card carries the prompt's
    // stored blocks — the continuation appends onto them instead of a blank
    // tail. No blocks (the live card's short path) keeps the turn in place:
    // there the tail already carries everything the history holds.
    const seeded = seedBlocks && seedBlocks.length > 0 ? seedFromBlocks(seedBlocks) : undefined;
    setTurn((prev) => ({
      ...(seeded ? { ...initialTurn, timeline: seeded } : prev),
      phase: 'streaming',
      question: undefined,
      questionPromptId: undefined,
      ...(seeded ? { promptId, queue: prev.queue ?? [] } : {}),
    }));
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
  // L53c: seedBlocks rebuild the interrupted rounds into the tail — without
  // them the resume stream continued into an empty screen (the 69 KB of
  // invisible streaming: the SEQUENCE was there, the CONTENT was not).
  async function resume(promptId: number, seedBlocks?: ConversationBlock[]) {
    closeRef.current?.();
    const seeded = seedBlocks && seedBlocks.length > 0 ? seedFromBlocks(seedBlocks) : undefined;
    setTurn((prev) => ({
      ...initialTurn,
      phase: 'streaming',
      promptId,
      queue: prev.queue ?? [],
      ...(seeded ? { timeline: seeded } : {}),
    }));
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

  return { turn, send, answer, answerPrompt, resume, discard, stop, steer, parkQueue };
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
      qc.setQueryData(['session', s.id], s);
      // FM-DEVb: the Device tab's verdict is derived from session state, so
      // the cache must follow the server the moment it changes — test-info
      // flips from "no session" to the deep link/QR/APK, and live-session
      // must not keep reporting idle for another 5s poll. (The old
      // ['session', project_id, 'id'] write is gone: nothing ever read it —
      // a cache entry nothing reads is a second, phantom source of truth.)
      qc.invalidateQueries({ queryKey: ['test-info', s.project_id] });
      qc.invalidateQueries({ queryKey: ['live-session', s.project_id] });
    },
  });
}

export function useDestroySession() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: number) => sessionsApi.destroy(id),
    onSuccess: () => {
      // The vars are a session id, not a project id — prefix scope is the
      // only honest one here: every project's test-info/live-session
      // describe sessions, and a destroyed one must not keep serving the
      // dead deep link + QR the backend itself refuses to emit (FM-DEVb).
      qc.invalidateQueries({ queryKey: ['session'] });
      qc.invalidateQueries({ queryKey: ['test-info'] });
      qc.invalidateQueries({ queryKey: ['live-session'] });
    },
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
    // FM-DEVb: the same 5s rhythm as the session queries — the verdict IS
    // session state, so a session started or destroyed from another client
    // (the web IDE) converges here too, never serving a dead deep link.
    refetchInterval: 5000,
    queryFn: () => testInfoApi.get(projectId!),
  });
}

// ── Triage / usage / notifications / profile ───────────

export function useTriage(projectId: number | null) {
  return useQuery({
    queryKey: ['triage', projectId],
    // Same gate as the card: a hidden form should not poll the endpoint.
    enabled: projectId != null && SHOW_TRIAGE,
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
