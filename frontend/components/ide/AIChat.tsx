"use client";

import {
  forwardRef,
  Fragment,
  useCallback,
  useEffect,
  useImperativeHandle,
  useMemo,
  useRef,
  useState,
} from "react";
import { createPortal } from "react-dom";
import {
  attachments,
  conversation,
  isResumable,
  prompts,
  rowStatus,
  streamPrompt,
  usage,
  type ConversationTurn,
  type LatestPrompt,
  type StreamEvent,
} from "@/lib/api";
import {
  classifyFile,
  describeSize,
  downscaleImage,
  MAX_ATTACHMENTS,
  previewFor,
  stripInlineDocs,
  type LocalAttachment,
} from "@/lib/attachments";
import { ComposerAttachments, MessageAttachments } from "./Attachments";
import { useIDEStore } from "@/lib/store";
import { uuid } from "@/lib/uuid";
import type { ChatMessage, PlanData, PlanTask, StreamMessage, TraceRow } from "@/lib/types";
import { featureText } from "@/lib/types";
import AgentTrace, { getToolLabel, PlanProgress, progressHint } from "./AgentTrace";
import LoadingState from "@/components/ui/LoadingState";

interface AIChatProps {
  projectId: number;
  /** Which chat thread within the project this is. Omitted (or 0) means the
      project's default thread — every call site that predates multi-chat
      keeps working unchanged. */
  conversationId?: number;
  /** Called with the paths a finished turn changed, so the tree can refetch. */
  onFilesTouched?: (paths: string[]) => void;
  /** A plan arrived — the layout puts it in the centre panel to be read. */
  onPlan?: (plan: PlanData, message: string) => void;
  /** Approving a plan is one behaviour, owned by the layout, reachable from
      both the card here and the full plan view. */
  onApprovePlan?: (plan: PlanData) => void;
  /** §7: the live turn's trace rows + touched files, lifted so the centre
      AgentWorkspace renders the full-fidelity transcript alongside this
      compact rail. Fires on every change while a turn streams. */
  onLiveTrace?: (
    rows: TraceRow[],
    filesTouched: string[],
    planTasks: PlanTask[],
  ) => void;
  /** Start a new chat thread — owned by the layout, which selects it.
      The history block's new-chat button calls this; threads never delete. */
  onNewThread?: () => void;
}

export interface AIChatHandle {
  switchToBuild: () => void;
  sendMessage: (msg: string, mode?: "plan" | "build") => void;
}

type Entry = ChatMessage | StreamMessage;

const isAssistant = (m: Entry): m is StreamMessage => "isStreaming" in m;

/**
 * Settles a turn's trace rows the moment its stream ends — done frame or
 * error path alike. Running rows become done; streaming flags drop. A
 * narration/thinking row that never received its end frame (aborted stream,
 * gateway error) must not shimmer or blink forever after the turn is over.
 */
/**
 * FeedbackRow is the human-facing half of the message-feedback sidecar: two
 * thumbs under each finished assistant message.
 *
 * The rating is fire-and-forget optimistic — the thumb snaps to its state so
 * the UI never lags a person's click — but the write is what makes it durable.
 * A failure rolls the thumb back and says why rather than silently keeping a
 * state the server never accepted.
 *
 * Pressing the same thumb twice un-rates: the only thing worse than no feedback
 * is being unable to take it back.
 */
function FeedbackRow({
  msg,
  projectId,
  onRate,
}: {
  msg: { id: string; rating?: number };
  projectId: number;
  onRate: (rating: number) => void;
}) {
  const [saving, setSaving] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  // A click is a toggle: pressing the thumb already pressed clears the rating
  // (written as 0 — "no opinion" — so the row stays a single source of truth).
  const rate = async (value: number) => {
    const next = msg.rating === value ? 0 : value;
    onRate(next);
    setErr(null);
    if (projectId == null) return;
    try {
      setSaving(true);
      await conversation.rate(projectId, msg.id, next);
    } catch {
      // Roll back to whatever the server actually has: a failed write must not
      // leave a thumb the person thinks landed.
      setErr("could not save the rating");
      onRate(msg.rating ?? 0);
    } finally {
      setSaving(false);
    }
  };

  const active = msg.rating ?? 0;

  return (
    <div className="mt-2 flex items-center gap-1.5">
      <button
        type="button"
        disabled={saving}
        onClick={() => rate(1)}
        title="Good response"
        aria-pressed={active === 1}
        className="rounded-md p-1 text-[var(--muted)] transition-colors hover:text-[var(--accent-1)] disabled:opacity-40 aria-pressed:text-[var(--accent-1)]"
      >
        <svg viewBox="0 0 24 24" fill={active === 1 ? "currentColor" : "none"} stroke="currentColor" strokeWidth={1.75} strokeLinecap="round" strokeLinejoin="round" className="h-4 w-4">
          <path d="M7 10v11H4a1 1 0 0 1-1-1v-9a1 1 0 0 1 1-1h3z" />
          <path d="M7 10l4.5-7a2 2 0 0 1 3.5 1.9V9h4.5a2 2 0 0 1 2 2.4l-1.2 6A2 2 0 0 1 18.3 19H7z" />
        </svg>
      </button>
      <button
        type="button"
        disabled={saving}
        onClick={() => rate(-1)}
        title="Bad response"
        aria-pressed={active === -1}
        className="rounded-md p-1 text-[var(--muted)] transition-colors hover:text-[var(--warn)] disabled:opacity-40 aria-pressed:text-[var(--warn)]"
      >
        <svg viewBox="0 0 24 24" fill={active === -1 ? "currentColor" : "none"} stroke="currentColor" strokeWidth={1.75} strokeLinecap="round" strokeLinejoin="round" className="h-4 w-4">
          <path d="M17 14V3h3a1 1 0 0 1 1 1v9a1 1 0 0 1-1 1h-3z" />
          <path d="M17 14l-4.5 7a2 2 0 0 1-3.5-1.9V15H4.5a2 2 0 0 1-2-2.4l1.2-6A2 2 0 0 1 5.7 5H17z" />
        </svg>
      </button>
      {err ? (
        <span className="font-mono text-[10px] text-[var(--warn)]/80">{err}</span>
      ) : null}
    </div>
  );
}

function settleRows(rows: StreamMessage["traceRows"]): StreamMessage["traceRows"] {
  let changed = false;
  const settled = rows.map((r): TraceRow => {
    if (r.status === "running") {
      changed = true;
      return { ...r, status: "done", streaming: false };
    }
    if (r.streaming) {
      changed = true;
      return { ...r, streaming: false };
    }
    return r;
  });
  return changed ? settled : rows;
}

// What the agent is doing, in words that describe the work rather than the
// internal state machine. STAGE_WRITE and PROMOTE used to be listed here; both
// phases were removed when writes started landing in the workspace directly.
const PHASE_LABELS: Record<string, string> = {
  INTAKE: "Reading your request",
  PLAN: "Planning",
  CONTEXT_BUILD: "Scanning the project",
  RESEARCH: "Looking things up",
  GENERATE: "Writing code",
  VERIFY_STATIC: "Checking types",
  VERIFY_RUNTIME: "Running it on the device",
  DONE: "Done",
  FAILED_NEEDS_USER: "Needs you",
};

function phaseLabel(phase?: string): string {
  if (!phase) return "";
  return PHASE_LABELS[phase] ?? phase.toLowerCase().replace(/_/g, " ");
}

function compactTokens(n: number): string {
  if (n < 1000) return String(n);
  return `${(n / 1000).toFixed(n < 10_000 ? 1 : 0)}k`;
}

/**
 * Rebuild the chat from the turns the server stored.
 *
 * Reloading the page used to empty the panel even though every turn was on
 * disk, which made the agent look like it had forgotten the project it was
 * still perfectly able to continue.
 *
 * `latest` is the most recent prompt's run state. It is what tells a restored
 * ask_user apart: a still-`waiting` latest on the final turn means that turn's
 * unanswered ask_user is live — the card must let the user answer it (keyed by
 * latest.id). Any other ask_user is history, shown as a locked card.
 */
function turnsToEntries(
  turns: ConversationTurn[],
  latest: LatestPrompt | null,
): Entry[] {
  const entries: Entry[] = [];

  // A tool_result arrives in the turn *after* the call, so collect them all up
  // front and look each call's outcome up by id.
  const results = new Map<string, { content: string; isError: boolean }>();
  for (const turn of turns) {
    for (const block of turn.blocks ?? []) {
      if (block.type === "tool_result" && block.tool_use_id) {
        results.set(block.tool_use_id, {
          content: block.content ?? "",
          isError: block.is_error ?? false,
        });
      }
    }
  }

  const filesRead = new Set<string>();

  turns.forEach((turn, i) => {
    const blocks = turn.blocks ?? [];
    if (blocks.length === 0) return;

    if (turn.role === "user") {
      // A user turn that only carries tool results is plumbing, not something
      // the person said.
      const text = blocks
        .filter((b) => b.type === "text")
        .map((b) => b.text ?? "")
        .join("")
        .trim();
      if (!text) return;
      // Image and doc blocks restore as chips; the bytes load on demand and
      // inlined doc prose stays out of the bubble (see stripInlineDocs).
      const atts = blocks
        .filter(
          (b) =>
            (b.type === "image" || b.type === "doc") && typeof b.attachment_id === "number",
        )
        .map((b) => ({
          id: b.attachment_id as number,
          name: (b.name as string) || "file",
          mime: (b.mime as string) || "application/octet-stream",
        }));
      entries.push({
        id: `restored-u${i}`,
        role: "user",
        content: text,
        timestamp: Date.parse(turn.at) || 0,
        ...(typeof turn.prompt_id === "number" ? { promptId: turn.prompt_id } : {}),
        ...(atts.length > 0 ? { attachments: atts } : {}),
      });
      return;
    }

    const rows: TraceRow[] = [];
    // A restored plan-mode reply carries its structured plan in a "plan" block
    // alongside the message text. Rebuild the plan card from it so planning —
    // often the first thing done on a project — survives a reload the same way
    // build turns already do.
    let restoredPlan: PlanData | undefined;
    // A restored ask_user is the pause card, not a tool row. Its stored
    // tool_result (the reply) locks it; an unanswered one on a still-waiting
    // final turn is the live question this reload must let the user answer.
    let restoredQuestion: StreamMessage["question"] | undefined;
    // The terminal marker's verify evidence (row 44 payload), applied to the
    // entry below so the footer restores exactly what streamed. Absent on
    // legacy/crash markers: those keep `verified` undefined and the footer
    // claims nothing about checks — rounds ride the marker either way.
    let restoredEvidence:
      | Pick<
          StreamMessage,
          | "filesTouched"
          | "verified"
          | "verifiedBestEffort"
          | "verifyPolicy"
          | "skippedGates"
          | "rounds"
        >
      | undefined;
    const isLastTurn = i === turns.length - 1;
    for (const [j, block] of blocks.entries()) {
      if (block.type === "text" && block.text?.trim()) {
        rows.push({
          id: `restored-a${i}-${j}`,
          kind: "narration",
          text: block.text,
        });
      }
      if (block.type === "plan") {
        const p = block.plan ?? {};
        restoredPlan = {
          screens: p.screens ?? [],
          features: (p.features ?? []).map(featureText),
          navigation: p.navigation ?? "",
          packages: p.packages ?? [],
        };
      }
      if (block.type === "turn_end") {
        // The terminal marker — DSH's turn-tail. Only a non-clean outcome adds
        // a row; a completed turn is already ended by its own narration.
        rows.push({
          id: `restored-end-${i}-${j}`,
          kind: "end",
          ending: (block.status as TraceRow["ending"]) ?? "failed",
          text: block.error,
          rounds: block.rounds,
        });
        if (block.verify) {
          // L54c2 / FM-VRFYa — a payload present but empty of work claims
          // NOTHING: "finished, nothing to verify" is the documented meaning
          // of an empty payload, so the footer stays down (an old marker
          // carrying verified:false with zero files and no policy claimed a
          // check that never ran). A false verdict WITH work keeps claimable
          // — that one is real (F3). Payload-less legacy markers are the
          // branch below (F2).
          const files = block.verify.files_touched ?? [];
          const skips = block.verify.skipped_gates ?? [];
          const policy = block.verify.verify_policy;
          const hasWork = files.length > 0 || skips.length > 0 || !!policy;
          const verdict =
            hasWork || block.verify.verified === true
              ? block.verify.verified
              : undefined;
          if (verdict !== undefined || hasWork) {
            restoredEvidence = {
              filesTouched: files,
              verified: verdict,
              verifiedBestEffort:
                verdict !== undefined ? block.verify.verified_best_effort : undefined,
              verifyPolicy: policy,
              skippedGates: skips,
              rounds: block.rounds,
            };
          }
        } else if (block.rounds !== undefined) {
          // Legacy marker (no verify payload): the round count is still
          // something the marker actually knows — report it, claim no checks.
          restoredEvidence = { rounds: block.rounds };
        }
      }
      if (block.type === "context") {
        // Machine guidance, not a user message: renders as a single line so a
        // skill body or lint report never again owns the scroll.
        rows.push({
          id: `restored-ctx-${i}-${j}`,
          kind: "context",
          title: block.title ?? "Applied guidance",
          body: block.body,
          ctxKind: block.kind,
        });
      }
      if (block.type === "tool_use") {
        const input = (block.input ?? {}) as {
          path?: string;
          name?: string;
          command?: string;
          pattern?: string;
          question?: string;
          choices?: string[];
          multi_select?: boolean;
          // write_adr / write_memory, for the decision-card reconstruction below
          decision?: string;
          outcome?: string;
          type?: string;
          content?: string;
          metadata?: string;
        };

        if (block.name === "ask_user") {
          const outcome = block.id ? results.get(block.id) : undefined;
          const answered = outcome?.content?.trim();
          // Live only when unanswered AND the server still reports this final
          // turn as waiting — otherwise a truncated old ask_user would render a
          // dead answer box keyed to a prompt that has moved on.
          const liveWaiting = !answered && isLastTurn && latest?.status === "waiting";
          if (answered || liveWaiting) {
            restoredQuestion = {
              promptId: liveWaiting && latest ? latest.id : 0,
              text: input.question ?? "The agent asked a question.",
              choices: Array.isArray(input.choices) ? input.choices : [],
              multiSelect: !!input.multi_select,
              ...(answered ? { answered } : {}),
            };
            continue;
          }
          // Neither answered nor live (a truncated tail): fall through so the
          // turn still shows the call happened rather than vanishing.
        }

        if (block.name === "write_adr" || block.name === "write_memory") {
          // A recorded decision restores as its card, exactly as it streamed
          // live — but only if the write succeeded. A cut-off or errored call
          // recorded nothing, and live suppressed its row too, so it leaves no
          // trace on reload either (parity with the live path).
          const outcome = block.id ? results.get(block.id) : undefined;
          if (outcome && !outcome.isError) {
            rows.push({
              id: `restored-a${i}-${j}`,
              kind: "decision",
              decision:
                block.name === "write_adr"
                  ? { variant: "adr", decision: input.decision, outcome: input.outcome }
                  : {
                      variant: "memory",
                      category: input.type || "fact",
                      content: input.content,
                      metadata: input.metadata,
                    },
            });
          }
          continue;
        }

        const { label, target } = getToolLabel(block.name ?? "", input, filesRead);
        if (block.name === "read_file" && input.path) filesRead.add(input.path);
        const outcome = block.id ? results.get(block.id) : undefined;
        rows.push({
          id: `restored-a${i}-${j}`,
          kind: "tool",
          tool: block.name,
          label,
          target,
          // No stored result means the turn was cut off mid-call.
          status: outcome ? (outcome.isError ? "fail" : "done") : undefined,
          summary: outcome?.isError ? firstLine(outcome.content) : undefined,
        });
      }
    }

    if (rows.length === 0 && !restoredPlan && !restoredQuestion) return;
    const entry: StreamMessage = {
      id: `restored-a${i}`,
      role: "assistant",
      traceRows: rows,
      isStreaming: false,
      restored: true,
      ...(typeof turn.prompt_id === "number" ? { promptId: turn.prompt_id } : {}),
    };
    if (restoredPlan) {
      entry.plan = restoredPlan;
      // planMessage need only be defined for the card to render — the message
      // itself is already the narration row above, matching the live plan path.
      entry.planMessage = rows.find((r) => r.kind === "narration")?.text ?? "";
    }
    if (restoredQuestion) entry.question = restoredQuestion;
    if (restoredEvidence) Object.assign(entry, restoredEvidence);
    entries.push(entry);
  });

  return entries;
}

