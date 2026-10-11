"use client";

/**
 * AgentWorkspace — the §7 center pane: the DSH-style agent surface that
 * replaces the code editor as the project's centerpiece.
 *
 * Tabs:
 *  - Transcript: full-fidelity render of the running/latest turn —
 *    thinking blocks, tool calls with expandable inputs/outputs, bash
 *    commands, screenshots — the same event stream AIChat consumes, at
 *    full width.
 *  - File: a read-only view of the selected file (the editor is gone; the
 *    agent writes, the person reads).
 *  - Diff: the turn's git diff, when one exists.
 *
 * The empty state is the agent itself: the person describes what they want
 * and watches it get built — "Nothing open" becomes "The agent is your
 * editor."
 */
import { useEffect, useMemo, useRef, useState } from "react";
import Markdown from "@/components/ui/Markdown";
import CodeEditor from "./CodeEditor";
import { ContextRow, EndRow, PlanProgress } from "./AgentTrace";
import type { PlanTask, TraceRow } from "@/lib/types";
import { conversation } from "@/lib/api";

type Tab = "transcript" | "file" | "diff";

interface AgentWorkspaceProps {
  projectId: number;
  traceRows: TraceRow[];
  /** The live turn's plan checklist — DSH-style pending/completed tasks. */
  planTasks?: PlanTask[];
  selectedFile: string | null;
  fileContent?: string;
  /** filesTouched names the last turn's written files, for the diff fetch. */
  filesTouched: string[];
}

function AgentWorkspace({
  projectId,
  traceRows,
  planTasks,
  selectedFile,
  fileContent,
  filesTouched,
}: AgentWorkspaceProps) {
  const [tab, setTab] = useState<Tab>("transcript");
  // The transcript auto-follows the stream until the person scrolls up.
  const [follow, setFollow] = useState(true);
  const scrollRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (follow && scrollRef.current) {
      scrollRef.current.scrollTop = scrollRef.current.scrollHeight;
    }
  }, [traceRows, follow]);

  // Selecting a file from the tree switches to its view; opening the
  // transcript again is one click. Adjusted during render with a previous-
  // value guard (React's "store info from previous renders" pattern): the
  // tab lands on "file" in the same commit as the selection, with no
  // post-commit cascade from an effect. Null is the mount value — a file
  // already selected on first paint flips the tab, exactly as the old
  // mount-time effect did.
  const [prevSelected, setPrevSelected] = useState<string | null>(null);
  if (selectedFile !== prevSelected) {
    setPrevSelected(selectedFile);
    if (selectedFile) setTab("file");
  }

  const hasDiff = filesTouched.length > 0;

  return (
    <div className="flex h-full min-h-0 flex-col overflow-hidden">
      {/* Tab rail — an inset pill group on a tone step. The selected tab reads
          as raised, not as a border swap; the Diff tab's count is the only
          signal a diff exists. */}
      <div className="surface-1 flex flex-shrink-0 items-center gap-1 px-2 py-1.5 shadow-[0_1px_0_var(--seam)]">
        <div className="surface-2 flex gap-0.5 rounded-lg p-0.5">
          <button
            type="button"
            onClick={() => setTab("transcript")}
            aria-pressed={tab === "transcript"}
            className={`h-7 rounded-[7px] px-3 text-xs font-medium transition-colors duration-150 ease-out ${
              tab === "transcript" ? "surface-3 text-paper" : "text-ash hover:text-mist"
            }`}
          >
            Agent
          </button>
          {selectedFile ? (
            <button
              type="button"
              onClick={() => setTab("file")}
              aria-pressed={tab === "file"}
              className={`flex h-7 max-w-[180px] items-center gap-1.5 rounded-[7px] px-3 font-mono text-xs transition-colors duration-150 ease-out ${
                tab === "file" ? "surface-3 text-paper" : "text-ash hover:text-mist"
              }`}
            >
              <span className="truncate">{selectedFile.split("/").pop()}</span>
            </button>
          ) : null}
          {hasDiff ? (
            <button
              type="button"
              onClick={() => setTab("diff")}
              aria-pressed={tab === "diff"}
              className={`flex h-7 items-center gap-1.5 rounded-[7px] px-3 text-xs font-medium transition-colors duration-150 ease-out ${
                tab === "diff" ? "surface-3 text-paper" : "text-ash hover:text-mist"
              }`}
            >
              Diff
              <span className="gauge tnum">{filesTouched.length}</span>
            </button>
          ) : null}
        </div>
      </div>

      {/* Surfaces */}
      <div className="min-h-0 flex-1 overflow-hidden">
        {tab === "transcript" ? (
          <div className="flex h-full min-h-0 flex-col">
            {planTasks && planTasks.length > 0 ? (
              <div className="surface-1 flex-shrink-0 px-4 py-2 shadow-[0_1px_0_var(--seam)]">
                <div className="mx-auto max-w-3xl">
                  <PlanProgress tasks={planTasks} />
                </div>
              </div>
            ) : null}
            <div className="min-h-0 flex-1">
              <TranscriptView rows={traceRows} scrollRef={scrollRef} follow={follow} onFollow={setFollow} />
            </div>
          </div>
        ) : tab === "file" && selectedFile ? (
          // §7: markdown files render like DSH — the plan/tasks are read,
          // not edited; source files stay in the code view. The editor is
          // gone from the centerpiece; reading stays first-class.
          selectedFile.endsWith(".md") ? (
            <div className="h-full overflow-y-auto px-6 py-6">
              <div className="mx-auto max-w-2xl">
                <Markdown>{fileContent ?? ""}</Markdown>
              </div>
            </div>
          ) : (
            <CodeEditor projectId={projectId} filePath={selectedFile} content={fileContent ?? ""} />
          )
        ) : tab === "diff" ? (
          <DiffView projectId={projectId} files={filesTouched} />
        ) : null}
      </div>
    </div>
  );
}

