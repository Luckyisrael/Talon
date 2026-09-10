"use client";

import {
  forwardRef,
  useCallback,
  useEffect,
  useImperativeHandle,
  useMemo,
  useRef,
  useState,
} from "react";
import {
  conversation,
  isResumable,
  prompts,
  rowStatus,
  streamPrompt,
  type ConversationTurn,
  type LatestPrompt,
  type StreamEvent,
} from "@/lib/api";
import { useIDEStore } from "@/lib/store";
import type { ChatMessage, PlanData, PlanTask, StreamMessage, TraceRow } from "@/lib/types";
import AgentTrace, { getToolLabel } from "./AgentTrace";
import LoadingState from "@/components/ui/LoadingState";

interface AIChatProps {
  projectId: number;
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
}

export interface AIChatHandle {
  switchToBuild: () => void;
  sendMessage: (msg: string, mode?: "plan" | "build") => void;
}

type Entry = ChatMessage | StreamMessage;

const isAssistant = (m: Entry): m is StreamMessage => "isStreaming" in m;

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
      entries.push({
        id: `restored-u${i}`,
        role: "user",
        content: text,
        timestamp: Date.parse(turn.at) || 0,
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
          features: p.features ?? [],
          navigation: p.navigation ?? "",
          packages: p.packages ?? [],
        };
      }
      if (block.type === "tool_use") {
        const input = (block.input ?? {}) as {
          path?: string;
          name?: string;
          command?: string;
          pattern?: string;
          question?: string;
          choices?: string[];
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
    };
    if (restoredPlan) {
      entry.plan = restoredPlan;
      // planMessage need only be defined for the card to render — the message
      // itself is already the narration row above, matching the live plan path.
      entry.planMessage = rows.find((r) => r.kind === "narration")?.text ?? "";
    }
    if (restoredQuestion) entry.question = restoredQuestion;
    entries.push(entry);
  });

  return entries;
}

function firstLine(s: string): string {
  const line = s.split("\n").find((l) => l.trim() !== "") ?? "";
  return line.length > 120 ? `${line.slice(0, 119)}…` : line;
}