function firstLine(s: string): string {
  const line = s.split("\n").find((l) => l.trim() !== "") ?? "";
  return line.length > 120 ? `${line.slice(0, 119)}…` : line;
}

const AIChat = forwardRef<AIChatHandle, AIChatProps>(
  ({ projectId, conversationId, onFilesTouched, onPlan, onApprovePlan, onLiveTrace, onNewThread }, ref) => {
    const [messages, setMessages] = useState<Entry[]>([]);
    const [input, setInput] = useState("");
    const [mode, setMode] = useState<"plan" | "build">("plan");
    // Staged attachments: validated local files waiting for the next send.
    // They upload at send time (no orphans on abandoned composers) and clear
    // once the stream starts — the server claims them to that turn.
    const [staged, setStaged] = useState<LocalAttachment[]>([]);
    const [attError, setAttError] = useState<string | null>(null);
    const [uploading, setUploading] = useState(false);
    const [dragging, setDragging] = useState(false);
    const fileInputRef = useRef<HTMLInputElement>(null);
    const keyRef = useRef(0);
    const previewUrlsRef = useRef<Set<string>>(new Set());
    // Which thread's history has finished loading. Derived into `restoring`
    // rather than flipped on in the effect, so switching projects — or
    // switching chats within the same project — reads as restoring
    // immediately instead of after an extra render pass.
    const restoreKey = `${projectId}:${conversationId ?? 0}`;
    const [restoredFor, setRestoredFor] = useState<string | null>(null);
    const restoring = restoredFor !== restoreKey;
    // Past user prompts for the history block: most recent first, deduped,
    // capped. Real transcript data — nothing invented.
    const pastPrompts = useMemo(() => {
      const out: string[] = [];
      const seen = new Set<string>();
      for (let i = messages.length - 1; i >= 0 && out.length < 10; i--) {
        const m = messages[i];
        if (!isAssistant(m) && m.content.trim() && !seen.has(m.content)) {
          seen.add(m.content);
          out.push(m.content);
        }
      }
      return out;
    }, [messages]);
    // The last turn, when it ended without finishing cleanly — the chat offers
    // to resume or discard it. Null once resumed, discarded, or superseded by a
    // new prompt. Derived from server truth on every load, so it never claims a
    // turn is resumable that isn't.
    const [resumable, setResumable] = useState<LatestPrompt | null>(null);
    // H2 steer: while a turn streams, typed messages go to the running build
    // (steer) by default. `queueInstead` flips that — the message waits for
    // the next turn like before. The running prompt id is captured from the
    // first phase event (every one carries prompt_id), because the steer
    // route is addressed by it.
    const [queueInstead, setQueueInstead] = useState(false);
    const runningPromptIdRef = useRef<number>(0);
    // Plan-mode input: a plan turn is ONE model request — there is no loop on
    // the server that can absorb a message mid-call. `planStreamRef` marks the
    // stream currently running as a plan call; `planQueueRef` parks what the
    // user typed meanwhile (as visible queued bubbles) to send as follow-up
    // plan turns the moment the call returns. Nothing typed is dropped or
    // silently rejected.
    const planStreamRef = useRef(false);
    const planQueueRef = useRef<{ id: string; text: string }[]>([]);
    // §7: the live turn's touched files, set as write tools fire. State, not
    // a ref: the lifted trace effect must re-run when the set changes, so the
    // workspace's Diff tab appears the moment a file changes.
    const [liveTouched, setLiveTouched] = useState<string[]>([]);
    // §7: the live turn's plan checklist — tasks whose id starts with
    // "plan:" — upserted from task_update events. Lifted so the centre
    // pane renders DSH-style pending/completed tasks.
    const [planTasks, setPlanTasks] = useState<PlanTask[]>([]);
    // H13: the session token meter — the rolled per-turn ledger from the
    // conversation endpoint, live-updated by the per-turn usage events.
    const [sessionUsage, setSessionUsage] = useState<{
      in: number;
      out: number;
      cache_read: number;
    } | null>(null);

    // The gateway's enforced pacing, fetched once per project so a long,
    // quiet stretch during a turn reads as what it is — the rate limit —
    // instead of a hang. 0/undefined means no pacing is configured.
    const [pacingSeconds, setPacingSeconds] = useState(0);
    useEffect(() => {
      let cancelled = false;
      usage
        .get()
        .then((u) => {
          if (!cancelled) setPacingSeconds(u.pacing_seconds ?? 0);
        })
        .catch(() => {
          // Unreachable usage is not worth a banner; the badge simply
          // stays hidden.
        });
      return () => {
        cancelled = true;
      };
    }, []);

    // Object URLs minted for image chips die with the project view — the
    // messages that reuse them unmount with it too.
    useEffect(() => {
      const minted = previewUrlsRef.current;
      return () => {
        minted.forEach((u) => URL.revokeObjectURL(u));
        minted.clear();
      };
    }, [projectId]);

    const removeStaged = useCallback((key: string) => {
      setStaged((prev) => prev.filter((a) => a.key !== key));
    }, []);

    const scrollRef = useRef<HTMLDivElement>(null);
    const endRef = useRef<HTMLDivElement>(null);
    const textareaRef = useRef<HTMLTextAreaElement>(null);
    const abortRef = useRef<AbortController | null>(null);
    const filesReadRef = useRef<Set<string>>(new Set());
    const pinnedRef = useRef(true);

    // ── History ──

    // A reload must not forget what the last turn changed: the changed-files
    // panel (and the git/badge refreshes keyed off it) is fed live by
    // onFilesTouched, so restore re-feeds it from the FINAL assistant entry
    // that knows — mirroring the live path, where each done frame overwrites
    // with its own set. Mid-flight turns (paused/waiting) deliberately have
    // no marker, so their leg files come from the prompt row instead.
    const seedRestoredFiles = (
      entries: Entry[],
      latestRow: LatestPrompt | null,
      isCancelled: () => boolean,
    ) => {
      const last = entries
        .slice()
        .reverse()
        .find((e): e is StreamMessage => isAssistant(e));
      if (last?.filesTouched?.length) {
        onFilesTouched?.(last.filesTouched);
        return;
      }
      // The newest turn never carried a marker: still mid-flight
      // (running/paused/waiting), aborted (cancelled/failed), or predating
      // row 44's verify payload. Its legs' union in the prompt row (row 42)
      // is the only thing that knows what it changed — restricting this to
      // paused/waiting made a mid-build or post-abort refresh fall back to
      // the whole diff (row-45 live E2E: the reload cut the SSE stream, the
      // row became `cancelled`, and the panel forgot the turn's file).
      if (latestRow) {
        prompts
          .get(latestRow.id)
          .then((row) => {
            if (!isCancelled() && row?.files_touched?.length) {
              onFilesTouched?.(row.files_touched);
            }
          })
          .catch(() => {
            // No row, no panel seed — the git badge still tells the truth.
          });
      }
    };

    useEffect(() => {
      let cancelled = false;
      conversation
        .get(projectId, { conversationId })
        .then(({ turns, latest, usage, planTasks }) => {
          if (cancelled) return;
          if (usage) setSessionUsage(usage);
          const entries = turnsToEntries(turns, latest);
          setMessages(entries);
          seedRestoredFiles(entries, latest, () => cancelled);
          // Offer resume only for a build turn whose last run did not finish.
          // A waiting turn is not resumable this way — its question card owns it.
          setResumable(isResumable(latest) ? latest : null);
          // The plan checklist is otherwise fed live by task_update frames; this
          // is the reload restore from the durable backlog, so a refresh does
          // not blank the checklist the person was watching fill in.
          if (planTasks && planTasks.length > 0) setPlanTasks(planTasks);
        })
        .catch(() => {
          // An unreachable history is not worth an error banner over an empty
          // panel the person can still type into.
        })
        .finally(() => {
          // Marks this thread restored either way: a failed fetch still ends
          // the restoring state, or the panel would spin forever.
          if (!cancelled) setRestoredFor(restoreKey);
        });
      return () => {
        cancelled = true;
      };
      // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [projectId, conversationId]);

    useEffect(() => () => abortRef.current?.abort(), []);

    // ── Scroll ──
    // Only follow the stream while the person is already at the bottom.
    // Yanking the view back mid-scroll is how a log becomes unreadable.
    const onScroll = useCallback(() => {
      const el = scrollRef.current;
      if (!el) return;
      pinnedRef.current = el.scrollHeight - el.scrollTop - el.clientHeight < 80;
    }, []);

    useEffect(() => {
      if (pinnedRef.current) {
        endRef.current?.scrollIntoView({ block: "end" });
      }
    }, [messages]);

    const isStreaming = useMemo(
      () => messages.some((m) => isAssistant(m) && m.isStreaming),
      [messages],
    );

    const liveMessage = useMemo(
      () => messages.find((m): m is StreamMessage => isAssistant(m) && m.isStreaming),
      [messages],
    );

    // A turn paused on ask_user and still unanswered — the actual question,
    // not just whether one exists, so it can render anchored above the
    // composer (see the canvas's own JSX). While one is open the main
    // composer also defers to it, so typing a fresh prompt cannot orphan the
    // paused turn on the server.
    const pendingQuestion = useMemo(
      () =>
        messages.find(
          (m): m is StreamMessage =>
            isAssistant(m) && m.question !== undefined && m.question.answered === undefined,
        )?.question,
      [messages],
    );

    const addFiles = useCallback(
      (files: FileList | File[]) => {
        const list = Array.from(files);
        if (list.length === 0 || pendingQuestion) return;
        // Steering and attachments do not mix: steers carry text to a running
        // turn, while files claim to the next fresh turn. Say so now rather
        // than silently dropping them later.
        if (isStreaming) {
          setAttError("wait for this turn to finish — files send with the next one");
          return;
        }
        const next = [...staged];
        let err: string | null = null;
        for (const f of list) {
          if (next.length >= MAX_ATTACHMENTS) {
            err = `at most ${MAX_ATTACHMENTS} files per turn`;
            break;
          }
          const verdict = classifyFile(f);
          if ("error" in verdict) {
            err = verdict.error;
            continue;
          }
          const previewUrl = previewFor(f, verdict.kind);
          if (previewUrl) previewUrlsRef.current.add(previewUrl);
          keyRef.current += 1;
          next.push({
            key: `${Date.now()}-${keyRef.current}`,
            file: f,
            kind: verdict.kind,
            name: f.name || "file",
            sizeLabel: describeSize(f.size),
            previewUrl,
          });
        }
        setAttError(err);
        setStaged(next);
      },
      [staged, isStreaming, pendingQuestion],
    );

    // §7: lift the live turn's trace to the layout's AgentWorkspace. The
    // rows and the last reported touched files cross; the effect fires only
    // when they actually change. Rows cap in the rail; the workspace gets
    // the full stream.
    useEffect(() => {
      onLiveTrace?.(
        liveMessage?.traceRows ?? [],
        liveTouched,
        planTasks,
      );
    }, [liveMessage, liveTouched, planTasks, onLiveTrace]);

    // The path of the tool the agent is running right now — shown in the live
    // status strip so you can see what it is touching, not just that it is busy.
    const liveTarget = useMemo(() => {
      const rows = liveMessage?.traceRows;
      if (!rows) return "";
      for (let i = rows.length - 1; i >= 0; i--) {
        const r = rows[i];
        if (r.kind === "tool" && r.status === "running" && r.target) return r.target;
      }
      return "";
    }, [liveMessage]);

    // ── Event handling ──

    const handleEvent = useCallback(
      (
        id: string,
        patch: (updater: (prev: StreamMessage) => StreamMessage) => void,
      ) =>
        (event: StreamEvent) => {
          const d = event.data ?? {};

          const appendRow = (row: TraceRow) =>
            patch((prev) => ({ ...prev, traceRows: [...prev.traceRows, row] }));

          // Like appendRow, but replaces an existing row with the same id.
          // Events whose id is deterministic within a turn (task updates key
          // on the task id) arrive more than once — running → completed is
          // two frames for one row — and pushing both would duplicate the row
          // and collide its React key.
          const upsertRow = (row: TraceRow) =>
            patch((prev) => {
              const i = prev.traceRows.findIndex((r) => r.id === row.id);
              if (i === -1)
                return { ...prev, traceRows: [...prev.traceRows, row] };
              const rows = [...prev.traceRows];
              rows[i] = row;
              return { ...prev, traceRows: rows };
            });

          switch (event.type) {
            // The agent's spoken prose streams as narration; its private
            // reasoning streams as a separate thinking row. Keeping them apart
            // is what lets the UI cap one to a couple of lines while the other
            // renders in full.
            case "text.start":
              patch((prev) => {
                const last = prev.traceRows[prev.traceRows.length - 1];
                if (last?.kind === "narration" && last.streaming) return prev;
                return {
                  ...prev,
                  traceRows: [
                    ...prev.traceRows,
                    { id: `${id}-n${prev.traceRows.length}`, kind: "narration", text: "", streaming: true },
                  ],
                };
              });
              break;

            case "text.delta":
              patch((prev) => {
                const rows = [...prev.traceRows];
                const last = rows[rows.length - 1];
                if (last?.kind === "narration" && last.streaming) {
                  rows[rows.length - 1] = { ...last, text: (last.text ?? "") + (d.text ?? "") };
                } else {
                  // The build stream sends deltas with no preceding start event.
                  rows.push({
                    id: `${id}-n${rows.length}`,
                    kind: "narration",
                    text: d.text ?? "",
                    streaming: true,
                  });
                }
                return { ...prev, traceRows: rows };
              });
              break;

            case "text.end":
              patch((prev) => {
                const rows = [...prev.traceRows];
                const last = rows[rows.length - 1];
                if (last?.kind === "narration" && last.streaming) {
                  rows[rows.length - 1] = { ...last, streaming: false };
                }
                return { ...prev, traceRows: rows };
              });
              break;

            case "thinking.start":
              patch((prev) => {
                const last = prev.traceRows[prev.traceRows.length - 1];
                if (last?.kind === "thinking" && last.streaming) return prev;
                return {
                  ...prev,
                  traceRows: [
                    ...prev.traceRows,
                    { id: `${id}-t${prev.traceRows.length}`, kind: "thinking", text: "", streaming: true },
                  ],
                };
              });
              break;

            case "thinking.delta":
              patch((prev) => {
                const rows = [...prev.traceRows];
                const last = rows[rows.length - 1];
                if (last?.kind === "thinking" && last.streaming) {
                  rows[rows.length - 1] = { ...last, text: (last.text ?? "") + (d.text ?? "") };
                } else {
                  rows.push({
                    id: `${id}-t${rows.length}`,
                    kind: "thinking",
                    text: d.text ?? "",
                    streaming: true,
                  });
                }
                return { ...prev, traceRows: rows };
              });
              break;

            case "thinking.end":
              patch((prev) => {
                const rows = [...prev.traceRows];
                const last = rows[rows.length - 1];
                if (last?.kind === "thinking" && last.streaming) {
                  rows[rows.length - 1] = { ...last, streaming: false };
                }
                return { ...prev, traceRows: rows };
              });
              break;

            case "phase": {
              // Capture the running turn's id from the first frame — the steer
              // route is addressed by it. Zero stays zero (never steers) until
              // the server names the turn.
              if (
                typeof d.prompt_id === "number" &&
                d.prompt_id > 0 &&
                d.prompt_id !== runningPromptIdRef.current
              ) {
                // A new turn: the live touched-files window and the plan
                // checklist start empty.
                runningPromptIdRef.current = d.prompt_id;
                setLiveTouched([]);
                setPlanTasks([]);
              }
              const roundMatch = typeof d.status === "string" && d.status.match(/round (\d+)/);
              patch((prev) => {
                // §7: a new round opens a collapsible group row — DSH-style
                // round blocks in the workspace transcript. Duplicate
                // "round N" frames collapse into the one marker.
                let rows = prev.traceRows;
                if (roundMatch) {
                  const n = parseInt(roundMatch[1], 10);
                  const lastGroup = [...rows]
                    .reverse()
                    .find((r) => r.kind === "group");
                  if (!lastGroup || lastGroup.label !== `Round ${n}`) {
                    rows = [
                      ...rows,
                      {
                        id: `${id}-round${n}`,
                        kind: "group",
                        label: `Round ${n}`,
                        collapsed: false,
                      },
                    ];
                  }
                }
                return {
                  ...prev,
                  phase: d.phase,
                  traceRows: rows,
                  ...(roundMatch ? { round: parseInt(roundMatch[1], 10) } : {}),
                };
              });
              break;
            }

            case "skills_group": {
              const skills = Array.isArray(d)
                ? (d as unknown as string[])
                : Array.isArray((d as unknown as { data?: unknown }).data)
                  ? ((d as unknown as { data: string[] }).data as string[])
                  : Array.isArray((d as unknown as { skills?: string[] }).skills)
                    ? (d as unknown as { skills: string[] }).skills
                    : [];
              if (skills.length === 0) break;
              patch((prev) => ({
                ...prev,
                traceRows: [
                  ...prev.traceRows,
                  {
                    id: `${id}-skills`,
                    kind: "group",
                    label: `Skills for this turn`,
                    summary: skills.join(" · "),
                    collapsed: false,
                    children: skills.map((name, i) => ({
                      id: `${id}-skill-${i}`,
                      kind: "tool" as const,
                      tool: "read_skill",
                      label: name,
                      target: "",
                      status: "done" as const,
                    })),
                  },
                ],
              }));
              break;
            }

            case "tool_call": {
              const tool = d.tool ?? "";
              // write_adr / write_memory surface as their own decision cards, fed
              // by the `decision` event that carries the recorded content. Skip
              // the generic row here so the same action never shows twice.
              if (tool === "write_adr" || tool === "write_memory") break;
              // The backend sends the raw target (file path, command, pattern)
              // in `path` and a human verb phrase in `name`. getToolLabel turns
              // tool + target into a label; the phrase is its fallback for the
              // tools it doesn't model (a context compaction, an applied skill).
              const { label } = getToolLabel(
                tool,
                { path: d.path, name: d.name },
                filesReadRef.current,
              );
              const target = d.path ?? "";
              if (tool === "read_file" && d.path) filesReadRef.current.add(d.path);

              const status = rowStatus(d.status);
              // §7: write tools feed the live touched-files window so the
              // Diff tab appears the moment a file changes, not only at done.
              if (
                status === "running" &&
                typeof d.path === "string" &&
                d.path &&
                (tool === "write_file" ||
                  tool === "edit_file" ||
                  tool === "multi_edit" ||
                  tool === "delete_file")
              ) {
                const p = d.path;
                setLiveTouched((prev) => (prev.includes(p) ? prev : [...prev, p]));
              }

              // One call streams as two events — running, then resolved —
              // sharing a stable id. Reconcile the row by that id so the resolve
              // updates in place (never a duplicate) and a second edit to the
              // same file opens its own row. One-shot events without an id (a
              // compaction, an applied skill) just append; a resolve that
              // somehow lacks an id falls back to the newest open row of its
              // tool.
              patch((prev) => {
                const rows = [...prev.traceRows];
                const rowID = d.id ? `tool-${d.id}` : "";
                let idx = -1;
                if (rowID) {
                  idx = rows.findIndex((r) => r.kind === "tool" && r.id === rowID);
                } else if (status !== "running") {
                  for (let i = rows.length - 1; i >= 0; i--) {
                    const r = rows[i];
                    if (r.kind === "tool" && r.tool === tool && r.status === "running") {
                      idx = i;
                      break;
                    }
                  }
                }
                if (idx >= 0) {
                  const r = rows[idx];
                  rows[idx] = {
                    ...r,
                    status,
                    label: label || r.label,
                    target: target || r.target,
                  };
                  return { ...prev, traceRows: rows };
                }
                rows.push({
                  id: rowID || `${id}-t${rows.length}`,
                  kind: "tool",
                  tool,
                  label,
                  target,
                  status,
                });
                return { ...prev, traceRows: rows };
              });
              break;
            }

            case "verify":
              // The trace shows one line; the whole compiler output goes to the
              // Output pane, which had nothing writing to it at all before this.
              if (d.error) {
                useIDEStore
                  .getState()
                  .appendBuildOutput(`$ verify ${d.stage ?? ""}\n${d.error}`);
              }
              appendRow({
                id: `${id}-v${Math.random().toString(36).slice(2, 8)}`,
                kind: "tool",
                tool: "verify",
                label: d.stage ? `Checked ${d.stage}` : "Checked",
                target: "",
                status: rowStatus(d.status),
                summary: d.error ? firstLine(d.error) : undefined,
              });
              break;

            case "command_output":
              // Live stdout/stderr from run_command, typecheck, lint, and
              // build_apk. Previously the Output pane stayed empty while the
              // agent's tools ran; now the developer watches them do it.
              if (typeof d.text === "string" && d.text.trim()) {
                useIDEStore.getState().appendBuildOutput(d.text);
                // §7: the transcript carries the command's live output too —
                // DSH renders bash inline, not only in a side terminal. The
                // open run_command/typecheck/lint/build row accumulates a
                // bounded tail; the full log keeps streaming to the terminal.
                const chunk = d.text;
                patch((prev) => {
                  const rows = [...prev.traceRows];
                  for (let i = rows.length - 1; i >= 0; i--) {
                    const r = rows[i];
                    if (
                      r.kind === "tool" &&
                      r.status === "running" &&
                      (r.tool === "run_command" ||
                        r.tool === "run_shell" ||
                        r.tool === "run_typecheck" ||
                        r.tool === "run_lint" ||
                        r.tool === "build_apk")
                    ) {
                      const MAX = 2000;
                      const merged = ((r.summary ?? "") + chunk).slice(-MAX);
                      rows[i] = { ...r, summary: merged, progress: progressHint(merged) };
                      break;
                    }
                  }
                  return { ...prev, traceRows: rows };
                });
              }
              break;

            case "task_update": {
              // §7: plan: prefixed updates upsert the checklist state; all
              // task updates keep their trace row.
              const taskKey =
                typeof d.id === "string" && d.id.length > 0 ? d.id : undefined;
              if (taskKey?.startsWith("plan:")) {
                const task: PlanTask = {
                  id: taskKey,
                  label:
                    typeof d.description === "string" ? d.description : taskKey,
                  status:
                    d.status === "completed"
                      ? "completed"
                      : d.status === "running" || d.status === "started"
                        ? "in_progress"
                        : d.status === "fail" || d.status === "failed"
                          ? "failed"
                          : "pending",
                  note: typeof d.result === "string" ? firstLine(d.result) : undefined,
                };
                setPlanTasks((prev) => {
                  const i = prev.findIndex((t) => t.id === task.id);
                  if (i === -1) return [...prev, task];
                  const next = [...prev];
                  next[i] = task;
                  return next;
                });
              }
              // A seeded-but-not-started plan item is checklist state, not
              // activity: the plan seed blasts every screen and feature at
              // once, and rendering each pending one as a running row put a
              // spinner on the left of the whole plan the moment a build was
              // approved. The row appears when the item actually starts or
              // lands; until then the checklist already shows it pending.
              const seededPending =
                taskKey?.startsWith("plan:") && d.status === "pending";
              if (!seededPending) {
                upsertRow({
                  id: `${id}-k${taskKey ?? Math.random().toString(36).slice(2, 8)}`,
                  kind: "tool",
                  tool: "task",
                  label: d.description ?? "Task",
                  target: "",
                  status: rowStatus(d.status),
                  summary: d.result ? firstLine(d.result) : undefined,
                });
              }
              break;
            }

            case "rebuild":
              if (d.detail) {
                useIDEStore.getState().appendBuildOutput(`$ rebuild dev client\n${d.detail}`);
              }
              appendRow({
                id: `${id}-r${Math.random().toString(36).slice(2, 8)}`,
                kind: "tool",
                tool: "rebuild",
                // A native dependency means the dev client itself has to be
                // rebuilt, which takes minutes. Saying so beats silence.
                label: "Rebuilding the dev client",
                target: d.detail ?? "",
                status: rowStatus(d.status),
              });
              break;

            case "guardrail_reject":
              appendRow({
                id: `${id}-g${Math.random().toString(36).slice(2, 8)}`,
                kind: "tool",
                tool: "guardrail",
                label: "Blocked",
                target: d.tool ?? "",
                status: "fail",
                summary: d.error ? firstLine(d.error) : undefined,
              });
              break;

            case "usage":
              // H13: the session meter rolls live as the turn spends.
              setSessionUsage((prev) => ({
                in: (prev?.in ?? 0) + (d.input_tokens ?? 0),
                out: (prev?.out ?? 0) + (d.output_tokens ?? 0),
                cache_read: (prev?.cache_read ?? 0) + (d.cache_read_tokens ?? 0),
              }));
              patch((prev) => ({
                ...prev,
                usage: {
                  input_tokens: (prev.usage?.input_tokens ?? 0) + (d.input_tokens ?? 0),
                  output_tokens: (prev.usage?.output_tokens ?? 0) + (d.output_tokens ?? 0),
                  cache_read_tokens:
                    (prev.usage?.cache_read_tokens ?? 0) + (d.cache_read_tokens ?? 0),
                },
              }));
              break;

            case "plan": {
              const raw = (d.plan ?? null) as PlanData | null;
              // L15 slice contract: wire features may be shaped objects —
              // reduce them to display text at the ingestion boundary so
              // the card and PlanViewer only ever see strings (FM-C1a).
              const plan = raw
                ? { ...raw, features: (raw.features ?? []).map(featureText) }
                : null;
              const msg = d.message || "Plan ready.";
              patch((prev) => ({
                ...prev,
                plan,
                planMessage: msg,
                // The parsed plan is the canonical reply. Any narration rows the
                // raw stream already emitted (thinking/prose/JSON) are stale the
                // moment this lands — replace them so the chat never shows the
                // model's private reasoning or the raw JSON envelope.
                traceRows: [{ id: `${id}-pm`, kind: "narration", text: msg }],
              }));
              if (plan) onPlan?.(plan, msg);
              break;
            }

            case "question":
              // The agent called ask_user: the turn pauses here. Attach the
              // question to this entry so it renders a card the user answers in
              // place. prompt_id addresses POST /answer; the `done` frame that
              // follows carries status "waiting", which ends the stream without
              // ending the turn. `...prev` keeps this through that done frame.
              patch((prev) => ({
                ...prev,
                question: {
                  promptId: d.prompt_id ?? 0,
                  text: d.question ?? "The agent has a question.",
                  choices: Array.isArray(d.choices) ? d.choices : [],
                  multiSelect: !!d.multi_select,
                },
              }));
              break;

            case "decision": {
              // The agent recorded an architecture decision or a durable memory.
              // Drop it into the trace as a card at the point it happened; the
              // generic tool row for the same call is suppressed above.
              const variant = d.variant === "adr" ? "adr" : "memory";
              appendRow({
                id: `${id}-d${Math.random().toString(36).slice(2, 8)}`,
                kind: "decision",
                decision: {
                  variant,
                  decision: d.decision,
                  outcome: d.outcome,
                  category: d.category,
                  content: d.content,
                  metadata: d.metadata,
                },
              });
              break;
            }

            case "error":
              patch((prev) => ({
                ...prev,
                error: d.message ?? "The turn failed without saying why.",
                isStreaming: false,
              }));
              break;

            case "done": {
              // The turn can accept no more steers — except a pause (waiting
              // or paused) is not the end of the turn: the answer or resume
              // continues THIS turn. Zeroing the id here made the
              // continuation's first phase frame look like a new turn, which
              // wiped the checklist so the replayed tasks re-seeded from
              // scratch — the double injection after answering a question.
              // A paused turn can't be steered anyway: streaming has ended.
              if (d.status !== "waiting" && d.status !== "paused") {
                runningPromptIdRef.current = 0;
              }
              const touched = d.files_touched ?? [];
              const skipped = Array.isArray(d.skipped_gates) ? d.skipped_gates : [];
              const stalled = d.status === "stalled";
              const failed = d.status === "failed";
              // L54c / FM-VRFYa — the verdict rule, the twin of the backend's
              // omission: verified keys only when checks could have run
              // (files, policy, skips) or the verdict itself is an explicit
              // true. A no-changes done ({rounds: 1, files_touched: []},
              // verified key absent since the L54 backend fix) claims
              // nothing at all — no footer — instead of a red "checks did
              // not pass" over "1 round" the backend never asserted.
              const policy =
                typeof d.verify_policy === "string" ? d.verify_policy : undefined;
              const remaining = Array.isArray(d.remaining_tasks)
                ? d.remaining_tasks
                : undefined;
              const hasWork =
                touched.length > 0 || skipped.length > 0 || !!policy;
              const verdict =
                hasWork || d.verified === true ? d.verified : undefined;
              const claimable =
                verdict !== undefined ||
                hasWork ||
                (remaining?.length ?? 0) > 0 ||
                d.cost_usd !== undefined;
              patch((prev) => {
                // Adopt the frame's message as narration only when the turn
                // streamed no words of its own (a restarted stream, a tools-
                // only round): the turn still ends with what it had to say,
                // and a turn that DID narrate never doubles.
                const hasWords = prev.traceRows.some(
                  (r) =>
                    r.kind === "narration" && (r.text ?? "").trim() !== "",
                );
                const narrated =
                  !failed && !hasWords && d.message?.trim()
                    ? [
                        ...prev.traceRows,
                        {
                          id: `${prev.id}-msg`,
                          kind: "narration" as const,
                          text: d.message.trim(),
                        },
                      ]
                    : prev.traceRows;
                // The live twin of the stored turn_end block: a stalled or
                // failed turn must SAY it stopped — restore renders exactly
                // this row — instead of collapsing into a generic DONE.
                const rows =
                  stalled || failed
                    ? [
                        ...narrated,
                        {
                          id: `${prev.id}-end`,
                          kind: "end" as const,
                          ending: (failed ? "failed" : "stalled") as TraceRow["ending"],
                          rounds: d.rounds,
                        },
                      ]
                    : narrated;
                return {
                  ...prev,
                  isStreaming: false,
                  phase: failed
                    ? "FAILED_NEEDS_USER"
                    : d.status === "waiting" || d.status === "paused"
                      ? "WAITING_FOR_USER"
                      : stalled
                        ? "STALLED"
                        : "DONE",
                  ...(claimable
                    ? {
                        filesTouched: touched,
                        verified: verdict,
                        verifiedBestEffort:
                          verdict !== undefined
                            ? d.verified_best_effort
                            : undefined,
                        verifyPolicy: policy,
                        skippedGates: skipped,
                        rounds: d.rounds,
                        remainingTasks: remaining,
                        ...(d.cost_usd !== undefined
                          ? {
                              costUsd: d.cost_usd,
                              routedProvider: d.routed_provider,
                              budgetStop: d.budget_stop,
                            }
                          : {}),
                      }
                    : {}),
                  traceRows: rows,
                };
              });
              // Refetch rather than trusting the paths: the old code inserted
              // each path with empty content, so opening a file the agent had
              // just written showed a blank editor.
              if (touched.length > 0) onFilesTouched?.(touched);
              // A turn that reached its round budget ended cleanly but is not
              // done, and a turn that failed outright has the files it already
              // wrote. Pull the server's latest status for either so the resume
              // banner appears at once — the same affordance a reload offers —
              // without rebuilding (and flickering) the just-streamed transcript.
              if (d.status === "paused" || d.status === "failed") {
                conversation
                  .get(projectId, { conversationId })
                  .then(({ latest }) =>
                    setResumable(isResumable(latest) ? latest : null),
                  )
                  .catch(() => {});
              }
              break;
            }
          }
        },
      [onFilesTouched, onPlan, projectId, conversationId],
    );

    // ── Sending ──

    const send = useCallback(
      async (
        raw: string,
        forcedMode?: "plan" | "build",
        // existingUserBubble: this send IS a queued message finally going out
        // — its bubble is already in the transcript, so no second copy is
        // pushed. Only the plan queue (see planQueueRef) passes it.
        opts?: { existingUserBubble?: string },
      ) => {
        const prompt = raw.trim();
        if (!prompt || isStreaming || uploading) return;
        const effectiveMode = forcedMode ?? mode;

        // Staged files upload first (downscaled when they are large images),
        // then the turn claims them by id. An upload failure stops the send
        // here: a turn whose chips the model never saw would be a lie.
        const outgoing = staged;
        let sent: { id: number; name: string; mime: string; previewUrl?: string }[] = [];
        if (outgoing.length > 0) {
          setUploading(true);
          setAttError(null);
          try {
            sent = [];
            for (const a of outgoing) {
              const file = await downscaleImage(a.file);
              const rec = await attachments.upload(projectId, file);
              sent.push({ id: rec.id, name: rec.name, mime: rec.mime, previewUrl: a.previewUrl });
            }
          } catch (err) {
            setAttError(err instanceof Error ? err.message : "upload failed");
            setUploading(false);
            return;
          }
          setStaged([]);
          setUploading(false);
        }

        setInput("");
        setAttError(null);
        // A fresh prompt becomes the latest turn, so the interrupted one is no
        // longer what a reload would offer to resume. Drop the banner to match.
        setResumable(null);
        filesReadRef.current = new Set();
        pinnedRef.current = true;

        const streamId = uuid();
        // Mark the stream as a plan call while it runs: submitted input
        // during it queues visibly instead of steering (a plan turn has no
        // loop to steer) — see submitInput.
        planStreamRef.current = effectiveMode === "plan";
        setMessages((prev) => [
          ...prev,
          ...(opts?.existingUserBubble
            ? []
            : [
                {
                  id: uuid(),
                  role: "user" as const,
                  content: prompt,
                  timestamp: Date.now(),
                  ...(sent.length > 0 ? { attachments: sent } : {}),
                },
              ]),
          { id: streamId, role: "assistant" as const, traceRows: [], isStreaming: true },
        ]);

        const patch = (fn: (prev: StreamMessage) => StreamMessage) =>
          setMessages((prev) =>
            prev.map((m) => {
              if (m.id !== streamId || !isAssistant(m)) return m;
              const next = fn(m);
              // Stream over (done frame or error path): settle the rows.
              if (!next.isStreaming) {
                const settled = settleRows(next.traceRows);
                if (settled !== next.traceRows) return { ...next, traceRows: settled };
              }
              return next;
            }),
          );

        abortRef.current = streamPrompt(
          projectId,
          prompt,
          // Approving a plan calls switchToBuild then sendMessage in the same
          // tick; React state has not re-rendered yet, so the closure's `mode`
          // would still read "plan" and the build would re-plan forever. The
          // explicit argument makes the caller decide, not the render cycle.
          effectiveMode,
          handleEvent(streamId, patch),
          (err) => patch((prev) => ({ ...prev, isStreaming: false, error: err.message })),
          { attachmentIds: sent.map((a) => a.id), conversationId },
        );
      },
      [handleEvent, isStreaming, uploading, mode, projectId, staged, conversationId],
    );

    // Deliver what was typed while a plan call was running: the call has
    // ended (done, error, or stop), so the composer is free again. One
    // message per turn — each flips from "queued" to an ordinary prompt as it
    // goes out, and the next one follows when that turn ends. Uploading
    // blocks delivery the same way it blocks send, so staged files never ride
    // a message the composer no longer shows.
    useEffect(() => {
      if (isStreaming || uploading) return;
      const next = planQueueRef.current[0];
      if (!next) return;
      planQueueRef.current.shift();
      setMessages((prev) =>
        prev.map((m) =>
          m.id === next.id && m.role === "user" ? { ...m, queued: false } : m,
        ),
      );
      void send(next.text, "plan", { existingUserBubble: next.id });
    }, [isStreaming, uploading, send]);

    // Resume the interrupted turn: no new user message, since this continues the
    // existing turn rather than starting one. The server replays the persisted
    // transcript and the agent picks up where it stopped; its new work streams
    // into a fresh assistant entry below the restored history.
    const resume = useCallback(() => {
      if (!resumable || isStreaming) return;
      const promptId = resumable.id;
      setResumable(null);
      // A resume runs a build loop: input during it steers like any other
      // build stream, never queues as a plan follow-up.
      planStreamRef.current = false;
        filesReadRef.current = new Set();
        pinnedRef.current = true;

        const streamId = uuid();
      setMessages((prev) => [
        ...prev,
        { id: streamId, role: "assistant", traceRows: [], isStreaming: true },
      ]);

      const patch = (fn: (prev: StreamMessage) => StreamMessage) =>
        setMessages((prev) =>
          prev.map((m) => {
            if (m.id !== streamId || !isAssistant(m)) return m;
            const next = fn(m);
            // Stream over (done frame or error path): settle the rows.
            if (!next.isStreaming) {
              const settled = settleRows(next.traceRows);
              if (settled !== next.traceRows) return { ...next, traceRows: settled };
            }
            return next;
          }),
        );

      abortRef.current = prompts.resume(
        promptId,
        handleEvent(streamId, patch),
        (err) => patch((prev) => ({ ...prev, isStreaming: false, error: err.message })),
      );
    }, [handleEvent, isStreaming, resumable]);

    // Discard the interrupted turn so a reload stops offering it. Hide the banner
    // at once; if the server rejects it (e.g. the turn is somehow still running),
    // put the banner back rather than pretend the turn was dealt with.
    const discard = useCallback(async () => {
      if (!resumable) return;
      const target = resumable;
      setResumable(null);
      try {
        await prompts.discard(target.id);
      } catch {
        setResumable(target);
      }
    }, [resumable]);

    // Answer a paused ask_user question. The reply resolves the pending call and
    // resumes the same turn: the asking entry's card locks to show what was
    // answered, and the agent's continuation streams into a fresh entry below —
    // exactly the shape a reload rebuilds (locked card, then the next assistant
    // turn). No user bubble: the answer lives in the card, live and on reload
    // alike, so the two never disagree.
    const answerQuestion = useCallback(
      (promptId: number, reply: string) => {
        const answer = reply.trim();
        if (!answer || isStreaming || promptId <= 0) return;
        // The answer resumes a paused turn's loop — a build turn's loop
        // steers, so the plan-queue classification must not leak into it.
        planStreamRef.current = false;

        // Lock the asking card. The prompt id names the paused turn uniquely, so
        // it is enough to find the one card to close.
        setMessages((prev) =>
          prev.map((m) =>
            isAssistant(m) && m.question && m.question.promptId === promptId
              ? { ...m, question: { ...m.question, answered: answer } }
              : m,
          ),
        );

        filesReadRef.current = new Set();
        pinnedRef.current = true;

        const streamId = uuid();
        setMessages((prev) => [
          ...prev,
          { id: streamId, role: "assistant", traceRows: [], isStreaming: true },
        ]);

        const patch = (fn: (prev: StreamMessage) => StreamMessage) =>
          setMessages((prev) =>
            prev.map((m) => {
              if (m.id !== streamId || !isAssistant(m)) return m;
              const next = fn(m);
              // Stream over (done frame or error path): settle the rows.
              if (!next.isStreaming) {
                const settled = settleRows(next.traceRows);
                if (settled !== next.traceRows) return { ...next, traceRows: settled };
              }
              return next;
            }),
          );

        abortRef.current = prompts.answer(
          promptId,
          answer,
          handleEvent(streamId, patch),
          (err) => patch((prev) => ({ ...prev, isStreaming: false, error: err.message })),
        );
      },
      [handleEvent, isStreaming],
    );

    // Deliver a message to the running build (H2 steer). The text lands in
    // the loop's next model request; the running stream surfaces it as a
    // marked turn. The bubble renders dimmed and labeled — honest that it is
    // a redirect, not a new prompt. A failed delivery (turn finished between
    // the phase event and here) shows the error on the bubble rather than
    // silently dropping the user's words.
    const steerRunning = useCallback(
      async (raw: string) => {
        const message = raw.trim();
        const promptId = runningPromptIdRef.current;
        if (!message || promptId <= 0) return;
        setInput("");
        setMessages((prev) => [
          ...prev,
          {
            id: uuid(),
            role: "user",
            content: message,
            timestamp: Date.now(),
            steer: true,
            steerError: null,
          },
        ]);
        try {
          await prompts.steer(promptId, message);
        } catch (err) {
          const text = err instanceof Error ? err.message : "steer failed";
          setMessages((prev) =>
            prev.map((m) =>
              m.role === "user" && m.steer && m.content === message
                ? { ...m, steerError: text }
                : m,
            ),
          );
        }
      },
      [],
    );

    // Park a message typed while a plan is being written: it lands in the
    // transcript as a queued bubble (visible, honest that it has not gone out
    // yet) and the delivery effect after `send` fires it as its own plan turn
    // the moment the current call returns. One in-flight model request cannot
    // be interrupted with new input — this replaces the old silent drop.
    function queuePlanMessage(raw: string) {
      const message = raw.trim();
      if (!message) return;
      setInput("");
      const bubbleId = uuid();
      setMessages((prev) => [
        ...prev,
        {
          id: bubbleId,
          role: "user" as const,
          content: message,
          timestamp: Date.now(),
          queued: true,
        },
      ]);
      planQueueRef.current.push({ id: bubbleId, text: message });
    }

    // Declared after `send` on purpose: reading it from above the declaration is
    // a temporal-dead-zone hazard, and it only worked because the handle body
    // does not run until after the first render.
    useImperativeHandle(ref, () => ({
      switchToBuild: () => setMode("build"),
      sendMessage: (msg: string, modeArg?: "plan" | "build") => {
        void send(msg, modeArg);
      },
    }));

    // Both the send button and Enter go through here. When a turn is waiting to
    // be resumed, typing just "resume" or "continue" triggers that rather than
    // a new prompt — the obvious thing to type when the banner is showing. Gated on
    // `resumable`, so the same words are an ordinary prompt at any other time.
    //
    // H2: while a turn streams, a submitted message steers the running build
    // (dimmed, labeled) unless queueInstead is on — then it queues for the
    // next turn like before. A turn paused on a question never steers: the
    // question card owns the conversation.
    function submitInput() {
      // A pending question owns the conversation: it is answered in its card, not
      // here, so the paused turn is never left behind by a fresh prompt.
      if (pendingQuestion) return;
      if (resumable && !isStreaming && /^(resume|continue)$/i.test(input.trim())) {
        setInput("");
        resume();
        return;
      }
      // H2 steer: while a turn runs and the queue toggle is off, a submitted
      // message goes to the running build. With the queue toggle on, the
      // message stays in the composer — it sends as the next turn when this
      // one ends. Nothing is silently dropped or held in hidden state.
      if (isStreaming) {
        if (!queueInstead) {
          // A plan call is one in-flight model request with nothing on the
          // server that can absorb a message mid-call: park it visibly as a
          // queued bubble rather than steering into a 409 or dropping it —
          // it goes out as its own plan turn when the call returns. With
          // staged files the text stays in the composer instead: a queued
          // bubble cannot show chips it has not claimed yet.
          if (planStreamRef.current) {
            if (staged.length === 0) queuePlanMessage(input);
          } else {
            void steerRunning(input);
          }
        }
        return;
      }
      void send(input);
    }

    function handleSubmit(e: React.FormEvent) {
      e.preventDefault();
      submitInput();
    }

    function handleKeyDown(e: React.KeyboardEvent<HTMLTextAreaElement>) {
      // Enter sends, Shift+Enter breaks the line. The composer used to be a
      // single-line input, so a prompt describing three screens had to be
      // written as one unbroken sentence.
      if (e.key === "Enter" && !e.shiftKey && !e.nativeEvent.isComposing) {
        e.preventDefault();
        submitInput();
      }
    }

    function stop() {
      abortRef.current?.abort();
      setMessages((prev) =>
        prev.map((m) =>
          isAssistant(m) && m.isStreaming
            ? {
                ...m,
                isStreaming: false,
                error: "You stopped this turn. The files it already wrote are still there.",
                // Settle the rows like every other exit path does: a stopped
                // turn must not leave narration shimmering forever.
                traceRows: m.traceRows.map((r) =>
                  r.status === "running"
                    ? { ...r, status: "fail", streaming: false }
                    : r.streaming
                      ? { ...r, streaming: false }
                      : r,
                ),
              }
            : m,
        ),
      );
    }

    // Grow the composer with its content, up to a ceiling.
    useEffect(() => {
      const el = textareaRef.current;
      if (!el) return;
      el.style.height = "auto";
      el.style.height = `${Math.min(el.scrollHeight, 168)}px`;
    }, [input]);

    const approvePlan = (plan: PlanData) => {
      // The layout owns what approval means, so the card here and the full plan
      // view in the centre panel cannot drift apart.
      onApprovePlan?.(plan);
    };

    // Jump the stream to a restored turn: scrolls the first message row
    // carrying its prompt id into view and flashes it once. Returns whether
    // the turn is currently rendered — live turns and trimmed pages are not,
    // and the drawer says so instead of pretending.
    const jumpToTurn = useCallback((promptId: number) => {
      const el = scrollRef.current?.querySelector(
        `[data-prompt-turn="${promptId}"]`,
      );
      if (!(el instanceof HTMLElement)) return false;
      pinnedRef.current = false;
      el.scrollIntoView({ block: "center" });
      el.classList.add("turn-flash");
      window.setTimeout(() => el.classList.remove("turn-flash"), 1600);
      return true;
    }, []);

    return (
      <section className="flex h-full min-h-0 min-w-[280px] flex-col overflow-hidden bg-[var(--bg)]">
        {/* No panel header: the icon rail owns navigation, and the canvas is
            the conversation. What was a bay-head is now nothing at all. */}

        <div
          ref={scrollRef}
          onScroll={onScroll}
          className="stream"
        >
          <ChatHistory
            key={restoreKey}
            projectId={projectId}
            prompts={pastPrompts}
            onPick={(text) => {
              setInput(text);
              textareaRef.current?.focus();
            }}
            onNewChat={() => onNewThread?.()}
            onJump={jumpToTurn}
          />
          {restoring && messages.length === 0 && (
            <p className="gauge py-4 text-center">Loading this project&rsquo;s history</p>
          )}

          {!restoring && messages.length === 0 && (
            <div className="flex h-full flex-col justify-center px-1 sm:px-2">
              <p className="text-[13px] font-medium tracking-tight text-paper">
                {mode === "plan"
                  ? "Describe the app you want."
                  : "Say what to build next."}
              </p>
              <p className="mt-1.5 text-xs text-fog">
                {mode === "plan"
                  ? "Plan mode talks it through — screens, navigation, which packages are needed — and writes nothing until you approve it."
                  : "Build mode writes files into the project, type-checks them, and installs the result on the device."}
              </p>
              <div className="mt-5 flex flex-col gap-1.5">
                {(mode === "plan"
                  ? [
                      "A wallet app that shows my SOL balance and recent transactions",
                      "A ticket scanner that verifies NFT tickets with the camera",
                    ]
                  : [
                      "Add a pull-to-refresh to the balance screen",
                      "Wire the Connect button to Mobile Wallet Adapter",
                    ]
                ).map((example) => (
                  <button
                    key={example}
                    type="button"
                    onClick={() => {
                      setInput(example);
                      textareaRef.current?.focus();
                    }}
                    className="surface-2 rounded-lg px-2.5 py-2 text-left text-xs leading-snug text-fog transition-colors duration-150 ease-out hover:bg-[var(--surface-3)] hover:text-mist"
                  >
                    {example}
                  </button>
                ))}
              </div>
            </div>
          )}

          <div className="flex min-w-0 flex-col">
            {messages.map((msg, i) => {
              // A turn label before every assistant entry that follows
              // another turn — the transcript reads as turns, not as one
              // undifferentiated stack of rows. First entry needs no label.
              const divider =
                isAssistant(msg) && i > 0 ? (
                  <div key={`d-${msg.id}`} className="turn-label" aria-hidden>
                    new turn
                  </div>
                ) : null;
              return (
                <Fragment key={msg.id}>
                  {divider}
                  {isAssistant(msg) ? (
                    <div
                      className="msg assistant"
                      {...(msg.promptId !== undefined
                        ? { "data-prompt-turn": msg.promptId }
                        : {})}
                    >
                      <span className="who" aria-hidden>
                        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={1.75} strokeLinecap="round" strokeLinejoin="round">
                          <path d="M12 3l1.9 5.6L19.5 10l-5.6 1.9L12 17.5 10.1 12 4.5 10l5.6-1.4z" />
                          <path d="M19 3.5l.7 2 2 .7-2 .7-.7 2-.7-2-2-.7 2-.7z" />
                        </svg>
                      </span>
                      <div className="body">
                        <AssistantEntry
                          msg={msg}
                          onApprove={approvePlan}
                          onAnswer={answerQuestion}
                          busy={isStreaming}
                          planTasks={planTasks}
                        />
                        {/*
                          The feedback row sits under each finished assistant
                          message. It is deliberately absent while a message is
                          still streaming: rating half a thought is noise. The
                          rating lives in the sidecar, never the session log,
                          so the model never replays a human judgement of its
                          own output and starts performing for it.
                        */}
                        {!isStreaming && (msg.text ?? "").trim() ? (
                          <FeedbackRow
                            msg={msg}
                            projectId={projectId}
                            onRate={(rating) =>
                              setMessages((prev) =>
                                prev.map((m) =>
                                  m.id === msg.id ? { ...m, rating } : m,
                                ),
                              )
                            }
                          />
                        ) : null}
                      </div>
                    </div>
                  ) : (
                    <div
                      className="msg user"
                      {...(msg.promptId !== undefined
                        ? { "data-prompt-turn": msg.promptId }
                        : {})}
                    >
                      <span className="who" aria-hidden>
                        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={1.75} strokeLinecap="round" strokeLinejoin="round">
                          <path d="M21 11.5a8.4 8.4 0 0 1-8.5 8.4c-1.5 0-3-.4-4.2-1.1L3 20l1.2-5.3A8.4 8.4 0 1 1 21 11.5z" />
                        </svg>
                      </span>
                      <div className="body">
                        <div className="flex min-w-0 flex-col items-end gap-2">
                          <div className="bubble whitespace-pre-wrap break-words text-sm leading-relaxed">
                            {msg.queued ? (
                              <span className="mr-1.5 font-mono text-[10px] uppercase tracking-wider text-[var(--accent-2)]/70">
                                queued — sends when the plan lands
                              </span>
                            ) : msg.steer ? (
                              <span className="mr-1.5 font-mono text-[10px] uppercase tracking-wider text-[var(--accent-2)]/70">
                                {msg.steerError ? "steer failed" : "steer — not a new turn"}
                              </span>
                            ) : null}
                            {stripInlineDocs(msg.content)}
                            {msg.steer && msg.steerError ? (
                              <div className="mt-1 text-[11px] text-[var(--err)]">{msg.steerError}</div>
                            ) : null}
                          </div>
                          {msg.attachments && msg.attachments.length > 0 ? (
                            <div className="flex justify-end">
                              <MessageAttachments projectId={projectId} items={msg.attachments} />
                            </div>
                          ) : null}
                        </div>
                      </div>
                    </div>
                  )}
                </Fragment>
              );
            })}
          </div>

          <div ref={endRef} />
        </div>

        {/* Live status — the one place that says, in the moment, what the agent
            is doing, what it is touching, and how long it has been at it. It
            rides at the bottom of the stream, above the composer, as a tool
            row in the same vocabulary — not as a separate panel. */}
        {isStreaming && (
          <div className="px-7 pb-2">
            <div className="mx-auto flex max-w-[760px] items-center gap-2.5 rounded-xl bg-[var(--panel-2)] px-3 py-2">
              <LoadingState variant="Dots" label={phaseLabel(liveMessage?.phase) || "Working"} />
              {liveMessage?.round && liveMessage.round > 1 ? (
                <span className="gauge flex-shrink-0">round {liveMessage.round}</span>
              ) : null}
              {liveTarget ? (
                <span
                  dir="rtl"
                  title={liveTarget}
                  className="min-w-0 flex-1 truncate text-left font-mono text-xs text-fog"
                >
                  <span dir="ltr">{liveTarget}</span>
                </span>
              ) : (
                <span className="flex-1" />
              )}
            </div>
          </div>
        )}

        {/* The agent's question renders as a modal bottom sheet (portaled
            to <body> — see QuestionCard) so it floats over the whole shell
            like Claude Desktop's AskUserQuestion. Answered, AssistantEntry
            shows the small "Answer sent" receipt inline. */}
        {pendingQuestion && (
          <QuestionCard
            question={pendingQuestion}
            busy={isStreaming}
            onAnswer={(reply) => answerQuestion(pendingQuestion.promptId, reply)}
          />
        )}

        {/* An interrupted turn waits on a human decision: continue it from where
            it stopped, or set it aside. Neutral, not fault — the work
            is not broken, it is paused. Hidden while streaming, when the status
            strip above owns this row. */}
        {resumable && !isStreaming && (
          <div className="px-7 pb-2">
            <div className="mx-auto flex max-w-[760px] items-center gap-2.5 rounded-xl bg-[var(--panel-2)] px-3 py-2">
              <span className="lamp flex-shrink-0" data-state="waiting" />
              <div className="min-w-0 flex-1">
                <p className="text-xs text-paper">
                  This turn was interrupted before it finished.
                </p>
                <p className="gauge">
                  {resumable.status === "running"
                    ? "the server stopped before it finished"
                    : resumable.status === "cancelled"
                      ? "it was stopped early"
                      : resumable.status === "paused"
                        ? "it hit its round budget"
                        : "it ended with an error"}
                  {resumable.round > 0 ? ` · reached round ${resumable.round}` : ""}
                </p>
              </div>
              <button type="button" onClick={resume} className="btn btn-go h-7 flex-shrink-0">
                Resume
              </button>
              <button
                type="button"
                onClick={() => void discard()}
                className="btn btn-ghost h-7 flex-shrink-0"
              >
                Discard
              </button>
            </div>
          </div>
        )}

        {/* The floating composer — the canvas's single input, riding above
            the stream on a gradient so the last message stays readable
            underneath it. */}
        <div className="composer">
          {isStreaming && pacingSeconds > 0 ? (
            // The honest clock: a long quiet stretch in a paced turn is
            // the gateway interval, not a hang. Say so while it happens.
            <p className="composer-meta mb-1.5 px-1 text-right text-fog/70">
              paced {pacingSeconds}s between model calls — long pauses are the
              rate limit, not a stall
            </p>
          ) : null}
          <ComposerAttachments items={staged} onRemove={removeStaged} error={attError} />
          <form
            onSubmit={handleSubmit}
            className={`composer-in${dragging ? " drag" : ""}`}
            onPaste={(e) => {
              const files = e.clipboardData?.files;
              if (files && files.length > 0) addFiles(files);
            }}
            onDragOver={(e) => {
              if (pendingQuestion) return;
              if (Array.from(e.dataTransfer.types).includes("Files")) {
                e.preventDefault();
                setDragging(true);
              }
            }}
            onDragLeave={() => setDragging(false)}
            onDrop={(e) => {
              e.preventDefault();
              setDragging(false);
              const files = e.dataTransfer.files;
              if (files && files.length > 0) addFiles(files);
            }}
          >
            <textarea
              ref={textareaRef}
              rows={1}
              value={input}
              onChange={(e) => setInput(e.target.value)}
              onKeyDown={handleKeyDown}
              placeholder={
                pendingQuestion
                  ? "Answer the question above to continue…"
                  : isStreaming && !queueInstead
                    ? "Steer the running build…"
                    : isStreaming
                      ? "Queue for the next turn…"
                      : mode === "plan"
                        ? "Describe the app…"
                        : "What should I build?"
              }
              className="min-h-4 flex-1 resize-none bg-transparent text-sm leading-[22px] text-[var(--label)] outline-none placeholder:text-[var(--label-4)]"
              disabled={!!pendingQuestion}
              aria-label="Chat prompt"
            />
            <div className="right flex items-center gap-1.5">
              <button
                type="button"
                onClick={() => fileInputRef.current?.click()}
                disabled={isStreaming || uploading || !!pendingQuestion}
                title="Attach an image or markdown file"
                aria-label="Attach an image or markdown file"
                className="icon-btn"
              >
                <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2} strokeLinecap="round" strokeLinejoin="round" aria-hidden>
                  <path d="M12 5v14M5 12h14" />
                </svg>
              </button>
              <input
                ref={fileInputRef}
                type="file"
                hidden
                multiple
                accept="image/png,image/jpeg,image/webp,.md"
                onChange={(e) => {
                  if (e.target.files) addFiles(e.target.files);
                  e.target.value = "";
                }}
                aria-hidden
                tabIndex={-1}
              />
              {/* The segmented mode pill — plan ⇄ build — lives in the
                  composer, where the decision is made. */}
              {!isStreaming ? (
                <div
                  role="group"
                  aria-label="What the agent should do with your next message"
                  className="seg"
                >
                  {(["plan", "build"] as const).map((m) => (
                    <button
                      key={m}
                      type="button"
                      onClick={() => setMode(m)}
                      aria-pressed={mode === m}
                      title={
                        m === "plan"
                          ? "Talk through the app first. Nothing gets written."
                          : "Write the code and run the checks."
                      }
                    >
                      {m === "plan" ? "Plan" : "Build"}
                    </button>
                  ))}
                </div>
              ) : (
                /* H2: while a turn runs, submitted messages steer it by
                   default. The toggle opts the next message into the old
                   behavior — queue for the next turn — one submission at a
                   time, so the common case needs no click. */
                <button
                  type="button"
                  onClick={() => setQueueInstead((q) => !q)}
                  title={
                    queueInstead
                      ? "Next message will queue for the next turn"
                      : "Next message will steer the running build"
                  }
                  aria-pressed={queueInstead}
                  className={[
                    "flex h-7 items-center gap-1 rounded-full px-2.5 font-mono text-[10px] uppercase tracking-wider transition-colors duration-150 ease-out",
                    queueInstead
                      ? "bg-[var(--accent-soft)] text-[var(--accent-2)]"
                      : "surface-2 text-fog hover:text-mist",
                  ].join(" ")}
                >
                  {queueInstead ? "Queue instead" : "Steers the build"}
                </button>
              )}
              {isStreaming ? (
                <button
                  type="button"
                  onClick={stop}
                  className="btn btn-stop h-7 flex-shrink-0"
                >
                  Stop
                </button>
              ) : null}
              <button
                type="submit"
                title={
                  isStreaming
                    ? queueInstead
                      ? "Queue for the next turn"
                      : "Steer the running build"
                    : mode === "plan"
                      ? "Ask"
                      : "Build"
                }
                aria-label="Send"
                disabled={!input.trim() || !!pendingQuestion || uploading}
                className="send-btn flex-shrink-0"
              >
                <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden>
                  <path d="M12 19V5M5 12l7-7 7 7" />
                </svg>
              </button>
            </div>
          </form>
          {/* The meta row: the session's honesty — live state, token budget,
              and the pacing clock. cache_read is the budget-saver; it rides
              the title attribute. */}
          <p className="composer-meta select-none">
            {useIDEStore.getState().previewStatus === "streaming" ? (
              <span className="ok">● session live</span>
            ) : (
              <span>session idle</span>
            )}
            {sessionUsage ? (
              <>
                <span className="text-[var(--label-4)]">·</span>
                <span title={`${sessionUsage.cache_read.toLocaleString()} cache-read tokens`}>
                  {compactTokens(sessionUsage.in)} in · {compactTokens(sessionUsage.out)} out
                </span>
              </>
            ) : null}
            <span className="ml-auto text-[var(--label-4)]">Enter sends · Shift+Enter adds a line</span>
          </p>
        </div>
      </section>
    );
  },
);

AIChat.displayName = "AIChat";
export default AIChat;

// ── Chat history ──
// Past prompts for reuse, plus server search across stored turns with
// jump-to-turn. New chats are threads (the rail lists them), so the button
// starts a thread instead of clearing anything — nothing here deletes.
function ChatHistory({
  projectId,
  prompts,
  onPick,
  onNewChat,
  onJump,
}: {
  projectId: number;
  /** Past user prompts, most recent first. Tap refills the composer. */
  prompts: string[];
  onPick: (text: string) => void;
  /** Start a new chat thread for this project. */
  onNewChat: () => void;
  /** Scroll the stream to the turn; returns whether it is rendered. */
  onJump: (promptId: number) => boolean;
}) {
  const [open, setOpen] = useState(false);
  const [searchOpen, setSearchOpen] = useState(false);
  const [query, setQuery] = useState("");
  const [expanded, setExpanded] = useState<number | null>(null);
  const [found, setFound] = useState<{ q: string; turns: ConversationTurn[] } | null>(null);
  const [jumpMiss, setJumpMiss] = useState<number | null>(null);
  const searchRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    if (searchOpen) searchRef.current?.focus();
  }, [searchOpen]);

  // Debounced server search: two characters minimum. Only the debounced
  // callback writes state; the searching flag derives.
  useEffect(() => {
    const q = query.trim();
    if (q.length < 2) return;
    let cancelled = false;
    const t = window.setTimeout(() => {
      conversation
        .search(projectId, q)
        .then((r) => {
          if (!cancelled) setFound({ q, turns: r.turns });
        })
        .catch(() => {
          if (!cancelled) setFound({ q, turns: [] });
        });
    }, 300);
    return () => {
      cancelled = true;
      window.clearTimeout(t);
    };
  }, [query, projectId]);

  if (prompts.length === 0) return null;
  const visible = prompts.filter((pr) =>
    pr.toLowerCase().includes(query.trim().toLowerCase()),
  );
  const searchingUI = query.trim().length >= 2;
  const results =
    searchingUI && found && found.q === query.trim() ? found.turns : null;
  const searching = searchingUI && results === null;

  function jump(id: number) {
    if (onJump(id)) {
      setJumpMiss(null);
      setOpen(false);
    } else {
      // Not rendered: trimmed by the page window or still streaming.
      // Say so instead of pretending.
      setJumpMiss(id);
    }
  }

  return (
    <div className="mb-2">
      <div className="flex items-center gap-1">
        <button
          type="button"
          aria-expanded={open}
          onClick={() => setOpen((v) => !v)}
          className="flex h-7 min-w-0 flex-1 items-center gap-1.5 rounded-md px-1.5 text-left transition-colors hover:bg-white/5"
        >
          <span className="gauge">History</span>
          <span className="gauge tnum">{prompts.length}</span>
          <svg
            width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="var(--color-ash)" strokeWidth="2.2" strokeLinecap="round" strokeLinejoin="round"
            className="ml-auto shrink-0 transition-transform duration-300"
            style={{ transform: open ? "rotate(180deg)" : "rotate(0)" }}
            aria-hidden
          >
            <path d="M6 9l6 6 6-6" />
          </svg>
        </button>
        <button
          type="button"
          title="New chat in this project"
          aria-label="New chat in this project"
          onClick={onNewChat}
          className="flex size-7 shrink-0 items-center justify-center rounded-md text-ash transition-colors hover:bg-white/5 hover:text-mist"
        >
          <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden>
            <path d="M12 5v14M5 12h14" />
          </svg>
        </button>
        <button
          type="button"
          aria-label="Search history"
          aria-expanded={searchOpen}
          onClick={() => setSearchOpen((v) => !v)}
          className="flex size-7 shrink-0 items-center justify-center rounded-md text-ash transition-colors hover:bg-white/5 hover:text-mist"
        >
          <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" aria-hidden>
            <circle cx="11" cy="11" r="7" />
            <path d="M21 21l-4.3-4.3" />
          </svg>
        </button>
      </div>

      {searchOpen && (
        <div className="surface-1 mt-1 flex h-8 items-center gap-1.5 rounded-lg px-2 transition-colors focus-within:bg-[var(--surface-2)]">
          <input
            ref={searchRef}
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Escape") {
                setSearchOpen(false);
                setQuery("");
              }
            }}
            placeholder="Search prompts and turns"
            aria-label="Search prompt history"
            className="min-w-0 flex-1 bg-transparent text-xs text-mist outline-none placeholder:text-ash"
          />
          {(visible.length > 0 || query) && (
            <button
              type="button"
              aria-label="Close history search"
              onClick={() => {
                setSearchOpen(false);
                setQuery("");
              }}
              className="flex size-6 shrink-0 items-center justify-center rounded-md text-ash transition-colors hover:text-mist"
            >
              <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.2" strokeLinecap="round" aria-hidden>
                <path d="M18 6L6 18M6 6l12 12" />
              </svg>
            </button>
          )}
        </div>
      )}

      <div
        className="grid transition-[grid-template-rows,opacity] duration-300"
        style={{
          gridTemplateRows: open ? "1fr" : "0fr",
          opacity: open ? 1 : 0,
          transitionTimingFunction: "cubic-bezier(0.23, 1, 0.32, 1)",
        }}
      >
        <div className="overflow-hidden">
          <div className="flex flex-col gap-px py-1">
            {visible.map((prompt) => (
              <button
                key={prompt}
                type="button"
                title={prompt}
                onClick={() => onPick(prompt)}
                className="flex h-8 min-w-0 items-center rounded-md px-2 text-left transition-colors hover:bg-white/5"
              >
                <span className="min-w-0 flex-1 truncate text-xs text-fog transition-colors hover:text-mist">
                  {prompt}
                </span>
              </button>
            ))}
            {visible.length === 0 && !searchingUI && (
              <p className="px-2 py-1.5 text-xs text-ash">No prompts match.</p>
            )}
            {searchingUI && (
              <div className="mt-1 border-t border-white/5 pt-1">
                <p className="gauge px-2 py-1">Matches in stored turns</p>
                {searching && <p className="gauge px-2 py-1">…</p>}
                {(results ?? []).map((t, i) => {
                  const excerpt =
                    t.blocks
                      .filter((b) => b.type === "text" && b.text?.trim())
                      .map((b) => b.text as string)
                      .join(" ")
                      .slice(0, 160) || "(no prose in this turn)";
                  const pid = t.prompt_id ?? -1;
                  return (
                    <TurnRow
                      key={`${pid}-${i}`}
                      title={excerpt}
                      meta={t.role}
                      expanded={expanded === pid && pid > 0}
                      onToggle={() => {
                        setExpanded((v) => (v === pid ? null : pid));
                        setJumpMiss(null);
                      }}
                      detail={
                        pid > 0 ? (
                          <div className="mt-1.5 flex gap-1.5">
                            <button
                              type="button"
                              onClick={() => jump(pid)}
                              className="btn h-7 flex-shrink-0 px-2.5 text-xs"
                            >
                              Jump to turn
                            </button>
                            {jumpMiss === pid && (
                              <p className="mt-1.5 font-mono text-[11px] text-fog">
                                That turn is not in the loaded transcript.
                              </p>
                            )}
                          </div>
                        ) : undefined
                      }
                    />
                  );
                })}
                {searchingUI && !searching && (results ?? []).length === 0 && (
                  <p className="px-2 py-1.5 text-xs text-ash">No turns match.</p>
                )}
              </div>
            )}
          </div>
        </div>
      </div>
    </div>
  );
}

