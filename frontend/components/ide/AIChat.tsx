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
  streamPrompt,
  type ConversationTurn,
  type LatestPrompt,
  type StreamEvent,
} from "@/lib/api";
import { useIDEStore } from "@/lib/store";
import type { ChatMessage, PlanData, StreamMessage, TraceRow } from "@/lib/types";
import AgentTrace, { getToolLabel } from "./AgentTrace";
import Meter from "./Meter";

interface AIChatProps {
  projectId: number;
  /** Called with the paths a finished turn changed, so the tree can refetch. */
  onFilesTouched?: (paths: string[]) => void;
  /** A plan arrived — the layout puts it in the centre panel to be read. */
  onPlan?: (plan: PlanData, message: string) => void;
  /** Approving a plan is one behaviour, owned by the layout, reachable from
      both the card here and the full plan view. */
  onApprovePlan?: (plan: PlanData) => void;
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
 */
function turnsToEntries(turns: ConversationTurn[]): Entry[] {
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
        };
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

    if (rows.length === 0 && !restoredPlan) return;
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
    entries.push(entry);
  });

  return entries;
}

function firstLine(s: string): string {
  const line = s.split("\n").find((l) => l.trim() !== "") ?? "";
  return line.length > 120 ? `${line.slice(0, 119)}…` : line;
}

