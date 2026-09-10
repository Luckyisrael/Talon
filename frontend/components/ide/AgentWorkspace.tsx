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
import { PlanProgress } from "./AgentTrace";
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
  // transcript again is one click.
  useEffect(() => {
    if (selectedFile) setTab("file");
  }, [selectedFile]);

  const hasDiff = filesTouched.length > 0;

  return (
    <div className="flex h-full min-h-0 flex-col overflow-hidden rounded-xl border border-graphite bg-void">
      {/* Tab rail */}
      <div className="flex flex-shrink-0 items-center gap-1 border-b border-graphite px-2 py-1.5">
        <button
          type="button"
          onClick={() => setTab("transcript")}
          className={`btn btn-sm ${tab === "transcript" ? "btn-primary" : "btn-ghost"}`}
        >
          Agent
        </button>
        {selectedFile ? (
          <button
            type="button"
            onClick={() => setTab("file")}
            className={`btn btn-sm ${tab === "file" ? "btn-primary" : "btn-ghost"}`}
          >
            {selectedFile.split("/").pop()}
          </button>
        ) : null}
        {hasDiff ? (
          <button
            type="button"
            onClick={() => setTab("diff")}
            className={`btn btn-sm ${tab === "diff" ? "btn-primary" : "btn-ghost"}`}
          >
            Diff
            <span className="gauge tnum">({filesTouched.length})</span>
          </button>
        ) : null}
      </div>

      {/* Surfaces */}
      <div className="min-h-0 flex-1 overflow-hidden">
        {tab === "transcript" ? (
          <div className="flex h-full min-h-0 flex-col">
            {planTasks && planTasks.length > 0 ? (
              <div className="flex-shrink-0 border-b border-graphite px-4 py-2">
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
          <CodeEditor projectId={projectId} filePath={selectedFile} content={fileContent ?? ""} />
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
        {rows.map((row) => (
          <TranscriptRow key={row.id} row={row} />
        ))}
      </div>
    </div>
  );
}

/** TranscriptRow renders one trace row at full fidelity. */
function TranscriptRow({ row }: { row: TraceRow }) {
  switch (row.kind) {
    case "thinking":
      return (
        <div className="rounded-lg border border-graphite/60 bg-ink/40 px-3 py-2">
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
    case "narration":
      return (
        <div className="px-1 py-0.5 text-sm leading-relaxed text-paper">
          {row.text}
        </div>
      );
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
    <div className="overflow-hidden rounded-lg border border-graphite bg-ink/30">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        className="flex w-full items-center gap-2 px-3 py-2 text-left hover:bg-ink/50"
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
  const [err, setErr] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    setErr(null);
    conversation
      .diff(projectId)
      .then((res) => {
        if (!cancelled) setDiff(res?.diff ?? "");
      })
      .catch(() => {
        if (!cancelled) setErr("The diff could not be loaded.");
      });
    return () => {
      cancelled = true;
    };
  }, [projectId, files.length]);

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
        {diff.split("\n").map((line, i) => (
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
      </pre>
    </div>
  );
}

export default AgentWorkspace;