const AIChat = forwardRef<AIChatHandle, AIChatProps>(
  ({ projectId, onFilesTouched, onPlan, onApprovePlan, onLiveTrace }, ref) => {
    const [messages, setMessages] = useState<Entry[]>([]);
    const [input, setInput] = useState("");
    const [mode, setMode] = useState<"plan" | "build">("plan");
    // Which project's history has finished loading. Derived into `restoring`
    // rather than flipped on in the effect, so switching projects reads as
    // restoring immediately instead of after an extra render pass.
    const [restoredFor, setRestoredFor] = useState<number | null>(null);
    const restoring = restoredFor !== projectId;
    const [confirmClear, setConfirmClear] = useState(false);
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
    // §7: the live turn's touched files, set as write tools fire. State, not
    // a ref: the lifted trace effect must re-run when the set changes, so the
    // workspace's Diff tab appears the moment a file changes.
    const [liveTouched, setLiveTouched] = useState<string[]>([]);
    // §7: the live turn's plan checklist — tasks whose id starts with
    // "plan:" — upserted from task_update events. Lifted so the centre
    // pane renders DSH-style pending/completed tasks.
    const [planTasks, setPlanTasks] = useState<PlanTask[]>([]);

    const scrollRef = useRef<HTMLDivElement>(null);
    const endRef = useRef<HTMLDivElement>(null);
    const textareaRef = useRef<HTMLTextAreaElement>(null);
    const abortRef = useRef<AbortController | null>(null);
    const filesReadRef = useRef<Set<string>>(new Set());
    const pinnedRef = useRef(true);

    // ── History ──
    useEffect(() => {
      let cancelled = false;
      conversation
        .get(projectId)
        .then(({ turns, latest }) => {
          if (cancelled) return;
          setMessages(turnsToEntries(turns, latest));
          // Offer resume only for a build turn whose last run did not finish.
          // A waiting turn is not resumable this way — its question card owns it.
          setResumable(isResumable(latest) ? latest : null);
        })
        .catch(() => {
          // An unreachable history is not worth an error banner over an empty
          // panel the person can still type into.
        })
        .finally(() => {
          // Marks this project restored either way: a failed fetch still ends
          // the restoring state, or the panel would spin forever.
          if (!cancelled) setRestoredFor(projectId);
        });
      return () => {
        cancelled = true;
      };
    }, [projectId]);

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

    // A turn paused on ask_user and still unanswered. While one is open the main
    // composer defers to the question card, so typing a fresh prompt cannot
    // orphan the paused turn on the server.
    const pendingQuestion = useMemo(
      () =>
        messages.some(
          (m) => isAssistant(m) && m.question !== undefined && m.question.answered === undefined,
        ),
      [messages],
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
              patch((prev) => ({
                ...prev,
                phase: d.phase,
                ...(roundMatch ? { round: parseInt(roundMatch[1], 10) } : {}),
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
                      rows[i] = { ...r, summary: merged };
                      break;
                    }
                  }
                  return { ...prev, traceRows: rows };
                });
              }
              break;

            case "task_update":
              // §7: plan: prefixed updates upsert the checklist state; all
              // task updates keep their trace row.
              if (typeof d.id === "string" && d.id.startsWith("plan:")) {
                const task: PlanTask = {
                  id: d.id,
                  label: typeof d.description === "string" ? d.description : d.id,
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
              appendRow({
                id: `${id}-k${d.id ?? Math.random().toString(36).slice(2, 8)}`,
                kind: "tool",
                tool: "task",
                label: d.description ?? "Task",
                target: "",
                status: rowStatus(d.status),
                summary: d.result ? firstLine(d.result) : undefined,
              });
              break;

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
              const plan = (d.plan ?? null) as PlanData | null;
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
              // The turn is over: no more steers can be delivered to it.
              runningPromptIdRef.current = 0;
              const touched = d.files_touched ?? [];
              const skipped = Array.isArray(d.skipped_gates) ? d.skipped_gates : [];
              patch((prev) => ({
                ...prev,
                isStreaming: false,
                phase:
                  d.status === "failed"
                    ? "FAILED_NEEDS_USER"
                    : d.status === "waiting" || d.status === "paused"
                      ? "WAITING_FOR_USER"
                      : "DONE",
                filesTouched: touched,
                verified: d.verified,
                skippedGates: skipped,
                rounds: d.rounds,
                traceRows: prev.traceRows.map((r) =>
                  r.status === "running" ? { ...r, status: "done" } : r,
                ),
              }));
              // Refetch rather than trusting the paths: the old code inserted
              // each path with empty content, so opening a file the agent had
              // just written showed a blank editor.
              if (touched.length > 0) onFilesTouched?.(touched);
              // A turn that reached its round budget ended cleanly but is not
              // done. Pull the server's now-'paused' latest status so the resume
              // banner appears at once — the same affordance a reload offers —
              // without rebuilding (and flickering) the just-streamed transcript.
              if (d.status === "paused") {
                conversation
                  .get(projectId)
                  .then(({ latest }) =>
                    setResumable(isResumable(latest) ? latest : null),
                  )
                  .catch(() => {});
              }
              break;
            }
          }
        },
      [onFilesTouched, onPlan, projectId],
    );

    // ── Sending ──

    const send = useCallback(
      async (raw: string, forcedMode?: "plan" | "build") => {
        const prompt = raw.trim();
        if (!prompt || isStreaming) return;

        setInput("");
        setConfirmClear(false);
        // A fresh prompt becomes the latest turn, so the interrupted one is no
        // longer what a reload would offer to resume. Drop the banner to match.
        setResumable(null);
        filesReadRef.current = new Set();
        pinnedRef.current = true;

        const streamId = crypto.randomUUID();
        setMessages((prev) => [
          ...prev,
          { id: crypto.randomUUID(), role: "user", content: prompt, timestamp: Date.now() },
          { id: streamId, role: "assistant", traceRows: [], isStreaming: true },
        ]);

        const patch = (fn: (prev: StreamMessage) => StreamMessage) =>
          setMessages((prev) =>
            prev.map((m) => (m.id === streamId && isAssistant(m) ? fn(m) : m)),
          );

        abortRef.current = streamPrompt(
          projectId,
          prompt,
          // Approving a plan calls switchToBuild then sendMessage in the same
          // tick; React state has not re-rendered yet, so the closure's `mode`
          // would still read "plan" and the build would re-plan forever. The
          // explicit argument makes the caller decide, not the render cycle.
          forcedMode ?? mode,
          handleEvent(streamId, patch),
          (err) => patch((prev) => ({ ...prev, isStreaming: false, error: err.message })),
        );
      },
      [handleEvent, isStreaming, mode, projectId],
    );

    // Resume the interrupted turn: no new user message, since this continues the
    // existing turn rather than starting one. The server replays the persisted
    // transcript and the agent picks up where it stopped; its new work streams
    // into a fresh assistant entry below the restored history.
    const resume = useCallback(() => {
      if (!resumable || isStreaming) return;
      const promptId = resumable.id;
      setResumable(null);
        filesReadRef.current = new Set();
        pinnedRef.current = true;

        const streamId = crypto.randomUUID();
      setMessages((prev) => [
        ...prev,
        { id: streamId, role: "assistant", traceRows: [], isStreaming: true },
      ]);

      const patch = (fn: (prev: StreamMessage) => StreamMessage) =>
        setMessages((prev) =>
          prev.map((m) => (m.id === streamId && isAssistant(m) ? fn(m) : m)),
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

        const streamId = crypto.randomUUID();
        setMessages((prev) => [
          ...prev,
          { id: streamId, role: "assistant", traceRows: [], isStreaming: true },
        ]);

        const patch = (fn: (prev: StreamMessage) => StreamMessage) =>
          setMessages((prev) =>
            prev.map((m) => (m.id === streamId && isAssistant(m) ? fn(m) : m)),
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
        setConfirmClear(false);
        setMessages((prev) => [
          ...prev,
          {
            id: crypto.randomUUID(),
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
        if (!queueInstead) void steerRunning(input);
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
                traceRows: m.traceRows.map((r) =>
                  r.status === "running" ? { ...r, status: "fail" } : r,
                ),
              }
            : m,
        ),
      );
    }

    async function clearHistory() {
      if (!confirmClear) {
        setConfirmClear(true);
        return;
      }
      setConfirmClear(false);
      try {
        await conversation.clear(projectId);
        setMessages([]);
      } catch {
        setConfirmClear(false);
      }
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

    return (
      <section className="flex h-full min-h-0 min-w-[280px] flex-col overflow-x-auto bg-void">
        <header className="bay-head justify-between">
          <div className="flex items-center gap-2 min-w-0">
            <span className="lamp" data-state={isStreaming ? "working" : "idle"} />
            <span className="gauge truncate">Agent</span>
          </div>

          <div className="flex min-w-0 flex-shrink-0 items-center gap-1">
            {messages.length > 0 && !isStreaming && (
              <button
                type="button"
                onClick={clearHistory}
                onBlur={() => setConfirmClear(false)}
                className="btn btn-ghost h-7 px-2 font-mono text-xs"
              >
                {confirmClear ? "Clear it?" : "Clear"}
              </button>
            )}
            <div
              role="group"
              aria-label="What the agent should do with your next message"
              className="flex overflow-hidden rounded-md border border-graphite"
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
                  className={`h-6 px-2.5 font-mono text-xs capitalize transition-colors ${
                    mode === m
                      ? "bg-white/10 text-paper"
                      : "text-ash hover:bg-white/5 hover:text-fog"
                  }`}
                >
                  {m}
                </button>
              ))}
            </div>
          </div>
        </header>

        <div
          ref={scrollRef}
          onScroll={onScroll}
          className="min-h-0 flex-1 overflow-y-auto px-2.5 py-3 sm:px-3"
        >
          <ChatHistory
            prompts={pastPrompts}
            onPick={(text) => {
              setInput(text);
              textareaRef.current?.focus();
            }}
            onNewChat={() => void clearHistory()}
            newChatConfirming={confirmClear}
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
                    className="rounded-md border border-graphite bg-carbon px-2.5 py-2 text-left text-xs leading-snug text-fog transition-colors hover:border-smoke hover:text-paper"
                  >
                    {example}
                  </button>
                ))}
              </div>
            </div>
          )}

          <div className="flex min-w-0 flex-col gap-4">
            {messages.map((msg) =>
              isAssistant(msg) ? (
                <AssistantEntry
                  key={msg.id}
                  msg={msg}
                  onApprove={approvePlan}
                  onAnswer={answerQuestion}
                  busy={isStreaming}
                />
              ) : (
                <div key={msg.id} className="flex min-w-0 justify-end">
                  <div
                    className={[
                      "max-w-[88%] whitespace-pre-wrap break-words rounded-xl px-3 py-1.5 text-xs leading-relaxed",
                      msg.steer
                        ? "border border-acid-lime/20 bg-acid-lime/[0.04] text-fog"
                        : "bg-white/5 text-mist",
                    ].join(" ")}
                  >
                    {msg.steer ? (
                      <span className="mr-1.5 font-mono text-[10px] uppercase tracking-wider text-acid-lime/70">
                        {msg.steerError ? "steer failed" : "steer — not a new turn"}
                      </span>
                    ) : null}
                    {msg.content}
                    {msg.steer && msg.steerError ? (
                      <div className="mt-1 text-[11px] text-sunset">{msg.steerError}</div>
                    ) : null}
                  </div>
                </div>
              ),
            )}
          </div>

          <div ref={endRef} />
        </div>

        {/* Live status — the one place that says, in the moment, what the agent
            is doing, what it is touching, and how long it has been at it. The
            pixel grid is the only moving thing. */}
        {isStreaming && (
          <div className="flex items-center gap-2.5 border-t border-graphite bg-acid-lime/[0.04] px-3 py-2">
            <LoadingState label={phaseLabel(liveMessage?.phase) || "Working"} />
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
        )}

        {/* An interrupted turn waits on a human decision: continue it from where
            it stopped, or set it aside. Neutral, not fault — the work
            is not broken, it is paused. Hidden while streaming, when the status
            strip above owns this row. */}
        {resumable && !isStreaming && (
          <div className="flex items-center gap-2.5 border-t border-graphite bg-white/[0.03] px-3 py-2">
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
        )}

        <form onSubmit={handleSubmit} className="border-t border-graphite p-2.5 sm:p-3">
          <div
            role="presentation"
            onClick={() => textareaRef.current?.focus()}
            className="flex cursor-text flex-col gap-2 rounded-md border border-graphite bg-white/[0.02] p-2.5 transition-colors duration-150 focus-within:border-smoke"
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
              className="min-h-4 bg-transparent text-xs leading-[1.4] text-mist outline-none placeholder:text-ash resize-none"
              disabled={pendingQuestion}
              aria-label="Chat prompt"
            />
            <div className="flex items-center justify-end gap-1.5">
              {isStreaming ? (
                <>
                  {/* H2: while a turn runs, submitted messages steer it by
                      default. The toggle opts the next message into the old
                      behavior — queue for the next turn — one submission at a
                      time, so the common case needs no click. */}
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
                      "flex h-7 items-center gap-1 rounded-full border px-2.5 font-mono text-[10px] uppercase tracking-wider transition-colors",
                      queueInstead
                        ? "border-acid-lime/50 text-acid-lime"
                        : "border-graphite text-fog hover:text-mist",
                    ].join(" ")}
                  >
                    {queueInstead ? "Queue instead" : "Steers the build"}
                  </button>
                  <button
                    type="button"
                    onClick={stop}
                    className="btn btn-stop h-7 flex-shrink-0"
                  >
                    Stop
                  </button>
                  <button
                    type="submit"
                    title={queueInstead ? "Queue for the next turn" : "Steer the running build"}
                    aria-label={queueInstead ? "Queue for the next turn" : "Steer the running build"}
                    disabled={!input.trim()}
                    className="flex size-7 flex-shrink-0 items-center justify-center rounded-full transition-[background-color,color,transform] duration-200 enabled:active:scale-[0.96]"
                    style={{
                      background: input.trim() ? "var(--color-acid-lime)" : "var(--color-graphite)",
                      color: input.trim() ? "var(--color-void)" : "var(--color-fog)",
                    }}
                  >
                    <svg width="14" height="14" viewBox="0 0 16 16" fill="none" aria-hidden>
                      <path
                        d="M2.5 8 13 3.5 10.8 8 13 12.5 2.5 8Z"
                        fill="currentColor"
                        stroke="currentColor"
                        strokeWidth="1.2"
                        strokeLinejoin="round"
                      />
                    </svg>
                  </button>
                </>
              ) : (
                <button
                  type="submit"
                  title={mode === "plan" ? "Ask" : "Build"}
                  aria-label={mode === "plan" ? "Ask" : "Build"}
                  disabled={!input.trim() || pendingQuestion}
                  className="flex size-7 flex-shrink-0 items-center justify-center rounded-full transition-[background-color,color,transform] duration-200 enabled:active:scale-[0.96]"
                  style={{
                    background: input.trim() && !pendingQuestion ? "var(--color-acid-lime)" : "var(--color-graphite)",
                    color: input.trim() && !pendingQuestion ? "var(--color-void)" : "var(--color-fog)",
                  }}
                >
                  <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.4" strokeLinecap="round" strokeLinejoin="round">
                    <path d="M12 19V5M5 12l7-7 7 7" />
                  </svg>
                </button>
              )}
            </div>
          </div>
          <p className="gauge mt-1.5 select-none">
            Enter sends · Shift+Enter adds a line
          </p>
        </form>
      </section>
    );
  },
);