/** TranscriptView renders the trace rows full-width, DSH-style. */
function TranscriptView({
  rows,
  scrollRef,
  follow,
  onFollow,
}: {
  rows: TraceRow[];
  scrollRef: React.RefObject<HTMLDivElement | null>;
  follow: boolean;
  onFollow: (v: boolean) => void;
}) {
  if (rows.length === 0) {
    return (
      <div className="flex h-full flex-col items-center justify-center px-8 text-center">
        <p className="text-[15px] font-medium tracking-tight text-paper">
          The agent is your editor.
        </p>
        <p className="mt-1.5 max-w-sm text-sm text-fog">
          Describe what you want in the chat and watch the build happen here —
          every thought, every tool call, every command, live.
        </p>
      </div>
    );
  }
  return (
    <div
      ref={scrollRef}
      onScroll={(e) => {
        const el = e.currentTarget;
        // Follow while the person is at (or near) the bottom.
        onFollow(el.scrollHeight - el.scrollTop - el.clientHeight < 48);
      }}
      className="h-full overflow-y-auto px-4 py-3"
    >
      <div className="mx-auto max-w-3xl space-y-2">
        <RoundBlocks rows={rows} />
      </div>
    </div>
  );
}

/** RoundBlocks partitions trace rows at group markers (Round N) into
 *  collapsible round blocks — the confirmed §7 AgentTrace UX: intent line,
 *  tool chips (green/red), one-line outcome, round number visible. Rows
 *  before the first marker form the opening block (no header). */
function RoundBlocks({ rows }: { rows: TraceRow[] }) {
  const blocks: { label: string | null; rows: TraceRow[] }[] = [];
  let current: { label: string | null; rows: TraceRow[] } = {
    label: null,
    rows: [],
  };
  for (const row of rows) {
    if (row.kind === "group") {
      if (current.rows.length > 0 || current.label !== null) {
        blocks.push(current);
      }
      current = { label: row.label ?? "Round", rows: [] };
    } else {
      current.rows.push(row);
    }
  }
  if (current.rows.length > 0 || current.label !== null) {
    blocks.push(current);
  }
  return (
    <>
      {blocks.map((b, i) =>
        b.label === null ? (
          // The opening block: no round header, straight rows.
          <div key={`open-${i}`} className="space-y-2">
            {b.rows.map((r) => (
              <TranscriptRow key={r.id} row={r} />
            ))}
          </div>
        ) : (
          <RoundBlock key={b.label} label={b.label} rows={b.rows} />
        ),
      )}
    </>
  );
}

/** RoundBlock: one agent round as a collapsible row. Header: round number,
 *  intent (first narration), tool chips colored by result, one-line outcome
 *  (the last tool's status or the last narration). Body: the full rows. */
