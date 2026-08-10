"use client";

import { useState, useRef, useEffect } from "react";
import type { TraceRow } from "@/lib/types";

// ── Verb Mapping (§11.4) — one place for all tool labels ──

const READ_ONLY_TOOLS = new Set(["read_file", "list_files", "grep_codebase", "web_search", "github_search"]);
const MUTATING_TOOLS = new Set(["write_file", "install_package", "run_build", "run_typecheck", "run_lint", "run_command", "context_build"]);

export function getToolLabel(
  tool: string,
  input: { path?: string; name?: string; command?: string; pattern?: string } | undefined,
  filesReadThisTurn: Set<string>,
): { label: string; target: string } {
  switch (tool) {
    case "read_file":
      return { label: "Reading", target: input?.path || "" };
    case "write_file": {
      const path = input?.path || "";
      const label = filesReadThisTurn.has(path) ? "Updating" : "Creating";
      return { label, target: path };
    }
    case "delete_file":
      return { label: "Deleting", target: input?.path || "" };
    case "list_files":
      return { label: "Listing", target: input?.path || "files" };
    case "grep_codebase":
      return { label: "Searching", target: input?.pattern || "codebase" };
    case "install_package":
      return { label: "Installing", target: input?.name || "package" };
    case "run_typecheck":
      return { label: "Checking types", target: "" };
    case "run_lint":
      return { label: "Linting", target: "" };
    case "run_build":
      return { label: "Building", target: "" };
    case "run_command":
      return { label: "Shell", target: input?.command || "" };
    case "context_build":
      return { label: "Scanning project", target: "" };
    default:
      return { label: tool, target: "" };
  }
}

export function isReadOnlyTool(tool: string): boolean {
  return READ_ONLY_TOOLS.has(tool);
}

// ── Component ──

interface AgentTraceProps {
  rows: TraceRow[];
}

export default function AgentTrace({ rows }: AgentTraceProps) {
  const containerRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (containerRef.current) {
      containerRef.current.scrollTop = containerRef.current.scrollHeight;
    }
  }, [rows]);

  if (rows.length === 0) return null;

  return (
    <div ref={containerRef} className="space-y-0.5">
      {rows.map((row) => (
        <TraceRowItem key={row.id} row={row} />
      ))}
    </div>
  );
}

// ── Row Renderer ──

function TraceRowItem({ row }: { row: TraceRow }) {
  switch (row.kind) {
    case "narration":
      return <NarrationRow text={row.text || ""} streaming={row.streaming} />;
    case "tool":
      return <ToolRow row={row} />;
    case "group":
      return <GroupRow row={row} />;
    default:
      return null;
  }
}

// ── Narration ──

function NarrationRow({ text, streaming }: { text: string; streaming?: boolean }) {
  return (
    <div className="py-1.5 text-sm text-text-secondary leading-relaxed">
      {text}
      {streaming && <span className="inline-block w-1.5 h-4 bg-accent ml-0.5 animate-pulse" />}
    </div>
  );
}

// ── Tool Row ──

function ToolRow({ row }: { row: TraceRow }) {
  return (
    <div className="flex items-center gap-2 py-1 px-2 rounded-lg hover:bg-surface-2/50 transition-colors group">
      {/* Status icon */}
      <div className="w-4 flex justify-center flex-shrink-0">
        {row.status === "running" ? (
          <span className="w-3 h-3 border-2 border-accent border-t-transparent rounded-full animate-spin" />
        ) : row.status === "done" ? (
          <span className="text-success text-xs">&#10003;</span>
        ) : row.status === "fail" ? (
          <span className="text-error text-xs">&#10007;</span>
        ) : (
          <span className="w-3 h-3" />
        )}
      </div>

      {/* Label */}
      <span className={`text-xs font-semibold ${row.status === "running" ? "text-accent" : "text-text-primary"}`}>
        {row.label}
      </span>

      {/* Target */}
      {row.target && (
        <span className="text-xs font-mono text-text-muted truncate max-w-[200px]">
          {row.kind === "tool" && row.tool === "run_command" ? (
            <code className="bg-surface-3 px-1.5 py-0.5 rounded text-[11px]">{row.target}</code>
          ) : (
            `\`{row.target}\``
          )}
        </span>
      )}

      {/* Summary on completion */}
      {row.summary && row.status !== "running" && (
        <span className="text-[11px] text-text-muted ml-auto truncate max-w-[150px]">
          {row.summary}
        </span>
      )}
    </div>
  );
}

// ── Group Row (collapsed read-only calls) ──

function GroupRow({ row }: { row: TraceRow }) {
  const [expanded, setExpanded] = useState(false);
  const childCount = row.children?.length || 0;

  return (
    <div className="rounded-lg">
      <button
        onClick={() => setExpanded(!expanded)}
        className="w-full flex items-center gap-2 py-1 px-2 hover:bg-surface-2/50 transition-colors text-left"
      >
        {/* Chevron */}
        <svg
          className={`w-3 h-3 text-text-muted transition-transform ${expanded ? "rotate-90" : ""}`}
          fill="none"
          viewBox="0 0 24 24"
          stroke="currentColor"
          strokeWidth={2}
        >
          <path strokeLinecap="round" strokeLinejoin="round" d="M9 5l7 7-7 7" />
        </svg>

        {/* Status dot */}
        <div className="w-4 flex justify-center flex-shrink-0">
          {row.status === "running" ? (
            <span className="w-2.5 h-2.5 border-2 border-accent border-t-transparent rounded-full animate-spin" />
          ) : row.status === "done" ? (
            <span className="text-success text-xs">&#10003;</span>
          ) : (
            <span className="w-2.5 h-2.5 rounded-full bg-text-muted/40" />
          )}
        </div>

        {/* Summary */}
        <span className="text-xs font-medium text-text-secondary">
          {row.summary || `Explored ${childCount} calls`}
        </span>

        {/* Detail count */}
        <span className="text-[10px] text-text-muted">
          {childCount} {childCount === 1 ? "call" : "calls"}
        </span>
      </button>

      {/* Expanded children */}
      {expanded && row.children && (
        <div className="ml-6 space-y-0.5">
          {row.children.map((child) => (
            <TraceRowItem key={child.id} row={child} />
          ))}
        </div>
      )}
    </div>
  );
}