AIChat.displayName = "AIChat";
export default AIChat;

// ── Chat history ──
// Searchable past prompts for this project, adapted from the sidebar pattern:
// the expanding search field, the recent list, the New chat action. Everything
// is real transcript data — past user prompts, most recent first. Picking one
// fills the composer; New chat reuses the existing two-step clear.
function ChatHistory({
  prompts,
  onPick,
  onNewChat,
  newChatConfirming,
}: {
  prompts: string[];
  onPick: (text: string) => void;
  onNewChat: () => void;
  newChatConfirming: boolean;
}) {
  const [open, setOpen] = useState(false);
  const [searchOpen, setSearchOpen] = useState(false);
  const [query, setQuery] = useState("");
  const searchRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    if (searchOpen) searchRef.current?.focus();
  }, [searchOpen]);

  if (prompts.length === 0) return null;
  const visible = prompts.filter((p) =>
    p.toLowerCase().includes(query.trim().toLowerCase()),
  );

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
          title="New chat (clears this project's history)"
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
        <div className="mt-1 flex h-8 items-center gap-1.5 rounded-md border border-graphite bg-white/[0.02] px-2 transition-colors focus-within:border-smoke">
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
            placeholder="Search prompts"
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
      {newChatConfirming && (
        <p className="mt-1 px-1.5 font-mono text-xs text-fog">
          Press New chat again to clear this history for good.
        </p>
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
            {visible.length === 0 && (
              <p className="px-2 py-1.5 text-xs text-ash">No prompts match.</p>
            )}
          </div>
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
}: {
  msg: StreamMessage;
  onApprove: (plan: PlanData) => void;
  onAnswer: (promptId: number, reply: string) => void;
  busy: boolean;
}) {
  const waiting = msg.isStreaming && msg.traceRows.length === 0 && !msg.error;
  // A const, not msg.question inline, so the closure below keeps the narrowing.
  const question = msg.question;

  return (
    <div className="rise flex min-w-0 flex-col gap-2">
      <AgentTrace rows={msg.traceRows} />

      {waiting && <p className="gauge py-1">Thinking</p>}

      {msg.plan && <PlanCard plan={msg.plan} onApprove={onApprove} />}

      {question && (
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
  if (msg.isStreaming || msg.restored) return null;

  const parts: string[] = [];
  if (msg.filesTouched?.length) {
    parts.push(`${msg.filesTouched.length} ${msg.filesTouched.length === 1 ? "file" : "files"}`);
  }
  if (msg.rounds) parts.push(`${msg.rounds} ${msg.rounds === 1 ? "round" : "rounds"}`);
  if (msg.usage?.input_tokens || msg.usage?.output_tokens) {
    parts.push(
      `${compactTokens(msg.usage.input_tokens ?? 0)} in · ${compactTokens(msg.usage.output_tokens ?? 0)} out`,
    );
  }
  if (parts.length === 0) return null;

  return (
    <p className="gauge tnum flex items-center gap-2">
      {msg.verified === false && <span className="text-coral-red">checks did not pass</span>}
      {msg.verified && msg.skippedGates && msg.skippedGates.length > 0 && (
        <span className="text-amber-400" title={`Did not run: ${msg.skippedGates.join(", ")}`}>
          verified (static only — {msg.skippedGates.join(", ")} could not run)
        </span>
      )}
      {parts.join("  ·  ")}
    </p>
  );
}

function PlanCard({
  plan,
  onApprove,
}: {
  plan: PlanData;
  onApprove: (plan: PlanData) => void;
}) {
  const screens = plan.screens ?? [];
  const features = plan.features ?? [];
  const packages = plan.packages ?? [];

  return (
    <div className="card !p-0 overflow-hidden">
      {screens.length > 0 && (
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
                    <span className="block font-mono text-xs text-fog">{screen.description}</span>
                  )}
                </span>
              </li>
            ))}
          </ul>
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

  return (
    <div className="card !p-0 overflow-hidden" style={{ animation: "fade-up 380ms cubic-bezier(0.23,1,0.32,1) both" }}>
      <div className="flex items-start gap-2 border-b border-graphite p-3">
        <span className="lamp mt-0.5 flex-shrink-0" data-state="waiting" />
        <div className="min-w-0">
          <p className="gauge mb-1">The agent needs an answer</p>
          <p className="text-[13px] text-paper">{question.text}</p>
        </div>
      </div>

      {question.choices.length > 0 && (
        <div className="flex flex-col gap-1 border-b border-graphite p-3">
          {question.choices.map((choice, i) => (
            <button
              key={`${choice}-${i}`}
              type="button"
              disabled={busy}
              aria-pressed="false"
              onClick={() => submit(choice)}
              className="group relative flex items-center gap-2 rounded-md px-1 py-1 text-left transition-colors duration-100 hover:bg-white/5 disabled:opacity-50"
            >
              <span className="flex size-4 shrink-0 items-center justify-center rounded-full text-transparent shadow-[inset_0_0_0_1.5px_var(--color-smoke)] transition-colors duration-200 group-hover:shadow-[inset_0_0_0_1.5px_var(--color-fog)]">
                <span className="size-1.5 rounded-full bg-acid-lime transition-transform duration-200 scale-0 group-hover:scale-50" />
              </span>
              <span className="text-xs leading-none text-fog transition-colors duration-200 group-hover:text-mist">
                {choice}
              </span>
            </button>
          ))}
          <label className="relative flex items-center gap-2 rounded-md px-1 py-1 transition-colors duration-100">
            <input
              value={draft}
              onChange={(e) => setDraft(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === "Enter" && draft.trim()) {
                  e.preventDefault();
                  submit(draft);
                }
              }}
              placeholder="Something else…"
              aria-label="Custom answer"
              className="min-w-0 flex-1 bg-transparent pl-1.5 text-xs text-mist outline-none placeholder:text-ash"
              disabled={busy}
            />
          </label>
        </div>
      )}

      <div className="flex items-center justify-between gap-3 p-3">
        <span className="font-mono text-xs tabular-nums text-ash">1 / 1</span>
        <div className="flex items-center gap-1.5">
          <button
            type="button"
            disabled={busy || !draft.trim()}
            onClick={() => submit(draft)}
            className="btn btn-go h-8 flex-shrink-0"
          >
            Send ⏎
          </button>
        </div>
      </div>
      {question.choices.length === 0 && (
        <div className="px-3 pb-3">
          <textarea
            rows={1}
            value={draft}
            onChange={(e) => setDraft(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter" && !e.shiftKey && !e.nativeEvent.isComposing) {
                e.preventDefault();
                submit(draft);
              }
            }}
            placeholder="Type your answer"
            className="field resize-none leading-snug"
            disabled={busy}
          />
        </div>
      )}
    </div>
  );
}