function RoundBlock({ label, rows }: { label: string; rows: TraceRow[] }) {
  const [open, setOpen] = useState(true);
  const tools = rows.filter((r) => r.kind === "tool");
  const intent =
    rows.find((r) => r.kind === "narration" && (r.text ?? "").trim())?.text ??
    "";
  const failed = tools.some((t) => t.status === "fail");
  const running = tools.some((t) => t.status === "running");
  const lastTool = tools[tools.length - 1];
  const outcome = failed
    ? "a call failed"
    : running
      ? "running…"
      : lastTool
        ? `${tools.length} call${tools.length === 1 ? "" : "s"}`
        : "no tool calls";

  return (
    <div className="surface-2 overflow-hidden rounded-lg">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        className="row-hover flex w-full items-center gap-2 px-3 py-2 text-left"
      >
        <span className="gauge flex-shrink-0 text-cyan">{label}</span>
        {intent ? (
          <span className="truncate text-xs text-paper">{firstWords(intent, 12)}</span>
        ) : null}
        <span className="ml-auto flex flex-shrink-0 items-center gap-1.5">
          {tools.slice(-8).map((t) => (
            <span
              key={t.id}
              title={`${t.tool}${t.target ? ` ${t.target}` : ""}`}
              className={`inline-block h-1.5 w-4 rounded-full ${
                t.status === "fail"
                  ? "bg-rose"
                  : t.status === "running"
                    ? "animate-pulse bg-acid-lime/60"
                    : t.status === "warning"
                      ? "bg-amber"
                      : "bg-acid-lime"
              }`}
            />
          ))}
          <span className="gauge tnum text-fog">{outcome}</span>
          <span className="gauge text-fog">{open ? "−" : "+"}</span>
        </span>
      </button>
      {open ? (
        <div className="space-y-2 border-t border-graphite/50 px-3 py-2">
          {rows.map((r) => (
            <TranscriptRow key={r.id} row={r} />
          ))}
        </div>
      ) : null}
    </div>
  );
}

/** firstWords bounds a line for the round header. */
function firstWords(s: string, n: number): string {
  const parts = s.split(/\s+/);
  return parts.length <= n ? s : `${parts.slice(0, n).join(" ")}…`;
}

/** TranscriptRow renders one trace row at full fidelity. */
function TranscriptRow({ row }: { row: TraceRow }) {
  switch (row.kind) {
    case "thinking":
      return (
        <div className="surface-2 rounded-lg px-3 py-2">
          <div className="gauge text-fog">thinking</div>
          <p className="mt-1 font-mono text-xs leading-relaxed text-mist">
            {row.text}
            {row.streaming ? <span className="animate-pulse">▍</span> : null}
          </p>
        </div>
      );
    case "tool":
      return <ToolRow row={row} />;
    case "decision":
      return <DecisionRow row={row} />;
    case "group":
      // Round markers render as dividers; the blocks form in TranscriptView.
      return (
        <div className="flex items-center gap-2 pt-2">
          <span className="gauge text-cyan">{row.label}</span>
          <span className="h-px flex-1 bg-graphite" />
        </div>
      );
    case "narration":
      return (
        <div className="px-1 py-0.5 text-sm leading-relaxed text-paper">
          {row.text}
        </div>
      );
    case "end":
      // Same law as the chat trace (row 45): only non-clean endings render —
      // a completed turn is ended by its own narration. Without this case the
      // workspace dropped the row to null and a failed/stalled tail vanished.
      return row.ending && row.ending !== "completed" ? (
        <EndRow ending={row.ending} error={row.text} rounds={row.rounds} />
      ) : null;
    case "context":
      return row.title ? (
        <ContextRow title={row.title} body={row.body} kind={row.ctxKind} />
      ) : null;
    default:
      return null;
  }
}

const toolStatusRing: Record<string, string> = {
  running: "border-acid-lime/60",
  done: "border-acid-lime",
  fail: "border-rose",
  warning: "border-amber",
};

/** ToolRow: the chip with status color + expandable detail. */
function ToolRow({ row }: { row: TraceRow }) {
  const [open, setOpen] = useState(false);
  const status = row.status ?? "done";
  return (
    <div className="surface-2 overflow-hidden rounded-lg">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        className="row-hover flex w-full items-center gap-2 px-3 py-2 text-left"
      >
        <span
          className={`inline-block h-2 w-2 flex-shrink-0 rounded-full border ${
            toolStatusRing[status] ?? "border-graphite"
          } ${status === "running" ? "animate-pulse bg-acid-lime/60" : ""}`}
        />
        <span className="font-mono text-xs text-acid-lime">{row.tool}</span>
        {row.label ? (
          <span className="truncate text-xs text-paper">{row.label}</span>
        ) : null}
        {row.target ? (
          <span className="truncate font-mono text-[11px] text-fog">{row.target}</span>
        ) : null}
        <span className="gauge ml-auto flex-shrink-0 tnum text-fog">
          {open ? "−" : "+"}
        </span>
      </button>
      {open && (row.summary || row.text) ? (
        <div className="border-t border-graphite/60 px-3 py-2">
          {row.summary ? (
            <p className="whitespace-pre-wrap font-mono text-xs leading-relaxed text-mist">
              {row.summary}
            </p>
          ) : null}
          {row.text ? (
            <div className="prose prose-invert prose-sm max-w-none text-mist">
              <Markdown>{row.text}</Markdown>
            </div>
          ) : null}
        </div>
      ) : null}
    </div>
  );
}

