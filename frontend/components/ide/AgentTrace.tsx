"use client";

import { memo, useMemo, useState } from "react";
import Markdown from "@/components/ui/Markdown";
import type { TraceRow } from "@/lib/types";

// ── Verb mapping — one place for all tool labels ──

const READ_ONLY_TOOLS = new Set([
  "read_file",
  "list_files",
  "grep_codebase",
  "web_search",
  "github_search",
  "fetch_url",
  "context_build",
]);

export function getToolLabel(
  tool: string,
  input: { path?: string; name?: string; command?: string; pattern?: string } | undefined,
  filesReadThisTurn: Set<string>,
): { label: string; target: string } {
  switch (tool) {
    case "read_file":
      return { label: "Read", target: input?.path || "" };
    case "write_file": {
      const path = input?.path || "";
      // Writing a file the agent never read is a different act from editing one
      // it did, and the verb is the only place that shows up.
      return { label: filesReadThisTurn.has(path) ? "Edited" : "Created", target: path };
    }
    case "delete_file":
      return { label: "Deleted", target: input?.path || "" };
    case "list_files":
      return { label: "Listed", target: input?.path || "project" };
    case "grep_codebase":
      return { label: "Searched", target: input?.pattern || "codebase" };
    case "web_search":
      return { label: "Searched the web", target: input?.name || input?.pattern || "" };
    case "github_search":
      return { label: "Searched GitHub", target: input?.pattern || input?.name || "" };
    case "fetch_url":
      return { label: "Fetched", target: input?.path || input?.name || "" };
    case "install_package":
      return { label: "Installed", target: input?.name || "package" };
    case "run_typecheck":
      return { label: "Checked types", target: "" };
    case "run_lint":
      return { label: "Linted", target: "" };
    case "run_build":
      return { label: "Built", target: "" };
    case "run_command":
      return { label: "Ran", target: input?.command || "" };
    case "capture_screenshot":
      return { label: "Looked at the device", target: "" };
    case "read_skill":
      // The backend sends a ready-made phrase ("Consulting the wallet-mwa skill" /
      // "Applied the wallet-mwa skill") as the path; show it as the label since
      // there is no file to point at.
      return { label: input?.path || "Consulted a skill", target: "" };
    case "tail_logcat":
      return { label: "Read the device log", target: "" };
    case "context_build":
      return { label: "Scanned the project", target: "" };
    default:
      return { label: tool.replace(/_/g, " "), target: input?.path || input?.name || "" };
  }
}

export function isReadOnlyTool(tool: string): boolean {
  return READ_ONLY_TOOLS.has(tool);
}

// A run of consecutive look-at-things calls is one act, not twelve. Collapsing
// them keeps the interesting rows — writes, installs, checks — visible instead
// of buried under twenty "Read" lines.
const GROUP_THRESHOLD = 3;

function groupRows(rows: TraceRow[]): TraceRow[] {
  const out: TraceRow[] = [];
  let run: TraceRow[] = [];

  const flushRun = () => {
    if (run.length === 0) return;
    if (run.length < GROUP_THRESHOLD) {
      out.push(...run);
    } else {
      const failed = run.some((r) => r.status === "fail");
      const running = run.some((r) => r.status === "running");
      out.push({
        id: `group-${run[0].id}`,
        kind: "group",
        status: running ? "running" : failed ? "fail" : "done",
        summary: running ? "Looking through the project" : "Looked through the project",
        children: run,
      });
    }
    run = [];
  };

  for (const row of rows) {
    if (row.kind === "tool" && row.tool && isReadOnlyTool(row.tool)) {
      run.push(row);
      continue;
    }
    flushRun();
    out.push(row);
  }
  flushRun();

  return out;
}

interface AgentTraceProps {
  rows: TraceRow[];
}

function AgentTrace({ rows }: AgentTraceProps) {
  const grouped = useMemo(() => groupRows(rows), [rows]);

  if (rows.length === 0) return null;

  return (
    <div className="flex flex-col gap-px">
      {grouped.map((row) => (
        <TraceRowItem key={row.id} row={row} />
      ))}
    </div>
  );
}

export default memo(AgentTrace);

function TraceRowItem({ row }: { row: TraceRow }) {
  switch (row.kind) {
    case "narration":
      return <NarrationRow text={row.text || ""} streaming={row.streaming} />;
    case "thinking":
      return <ThinkingRow text={row.text || ""} streaming={row.streaming} />;
    case "tool":
      return <ToolRow row={row} />;
    case "group":
      return <GroupRow row={row} />;
    default:
      return null;
  }
}

// ── Narration — the agent's own voice ──

function NarrationRow({ text, streaming }: { text: string; streaming?: boolean }) {
  if (!text && !streaming) return null;
  return (
    <div className="py-1.5">
      <Markdown>{text}</Markdown>
      {streaming && (
        <span
          className="ml-0.5 inline-block h-3.5 w-[2px] translate-y-0.5 bg-probe"
          style={{ animation: "breathe 1s var(--ease) infinite" }}
        />
      )}
    </div>
  );
}

// ── Thinking — the model's working, capped so the panel never drowns ──