// TurnRow is one history row: title plus meta, folding open into detail.
// Shared by the turn index and search results so both read the same.
function TurnRow({
  title,
  meta,
  expanded,
  onToggle,
  detail,
}: {
  title: string;
  meta: string;
  expanded: boolean;
  onToggle: () => void;
  detail?: React.ReactNode;
}) {
  return (
    <div>
      <button
        type="button"
        aria-expanded={expanded}
        onClick={onToggle}
        title={title}
        className="flex min-h-8 w-full min-w-0 items-center gap-2 rounded-md px-2 py-1.5 text-left transition-colors hover:bg-white/5"
      >
        <span className="min-w-0 flex-1">
          <span className="block truncate text-xs text-fog transition-colors hover:text-mist">
            {title}
          </span>
          <span className="gauge mt-0.5 block">{meta}</span>
        </span>
        <svg
          width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="var(--color-ash)" strokeWidth="2.2" strokeLinecap="round" strokeLinejoin="round"
          className="shrink-0 transition-transform duration-300"
          style={{ transform: expanded ? "rotate(180deg)" : "rotate(0)" }}
          aria-hidden
        >
          <path d="M6 9l6 6 6-6" />
        </svg>
      </button>
      <div
        className="grid transition-[grid-template-rows,opacity] duration-300"
        style={{
          gridTemplateRows: expanded ? "1fr" : "0fr",
          opacity: expanded ? 1 : 0,
          transitionTimingFunction: "cubic-bezier(0.23, 1, 0.32, 1)",
        }}
      >
        <div className="overflow-hidden">
          <div className="px-2 pb-2 pt-0.5">{detail}</div>
        </div>
      </div>
    </div>
  );
}