/** DecisionRow: ADR / memory cards, rendered as markdown. */
function DecisionRow({ row }: { row: TraceRow }) {
  if (!row.decision) return null;
  return (
    <div className="rounded-lg border border-cyan/40 bg-cyan/5 px-3 py-2">
      <div className="gauge text-cyan">{row.decision.category ?? "decision"}</div>
      <div className="prose prose-invert prose-sm mt-1 max-w-none text-mist">
        <Markdown>{row.decision.content ?? ""}</Markdown>
      </div>
    </div>
  );
}

/** DiffView fetches and renders the turn's diff, file by file. */
function DiffView({ projectId, files }: { projectId: number; files: string[] }) {
  const [diff, setDiff] = useState<string>("");
  // The error is keyed to the fetch it belongs to: a projectId/files change
  // hides the old error the moment the key changes — derived here instead of
  // cleared by a synchronous setState at the top of the effect.
  const fetchKey = `${projectId}:${files.length}`;
  const [errAt, setErrAt] = useState<{ key: string; msg: string } | null>(null);
  const err = errAt && errAt.key === fetchKey ? errAt.msg : null;

  useEffect(() => {
    let cancelled = false;
    conversation
      .diff(projectId)
      .then((res) => {
        if (!cancelled) setDiff(res?.diff ?? "");
      })
      .catch(() => {
        if (!cancelled) setErrAt({ key: fetchKey, msg: "The diff could not be loaded." });
      });
    return () => {
      cancelled = true;
    };
  }, [projectId, files.length, fetchKey]);

  // Render cap: a turn that touches many files (or a generated bundle) can
  // produce a diff of tens of thousands of lines, and one span per line froze
  // the whole tab while layout ran. Show the head in full and summarise the
  // rest — the user needs shape and verdict, not every hunk. Hooks run before
  // the early returns below.
  const { visible, hiddenLines, hiddenChars } = useMemo(() => {
    const MAX_LINES = 3000;
    const MAX_LINE_CHARS = 400;
    const lines = diff.split("\n");
    const head = lines.slice(0, MAX_LINES);
    const shown = head.map((line) =>
      line.length > MAX_LINE_CHARS
        ? `${line.slice(0, MAX_LINE_CHARS)}… [line truncated]`
        : line,
    );
    // Characters the cap removed: over-long lines trimmed here, plus every
    // line beyond the window. A diff that is few lines but enormous
    // (minified files) lands in the first term, so it reports too.
    const trimmed = head.reduce(
      (n, line) => n + Math.max(0, line.length - MAX_LINE_CHARS),
      0,
    );
    const beyondWindow = lines.slice(head.length).reduce((n, l) => n + l.length, 0);
    return {
      visible: shown,
      hiddenLines: lines.length - head.length,
      hiddenChars: trimmed + beyondWindow,
    };
  }, [diff]);

  if (err) {
    return <div className="p-6 text-sm text-fog">{err}</div>;
  }
  if (!diff) {
    return (
      <div className="p-6 text-sm text-fog">
        {files.length > 0
          ? "Reading the workspace changes…"
          : "No changes this turn."}
      </div>
    );
  }
  return (
    <div className="h-full overflow-y-auto p-4">
      <pre className="mx-auto max-w-4xl whitespace-pre-wrap rounded-lg border border-graphite bg-ink/30 p-4 font-mono text-xs leading-relaxed">
        {visible.map((line, i) => (
          <span
            key={i}
            className={
              line.startsWith("+") && !line.startsWith("+++")
                ? "text-acid-lime"
                : line.startsWith("-") && !line.startsWith("---")
                  ? "text-rose"
                  : line.startsWith("@@")
                    ? "text-cyan"
                    : "text-mist"
            }
          >
            {line}
            {"\n"}
          </span>
        ))}
        {hiddenLines > 0 || hiddenChars > 0 ? (
          <span className="text-fog">
            … {hiddenLines > 0 ? `${hiddenLines} more lines` : ""}
            {hiddenLines > 0 && hiddenChars > 0 ? " " : ""}
            {hiddenChars > 0 ? `(${hiddenChars.toLocaleString()} characters)` : ""}{" "}
            truncated — the full diff is on disk via git.
          </span>
        ) : null}
      </pre>
    </div>
  );
}

export default AgentWorkspace;