function ThinkingRow({ text, streaming }: { text: string; streaming?: boolean }) {
  const [expanded, setExpanded] = useState(false);
  const show = !!text || !!streaming;
  if (!show) return null;

  // Expandable only when there is something to reveal beyond the cap.
  const clampable = text.length > 160;

  return (
    <div className="flex items-start gap-2 py-1.5 pl-1">
      <span aria-hidden className="mt-1 flex-shrink-0 text-dust">
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={1.75} className="h-3 w-3">
          <path
            strokeLinecap="round"
            strokeLinejoin="round"
            d="M12 3a6 6 0 0 0-6 6c0 3 1.5 4.5 1.5 6.5v.5a2 2 0 0 0 2 2h5a2 2 0 0 0 2-2v-.5C18 13.5 18 12 18 9a6 6 0 0 0-6-6Zm-1.5 15.5a1.5 1.5 0 0 0 3 0"
          />
        </svg>
      </span>
      <div className="min-w-0 flex-1">
        <p
          aria-expanded={expanded}
          onClick={() => clampable && setExpanded((v) => !v)}
          className={`font-mono text-[0.6875rem] leading-snug text-graphite italic ${
            !expanded && clampable ? "line-clamp-2" : ""
          } ${clampable ? "cursor-pointer" : ""}`}
        >
          {text}
          {streaming && (
            <span
              className="ml-0.5 inline-block h-3 w-[2px] translate-y-0.5 bg-probe"
              style={{ animation: "breathe 1s var(--ease) infinite" }}
            />
          )}
        </p>
        {clampable && (
          <button
            type="button"
            onClick={() => setExpanded((v) => !v)}
            className="gauge mt-0.5 select-none text-tiny text-dust transition-colors hover:text-probe"
          >
            {expanded ? "hide thinking" : "show thinking"}
          </button>
        )}
      </div>
    </div>
  );
}

// ── Status glyph ──

function Glyph({ status }: { status?: TraceRow["status"] }) {
  if (status === "running") {
    return (
      <span
        aria-hidden
        className="h-2.5 w-2.5 rounded-full border-2 border-probe border-t-transparent"
        style={{ animation: "spin 0.7s linear infinite" }}
      />
    );
  }
  if (status === "fail") {
    return <span className="text-fault leading-none">✕</span>;
  }
  if (status === "done") {
    return <span className="text-signal-dim leading-none">✓</span>;
  }
  return <span className="h-1 w-1 rounded-full bg-dust" />;
}

// ── Tool row ──

function ToolRow({ row }: { row: TraceRow }) {
  const running = row.status === "running";

  return (
    <div
      className={`group flex items-baseline gap-2 rounded-1 px-1.5 py-1 ${
        running ? "sweeping" : "hover:bg-riser"
      }`}
    >
      <span className="flex h-4 w-3.5 flex-shrink-0 items-center justify-center self-start pt-0.5 text-tiny">
        <Glyph status={row.status} />
      </span>

      <span
        className={`flex-shrink-0 text-small ${running ? "text-probe" : "text-paper"}`}
      >
        {row.label}
      </span>

      {/* The path is the machine's word for the thing, so it is set in mono and
          truncated from the left — the filename matters more than the folder. */}
      {row.target && (
        <span
          dir="rtl"
          title={row.target}
          className="min-w-0 flex-1 truncate text-left font-mono text-tiny text-graphite"
        >
          <span dir="ltr">{row.target}</span>
        </span>
      )}

      {row.summary && !running && (
        <span
          title={row.summary}
          className={`ml-auto max-w-[45%] flex-shrink-0 truncate font-mono text-tiny ${
            row.status === "fail" ? "text-fault" : "text-dust"
          }`}
        >
          {row.summary}
        </span>
      )}
    </div>
  );
}

// ── Collapsed run of read-only calls ──

function GroupRow({ row }: { row: TraceRow }) {
  const [expanded, setExpanded] = useState(false);
  const count = row.children?.length ?? 0;

  return (
    <div>
      <button
        type="button"
        onClick={() => setExpanded((v) => !v)}
        aria-expanded={expanded}
        className={`flex w-full items-center gap-2 rounded-1 px-1.5 py-1 text-left ${
          row.status === "running" ? "sweeping" : "hover:bg-riser"
        }`}
      >
        <span className="flex h-4 w-3.5 flex-shrink-0 items-center justify-center text-tiny">
          <Glyph status={row.status} />
        </span>
        <span className="text-small text-graphite">{row.summary}</span>
        <span className="gauge tnum ml-auto flex-shrink-0">
          {count} {count === 1 ? "call" : "calls"}
        </span>
        <svg
          aria-hidden
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          strokeWidth={2}
          className={`h-3 w-3 flex-shrink-0 text-dust transition-transform ${
            expanded ? "rotate-90" : ""
          }`}
        >
          <path strokeLinecap="round" strokeLinejoin="round" d="M9 5l7 7-7 7" />
        </svg>
      </button>

      {expanded && row.children && (
        <div className="ml-3 border-l border-rail pl-2">
          {row.children.map((child) => (
            <TraceRowItem key={child.id} row={child} />
          ))}
        </div>
      )}
    </div>
  );
}