// ── One assistant turn ──

function AssistantEntry({
  msg,
  onApprove,
  onAnswer,
  busy,
  planTasks,
}: {
  msg: StreamMessage;
  onApprove: (plan: PlanData) => void;
  onAnswer: (promptId: number, reply: string) => void;
  busy: boolean;
  /** The live build checklist. Only the message that is streaming right now
      shows it — a finished turn's plan is settled, its tasks are history. */
  planTasks?: PlanTask[];
}) {
  const waiting = msg.isStreaming && msg.traceRows.length === 0 && !msg.error;
  // A const, not msg.question inline, so the closure below keeps the narrowing.
  const question = msg.question;

  return (
    <div className="rise flex min-w-0 flex-col gap-2">
      <AgentTrace rows={msg.traceRows} />

      {waiting && <p className="gauge py-1">Thinking</p>}

      {msg.plan && (
        <PlanCard
          plan={msg.plan}
          onApprove={onApprove}
          tasks={msg.isStreaming ? planTasks : undefined}
        />
      )}

      {/* A pending question anchors above the composer instead (see the
          canvas's own render, just below the message list) — Claude
          Desktop's pattern for "the agent needs something from you": a
          question is a decision waiting on the person, not another line of
          scrollback. Once answered it settles into this small receipt,
          exactly where the exchange happened. */}
      {question && question.answered !== undefined && (
        <QuestionCard
          question={question}
          busy={busy}
          onAnswer={(reply) => onAnswer(question.promptId, reply)}
        />
      )}

      {msg.error && (
        <p className="rounded-xl border border-coral-red/30 bg-coral-red/10 px-3 py-2 text-xs text-coral-red">
          {msg.error}
        </p>
      )}

      <TurnFooter msg={msg} />
    </div>
  );
}