const AIChat = forwardRef<AIChatHandle, AIChatProps>(
  ({ projectId, onFilesTouched, onPlan, onApprovePlan }, ref) => {
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
    // When the current turn began, so the status strip can show a live timer.
    // Only read while streaming, so it needs no reset when the turn ends.
    const [streamStartedAt, setStreamStartedAt] = useState<number | null>(null);

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
          setMessages(turnsToEntries(turns));
          // Offer resume only for a build turn whose last run did not finish.
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
              const { label, target } = getToolLabel(
                tool,
                { path: d.path, name: d.name },
                filesReadRef.current,
              );
              if (tool === "read_file" && d.path) filesReadRef.current.add(d.path);

              const status =
                d.status === "done"
                  ? "done"
                  : d.status === "failed" || d.status === "error"
                    ? "fail"
                    : "running";

              // A call arrives twice — once running, once resolved. Update the
              // open row instead of stacking a second one under it.
              patch((prev) => {
                const rows = [...prev.traceRows];
                for (let i = rows.length - 1; i >= 0; i--) {
                  const r = rows[i];
                  if (r.kind === "tool" && r.tool === tool && r.status === "running") {
                    if (status === "running") break;
                    rows[i] = { ...r, status, target: target || r.target };
                    return { ...prev, traceRows: rows };
                  }
                }
                rows.push({
                  id: `${id}-t${rows.length}`,
                  kind: "tool",
                  tool,
                  label,
                  target: target || d.path || "",
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
                status:
                  d.status === "done" || d.status === "pass"
                    ? "done"
                    : d.status === "failed" || d.status === "fail"
                      ? "fail"
                      : "running",
                summary: d.error ? firstLine(d.error) : undefined,
              });
              break;

            case "command_output":
              // Live stdout/stderr from run_command, typecheck, lint, and
              // build_apk. Previously the Output pane stayed empty while the
              // agent's tools ran; now the developer watches them do it.
              if (typeof d.text === "string" && d.text.trim()) {
                useIDEStore.getState().appendBuildOutput(d.text);
              }
              break;

            case "task_update":
              appendRow({
                id: `${id}-k${d.id ?? Math.random().toString(36).slice(2, 8)}`,
                kind: "tool",
                tool: "task",
                label: d.description ?? "Task",
                target: "",
                status: d.status === "done" ? "done" : d.status === "failed" ? "fail" : "running",
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
                status: d.status === "done" ? "done" : d.status === "failed" ? "fail" : "running",
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

            case "error":
              patch((prev) => ({
                ...prev,
                error: d.message ?? "The turn failed without saying why.",
                isStreaming: false,
              }));
              break;

            case "done": {
              const touched = d.files_touched ?? [];
              patch((prev) => ({
                ...prev,
                isStreaming: false,
                phase: d.status === "failed" ? "FAILED_NEEDS_USER" : "DONE",
                filesTouched: touched,
                verified: d.verified,
                rounds: d.rounds,
                traceRows: prev.traceRows.map((r) =>
                  r.status === "running" ? { ...r, status: "done" } : r,
                ),
              }));
              // Refetch rather than trusting the paths: the old code inserted
              // each path with empty content, so opening a file the agent had
              // just written showed a blank editor.
              if (touched.length > 0) onFilesTouched?.(touched);
              break;
            }
          }
        },
      [onFilesTouched, onPlan],
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
        setStreamStartedAt(Date.now());

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
      setStreamStartedAt(Date.now());

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

    // Declared after `send` on purpose: reading it from above the declaration is
    // a temporal-dead-zone hazard, and it only worked because the handle body
    // does not run until after the first render.
    useImperativeHandle(ref, () => ({
      switchToBuild: () => setMode("build"),
      sendMessage: (msg: string, mode?: "plan" | "build") => {
        void send(msg, mode);
      },
    }));

    // Both the send button and Enter go through here. When a turn is waiting to
    // be resumed, typing just "resume" or "continue" triggers that rather than a
    // new prompt — the obvious thing to type when the banner is showing. Gated on
    // `resumable`, so the same words are an ordinary prompt at any other time.
    function submitInput() {
      if (resumable && !isStreaming && /^(resume|continue)$/i.test(input.trim())) {
        setInput("");
        resume();
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
      <section className="flex h-full min-h-0 flex-col bg-ink">
        <header className="bay-head justify-between">
          <div className="flex items-center gap-2 min-w-0">
            <span className="lamp" data-state={isStreaming ? "working" : "idle"} />
            <span className="gauge truncate">Agent</span>
          </div>

          <div className="flex items-center gap-1">
            {messages.length > 0 && !isStreaming && (
              <button
                type="button"
                onClick={clearHistory}
                onBlur={() => setConfirmClear(false)}
                className="btn btn-ghost text-tiny"
              >
                {confirmClear ? "Clear it?" : "Clear history"}
              </button>
            )}
            <div
              role="group"
              aria-label="What the agent should do with your next message"
              className="flex overflow-hidden rounded-1 border border-rail"
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
                  className={`h-6 px-2.5 text-tiny capitalize transition-colors ${
                    mode === m
                      ? "bg-riser text-paper"
                      : "text-dust hover:bg-riser hover:text-graphite"
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
          className="min-h-0 flex-1 overflow-y-auto px-3 py-3"
        >
          {restoring && messages.length === 0 && (
            <p className="gauge py-4 text-center">Loading this project&rsquo;s history</p>
          )}

          {!restoring && messages.length === 0 && (
            <div className="flex h-full flex-col justify-center px-2">
              <p className="text-body text-paper">
                {mode === "plan"
                  ? "Describe the app you want."
                  : "Say what to build next."}
              </p>
              <p className="mt-1.5 text-small text-graphite">
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
                    className="rounded-1 border border-rail bg-slate px-2.5 py-2 text-left text-small text-graphite transition-colors hover:border-edge hover:text-paper"
                  >
                    {example}
                  </button>
                ))}
              </div>
            </div>
          )}

          <div className="flex flex-col gap-4">
            {messages.map((msg) =>
              isAssistant(msg) ? (
                <AssistantEntry key={msg.id} msg={msg} onApprove={approvePlan} />
              ) : (
                <div key={msg.id} className="flex justify-end">
                  <div className="max-w-[88%] whitespace-pre-wrap rounded-2 border border-edge bg-riser px-3 py-2 text-body text-paper">
                    {msg.content}
                  </div>
                </div>
              ),
            )}
          </div>

          <div ref={endRef} />
        </div>

        {/* Live status — the one place that says, in the moment, what the agent
            is doing, what it is touching, and how long it has been at it. The
            meter is the only moving thing. */}
        {isStreaming && (
          <div className="flex items-center gap-2.5 border-t border-rail bg-probe/[0.06] px-3 py-2">
            <Meter />
            <span className="flex-shrink-0 text-small text-paper">
              {phaseLabel(liveMessage?.phase) || "Working"}
            </span>
            {liveMessage?.round && liveMessage.round > 1 ? (
              <span className="gauge flex-shrink-0">round {liveMessage.round}</span>
            ) : null}
            {liveTarget ? (
              <span
                dir="rtl"
                title={liveTarget}
                className="min-w-0 flex-1 truncate text-left font-mono text-tiny text-graphite"
              >
                <span dir="ltr">{liveTarget}</span>
              </span>
            ) : (
              <span className="flex-1" />
            )}
            <ElapsedClock startedAt={streamStartedAt} className="gauge tnum flex-shrink-0" />
          </div>
        )}

        {/* An interrupted turn waits on a human decision: continue it from where
            it stopped, or set it aside. Filament (needs-you) not fault — the work
            is not broken, it is paused. Hidden while streaming, when the status
            strip above owns this row. */}
        {resumable && !isStreaming && (
          <div className="flex items-center gap-2.5 border-t border-filament/30 bg-filament/[0.06] px-3 py-2">
            <span className="lamp flex-shrink-0" data-state="waiting" />
            <div className="min-w-0 flex-1">
              <p className="text-small text-paper">
                This turn was interrupted before it finished.
              </p>
              <p className="gauge">
                {resumable.status === "running"
                  ? "the server stopped before it finished"
                  : resumable.status === "cancelled"
                    ? "it was stopped early"
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

        <form onSubmit={handleSubmit} className="border-t border-rail p-3">
          <div className="flex items-end gap-2">
            <textarea
              ref={textareaRef}
              rows={1}
              value={input}
              onChange={(e) => setInput(e.target.value)}
              onKeyDown={handleKeyDown}
              placeholder={
                mode === "plan" ? "Describe the app…" : "What should I build?"
              }
              className="field resize-none leading-snug"
              disabled={isStreaming}
            />
            {isStreaming ? (
              <button type="button" onClick={stop} className="btn btn-stop h-8 flex-shrink-0">
                Stop
              </button>
            ) : (
              <button
                type="submit"
                disabled={!input.trim()}
                className="btn btn-go h-8 flex-shrink-0"
              >
                {mode === "plan" ? "Ask" : "Build"}
              </button>
            )}
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

// ── Elapsed time ──
// Ticks once a second, and only while mounted — the status strip unmounts it
// when the turn ends, so the interval stops with the stream.
function ElapsedClock({
  startedAt,
  className = "",
}: {
  startedAt: number | null;
  className?: string;
}) {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    setNow(Date.now());
    const id = window.setInterval(() => setNow(Date.now()), 1000);
    return () => window.clearInterval(id);
  }, [startedAt]);

  if (!startedAt) return null;
  const secs = Math.max(0, Math.floor((now - startedAt) / 1000));
  const m = Math.floor(secs / 60);
  const s = secs % 60;
  return (
    <span className={className}>
      {m}:{String(s).padStart(2, "0")}
    </span>
  );
}

// ── One assistant turn ──

function AssistantEntry({
  msg,
  onApprove,
}: {
  msg: StreamMessage;
  onApprove: (plan: PlanData) => void;
}) {
  const waiting = msg.isStreaming && msg.traceRows.length === 0 && !msg.error;

  return (
    <div className="rise flex flex-col gap-2">
      <AgentTrace rows={msg.traceRows} />

      {waiting && <p className="gauge py-1">Thinking</p>}

      {msg.planMessage !== undefined && (
        <PlanCard plan={msg.plan ?? { screens: [], features: [], navigation: "", packages: [] }} onApprove={onApprove} />
      )}

      {msg.error && (
        <p className="rounded-2 border border-fault/35 bg-fault/10 px-3 py-2 text-small text-fault">
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
      {msg.verified === false && <span className="text-filament">checks did not pass</span>}
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
    <div className="bay overflow-hidden">
      {screens.length > 0 && (
        <div className="border-b border-rail p-3">
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
                  <span className="block text-small text-paper">{screen.name}</span>
                  {screen.description && (
                    <span className="block text-tiny text-graphite">{screen.description}</span>
                  )}
                </span>
              </li>
            ))}
          </ul>
        </div>
      )}

      {features.length > 0 && (
        <div className="border-b border-rail p-3">
          <p className="gauge mb-2">Features</p>
          <div className="flex flex-wrap gap-1">
            {features.map((f, i) => (
              <span
                key={`${f}-${i}`}
                className="rounded-1 border border-rail bg-riser px-1.5 py-0.5 text-tiny text-graphite"
              >
                {f}
              </span>
            ))}
          </div>
        </div>
      )}

      {plan.navigation && (
        <div className="border-b border-rail p-3">
          <p className="gauge mb-1">Navigation</p>
          <p className="text-small text-paper">{plan.navigation}</p>
        </div>
      )}

      {packages.length > 0 && (
        <div className="border-b border-rail p-3">
          <p className="gauge mb-2">Packages to install</p>
          <div className="flex flex-wrap gap-x-3 gap-y-1 font-mono text-tiny text-graphite">
            {packages.map((p, i) => (
              <span key={`${p}-${i}`}>{p}</span>
            ))}
          </div>
        </div>
      )}

      <div className="flex items-center gap-2 p-3">
        <button type="button" onClick={() => onApprove(plan)} className="btn btn-go">
          Build this
        </button>
        <span className="gauge">or keep refining it above</span>
      </div>
    </div>
  );
}