function TurnFooter({ msg }: { msg: StreamMessage }) {
  if (msg.isStreaming) return null;

  // Evidence decides whether there is anything to report: a legacy restored
  // marker predates the verify payload (row 44), so its footer states only
  // what the marker actually knows (rounds) and claims nothing about checks
  // in either direction. `verified`'s PRESENCE is the flag.
  const skips = msg.skippedGates ?? [];
  const hasEvidence =
    msg.verified !== undefined ||
    (msg.filesTouched?.length ?? 0) > 0 ||
    (msg.remainingTasks?.length ?? 0) > 0 ||
    msg.costUsd !== undefined ||
    msg.rounds !== undefined;
  const usageParts =
    msg.usage?.input_tokens || msg.usage?.output_tokens
      ? `${compactTokens(msg.usage?.input_tokens ?? 0)} in · ${compactTokens(
          msg.usage?.output_tokens ?? 0,
        )} out`
      : "";
  if (!hasEvidence && !usageParts) return null;

  // Every chip explains itself on hover: color never carries the claim
  // alone — policy, skipped gates, and what remains sit in the tooltip.
  const titleBits: string[] = [];
  if (msg.verifyPolicy) titleBits.push(`verify policy: ${msg.verifyPolicy}`);
  if (skips.length) titleBits.push(`did not run: ${skips.join(", ")}`);
  if (msg.remainingTasks?.length) titleBits.push(`left: ${msg.remainingTasks.join(", ")}`);
  if (msg.budgetStop) titleBits.push(`budget stop: ${msg.budgetStop}`);
  const verifyTitle = titleBits.length ? titleBits.join(" · ") : undefined;

  const chips: React.ReactNode[] = [];
  if (msg.verified === true) {
    chips.push(
      msg.verifiedBestEffort || skips.length > 0 ? (
        <span key="verify" className="text-amber-400" title={verifyTitle}>
          verified{skips.length ? ` · skipped: ${skips.join(", ")}` : " (best effort)"}
        </span>
      ) : (
        <span key="verify" className="text-[var(--ok)]" title={verifyTitle}>
          verified
        </span>
      ),
    );
  } else if (msg.verified === false) {
    // Red only when evidence says so: an absent payload renders nothing.
    chips.push(
      <span key="verify" className="text-coral-red" title={verifyTitle}>
        checks did not pass
      </span>,
    );
  }
  if (msg.remainingTasks?.length) {
    chips.push(
      <span key="left" className="text-amber-400" title={msg.remainingTasks.join(", ")}>
        {msg.remainingTasks.length} left
      </span>,
    );
  }

  const parts: React.ReactNode[] = [];
  if (msg.filesTouched?.length) {
    parts.push(
      <span key="f">
        {msg.filesTouched.length} {msg.filesTouched.length === 1 ? "file" : "files"}
      </span>,
    );
  }
  if (msg.rounds) {
    parts.push(
      <span key="r">
        {msg.rounds} {msg.rounds === 1 ? "round" : "rounds"}
      </span>,
    );
  }
  if (msg.costUsd) {
    const costBits = [
      msg.routedProvider ? `via ${msg.routedProvider}` : "",
      msg.budgetStop ? `budget stop: ${msg.budgetStop}` : "",
    ].filter(Boolean);
    parts.push(
      <span key="c" title={costBits.length ? costBits.join(" · ") : undefined}>
        ${msg.costUsd.toFixed(3)}
      </span>,
    );
  }
  if (usageParts) parts.push(<span key="u">{usageParts}</span>);

  if (chips.length === 0 && parts.length === 0) return null;
  const nodes = [...chips, ...parts];
  return (
    <p className="gauge tnum flex flex-wrap items-center gap-2">
      {nodes.map((node, i) => (
        <Fragment key={i}>
          {i > 0 ? <span aria-hidden>·</span> : null}
          {node}
        </Fragment>
      ))}
    </p>
  );
}

function PlanCard({
  plan,
  onApprove,
  tasks,
}: {
  plan: PlanData;
  onApprove: (plan: PlanData) => void;
  /** Live task ticks. Present while the plan's turn is building — the
      checklist replaces the static screen list, so you watch each item
      go pending → in_progress → completed as the backend reports it. */
  tasks?: PlanTask[];
}) {
  const screens = plan.screens ?? [];
  const features = plan.features ?? [];
  const packages = plan.packages ?? [];
  const structure = plan.structure;
  const live = tasks && tasks.length > 0;
  const done = tasks?.filter((t) => t.status === "completed").length ?? 0;

  return (
    <div className="card !p-0 overflow-hidden !shadow-none">
      {/* While the turn builds, the static screen list becomes a live
          checklist: each item ticks pending → in_progress → completed as
          the backend reports it. The count is the honest progress bar. */}
      {live ? (
        <div className="border-b border-graphite p-3">
          <p className="gauge mb-2 flex items-baseline justify-between">
            <span>Building the plan</span>
            <span className="tnum">
              {done}/{tasks!.length} done
            </span>
          </p>
          <PlanProgress tasks={tasks} />
        </div>
      ) : screens.length > 0 ? (
        <div className="border-b border-graphite p-3">
          <p className="gauge mb-2">
            {screens.length} {screens.length === 1 ? "screen" : "screens"}
          </p>
          <ul className="flex flex-col gap-2">
            {screens.map((screen, i) => (
              <li key={`${screen.name}-${i}`} className="flex gap-2">
                <span className="gauge tnum mt-0.5 flex-shrink-0">
                  {String(i + 1).padStart(2, "0")}
                </span>
                <span className="min-w-0">
                  <span className="block text-[13px] font-medium text-mist">{screen.name}</span>
                  {screen.description && (
                    <span className="block text-[13px] leading-relaxed text-fog">
                      {screen.description}
                    </span>
                  )}
                </span>
              </li>
            ))}
          </ul>
        </div>
      ) : null}

      {structure && (structure.prune.length > 0 || structure.write.length > 0) && (
        <div className="border-b border-graphite p-3">
          <p className="gauge mb-2">Structure — from the template manifest</p>
          <ul className="flex flex-col gap-1.5 font-mono text-xs">
            {structure.prune.map((p, i) => (
              <li key={`prune-${i}`} className="flex items-center gap-2 text-fog">
                <span className="text-coral-red/80">−</span>
                <span className="min-w-0 truncate" title={p}>{p}</span>
              </li>
            ))}
            {structure.write.map((p, i) => (
              <li key={`write-${i}`} className="flex items-center gap-2 text-fog">
                <span className="text-pulse-green/80">+</span>
                <span className="min-w-0 truncate" title={p}>{p}</span>
              </li>
            ))}
          </ul>
          {structure.folders && (
            <p className="mt-2 text-ash">{structure.folders}</p>
          )}
        </div>
      )}

      {features.length > 0 && (
        <div className="border-b border-graphite p-3">
          <p className="gauge mb-2">Features</p>
          <div className="flex flex-wrap gap-1">
            {features.map((f, i) => (
              <span
                key={`${f}-${i}`}
                className="rounded-[4px] border border-graphite bg-white/5 px-1.5 py-0.5 font-mono text-xs text-fog"
              >
                {f}
              </span>
            ))}
          </div>
        </div>
      )}

      {plan.navigation && (
        <div className="border-b border-graphite p-3">
          <p className="gauge mb-1">Navigation</p>
          <p className="text-[13px] text-mist">{plan.navigation}</p>
        </div>
      )}

      {packages.length > 0 && (
        <div className="border-b border-graphite p-3">
          <p className="gauge mb-2">Packages to install</p>
          <div className="flex flex-wrap gap-x-3 gap-y-1 font-mono text-xs text-fog">
            {packages.map((p, i) => (
              <span key={`${p}-${i}`}>{p}</span>
            ))}
          </div>
        </div>
      )}

      <div className="flex items-center justify-between gap-3 p-3">
        <span className="font-mono text-xs tabular-nums text-ash">plan · ready to build</span>
        <div className="flex items-center gap-1.5">
          <span className="text-xs text-ash">or keep refining above</span>
          <button type="button" onClick={() => onApprove(plan)} className="btn btn-go">
            Build this ⏎
          </button>
        </div>
      </div>
    </div>
  );
}

// ── ask_user question ──
// The agent paused to ask something. Unanswered, this is the one interactive
// thing in the turn: pick a suggested choice or type a reply, and the turn
// resumes. Answered, it locks to a plain record of what was asked and said —
// the same shape a reload rebuilds, so the card never contradicts itself.
function QuestionCard({
  question,
  onAnswer,
  busy,
}: {
  question: NonNullable<StreamMessage["question"]>;
  onAnswer: (reply: string) => void;
  busy: boolean;
}) {
  const [draft, setDraft] = useState("");
  // Multi-select mode keeps a set of picked choices; the freeform draft can be
  // added alongside them. Single-select fires on click (this stays empty).
  // Declared with `draft` above the answered-receipt early return: hooks run in
  // the same order on every render regardless of which branch returns.
  const [picked, setPicked] = useState<Set<number>>(new Set());
  const multi = !!question.multiSelect;

  if (question.answered !== undefined) {
    return (
      <div className="flex w-fit items-center gap-3" style={{ animation: "pop-in 260ms cubic-bezier(0.23,1,0.32,1) both" }}>
        <span className="inline-flex items-center gap-1.5 rounded-full bg-pulse-green/10 py-1 pr-2.5 pl-1 text-[13px] font-medium text-[#4cc069]">
          <span className="flex size-4 items-center justify-center rounded-full bg-pulse-green text-white">
            <svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="3" strokeLinecap="round" strokeLinejoin="round"><path d="M20 6L9 17l-5-5" /></svg>
          </span>
          Answer sent
        </span>
      </div>
    );
  }

  const submit = (reply: string) => {
    if (busy) return;
    const r = reply.trim();
    if (!r) return;
    onAnswer(r);
  };

  const submitMulti = () => {
    if (busy) return;
    const parts = Array.from(picked)
      .sort((a, b) => a - b)
      .map((i) => question.choices[i])
      .filter(Boolean);
    const draftTrim = draft.trim();
    if (draftTrim) parts.push(draftTrim);
    if (parts.length === 0) return;
    onAnswer(parts.join(", "));
  };

  const togglePick = (i: number) => {
    setPicked((prev) => {
      const next = new Set(prev);
      if (next.has(i)) next.delete(i);
      else next.add(i);
      return next;
    });
  };

  // ── Bottom sheet: portaled to <body> so it can float over the whole
  // viewport (backdrop dims the shell, sheet slides up from below), and
  // stays out of the chat's overflow-clipping. Clicking outside does NOT
  // dismiss — the agent is genuinely blocked on this answer, hiding the
  // sheet would strand the paused turn.
  //
  // Single-select (multi === false): a click on any choice fires the reply
  // immediately (no explicit Send needed) — one option becomes the answer.
  // Multi-select (multi === true): choices toggle, freeform text appends to
  // the picked set, and Send joins them into one comma-separated answer so
  // the same tool_result shape works either way.
  if (typeof document === "undefined") return null;
  return createPortal(
    <div
      role="dialog"
      aria-modal="true"
      aria-label="Talon needs your input"
      className="fixed inset-0 z-[60] flex items-end justify-center"
      style={{ animation: "fade-in 220ms ease both" }}
    >
      <div className="absolute inset-0 bg-black/50 backdrop-blur-sm" aria-hidden />
      <div
        className="relative m-3 w-full max-w-[640px] overflow-hidden rounded-2xl border border-[var(--seam-strong)] bg-[var(--panel-2)] shadow-[0_24px_80px_rgb(0_0_0/0.6)]"
        style={{ animation: "sheet-up 320ms cubic-bezier(0.22, 1, 0.36, 1) both" }}
      >
        {/* Grabber — visual affordance only, mirrors iOS/Claude Desktop */}
        <div className="flex justify-center pt-2 pb-1">
          <span className="h-1 w-9 rounded-full bg-[var(--seam-strong)]" aria-hidden />
        </div>

        {/* Header — the question itself, given room to breathe */}
        <div className="flex items-start gap-3 px-5 pt-2 pb-4">
          <span className="lamp mt-1 flex-shrink-0" data-state="waiting" />
          <div className="min-w-0 flex-1">
            <p className="gauge mb-1.5">Talon needs your input</p>
            <p className="text-[15px] leading-snug text-[var(--label)]">{question.text}</p>
          </div>
        </div>

        {/* Suggested choices — single-select fires on click; multi-select
            toggles and defers submission to the Send button below. */}
        {question.choices.length > 0 && (
          <div className="flex flex-col gap-1 border-t border-[var(--seam)] p-2">
            {question.choices.map((choice, i) => {
              const isPicked = multi && picked.has(i);
              return (
                <button
                  key={`${choice}-${i}`}
                  type="button"
                  disabled={busy}
                  role={multi ? "checkbox" : "button"}
                  aria-checked={multi ? isPicked : undefined}
                  onClick={() => (multi ? togglePick(i) : submit(choice))}
                  className={`group flex items-center gap-3 rounded-lg px-3 py-2.5 text-left transition-colors duration-150 disabled:opacity-40 ${
                    isPicked ? "bg-[var(--panel-3)]" : "hover:bg-[var(--panel-3)]"
                  }`}
                >
                  {multi ? (
                    <span
                      className={`flex size-5 flex-shrink-0 items-center justify-center rounded-md transition-colors duration-150 ${
                        isPicked
                          ? "bg-[var(--accent)] text-[#241611]"
                          : "shadow-[inset_0_0_0_1.5px_var(--color-smoke)] group-hover:shadow-[inset_0_0_0_1.5px_var(--accent)]"
                      }`}
                    >
                      {isPicked && (
                        <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="3" strokeLinecap="round" strokeLinejoin="round" aria-hidden>
                          <path d="M20 6L9 17l-5-5" />
                        </svg>
                      )}
                    </span>
                  ) : (
                    <span className="flex size-5 flex-shrink-0 items-center justify-center rounded-full shadow-[inset_0_0_0_1.5px_var(--color-smoke)] transition-colors duration-150 group-hover:shadow-[inset_0_0_0_1.5px_var(--accent)]">
                      <span className="size-2 scale-0 rounded-full bg-[var(--accent)] transition-transform duration-150 group-hover:scale-100" />
                    </span>
                  )}
                  <span
                    className={`text-[13.5px] leading-snug transition-colors duration-150 ${
                      isPicked
                        ? "text-[var(--label)]"
                        : "text-[var(--label-2)] group-hover:text-[var(--label)]"
                    }`}
                  >
                    {choice}
                  </span>
                </button>
              );
            })}
          </div>
        )}

        {/* Freeform answer — always available so the person can override or
            supplement the suggestions with their own words. */}
        <div className="flex items-end gap-2 border-t border-[var(--seam)] p-3">
          <textarea
            autoFocus
            rows={1}
            value={draft}
            onChange={(e) => setDraft(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter" && !e.shiftKey && !e.nativeEvent.isComposing) {
                e.preventDefault();
                if (multi) submitMulti();
                else submit(draft);
              }
            }}
            placeholder={
              multi
                ? question.choices.length > 0
                  ? "Add your own answer to the selection…"
                  : "Type your answers, one per line or comma-separated…"
                : question.choices.length > 0
                  ? "Or type your own answer…"
                  : "Type your answer…"
            }
            aria-label="Custom answer"
            disabled={busy}
            className="min-h-9 flex-1 resize-none rounded-lg border border-[var(--seam)] bg-[var(--panel-3)] px-3 py-2 text-[14px] leading-snug text-[var(--label)] outline-none placeholder:text-[var(--label-4)] focus:border-[var(--accent)]"
          />
          <button
            type="button"
            disabled={busy || (multi ? picked.size === 0 && !draft.trim() : !draft.trim())}
            onClick={() => (multi ? submitMulti() : submit(draft))}
            className="btn btn-go h-9 flex-shrink-0"
            title="Send answer (⏎)"
          >
            {multi && picked.size > 0
              ? `Send (${picked.size + (draft.trim() ? 1 : 0)})`
              : "Send"}
          </button>
        </div>
      </div>
    </div>,
    document.body,
  );
}
