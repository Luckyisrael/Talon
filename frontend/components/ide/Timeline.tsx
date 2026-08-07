"use client";

import { useEffect, useRef } from "react";

export interface TimelineEvent {
  type: "phase" | "tool_call" | "verify" | "guardrail_reject" | "error" | "done";
  phase?: string;
  tool?: string;
  path?: string;
  name?: string;
  status?: string;
  error?: string;
  attempt?: number;
}

interface TimelineProps {
  events: TimelineEvent[];
}

export default function Timeline({ events }: TimelineProps) {
  const containerRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (containerRef.current) {
      containerRef.current.scrollTop = containerRef.current.scrollHeight;
    }
  }, [events]);

  if (events.length === 0) return null;

  return (
    <div className="ml-4 space-y-1">
      <div className="text-xs font-medium text-text-muted mb-2">Activity</div>
      <div ref={containerRef} className="max-h-64 overflow-y-auto space-y-1">
        {events.map((event, i) => (
          <TimelineItem key={i} event={event} />
        ))}
      </div>
    </div>
  );
}

function TimelineItem({ event }: { event: TimelineEvent }) {
  const icon = getEventIcon(event);
  const label = getEventLabel(event);
  const statusColor = getStatusColor(event);

  return (
    <div className="flex items-center gap-2 text-xs py-0.5">
      <div className="w-3 flex justify-center">
        {event.status === "running" ? (
          <span className="w-2.5 h-2.5 border-2 border-accent border-t-transparent rounded-full animate-spin" />
        ) : event.status === "pass" || event.status === "done" || event.status === "success" ? (
          <span className="text-green-400">&#10003;</span>
        ) : event.status === "fail" || event.status === "error" || event.status === "rejected" ? (
          <span className="text-red-400">&#10007;</span>
        ) : (
          <span className={`w-2 h-2 rounded-full ${statusColor}`} />
        )}
      </div>
      <span className={`font-mono ${event.status === "running" ? "text-accent" : "text-text-muted"}`}>
        {label}
      </span>
      {event.attempt && event.attempt > 0 && (
        <span className="text-text-muted text-[10px]">
          (attempt {event.attempt})
        </span>
      )}
      {event.error && (
        <span className="text-red-400 text-[10px] truncate max-w-[200px]">
          {event.error}
        </span>
      )}
    </div>
  );
}

function getEventIcon(event: TimelineEvent): string {
  switch (event.type) {
    case "phase":
      return "●";
    case "tool_call":
      return "◆";
    case "verify":
      return "✓";
    case "guardrail_reject":
      return "⚠";
    case "error":
      return "✗";
    case "done":
      return "●";
    default:
      return "○";
  }
}

function getEventLabel(event: TimelineEvent): string {
  switch (event.type) {
    case "phase":
      return getPhaseLabel(event.phase || "", event.status || "");
    case "tool_call":
      return getToolLabel(event.tool || "", event.path || "", event.name || "");
    case "verify":
      return `${event.tool || "check"}: ${event.status || "unknown"}`;
    case "guardrail_reject":
      return `Blocked: ${event.error || "guardrail violation"}`;
    case "error":
      return `Error: ${event.error || "unknown"}`;
    case "done":
      return "Complete";
    default:
      return event.type;
  }
}

function getPhaseLabel(phase: string, status: string): string {
  const labels: Record<string, string> = {
    INTAKE: "Preparing",
    PLAN: "Planning",
    CONTEXT_BUILD: "Scanning project",
    RESEARCH: "Researching",
    GENERATE: "Generating code",
    STAGE_WRITE: "Writing files",
    INSTALL: "Installing packages",
    VERIFY_STATIC: "Running checks",
    PROMOTE: "Applying changes",
    VERIFY_RUNTIME: "Verifying runtime",
    HEAL: "Fixing errors",
    DONE: "Complete",
    FAILED_NEEDS_USER: "Failed",
  };
  return labels[phase] || phase;
}

function getToolLabel(tool: string, path: string, name: string): string {
  switch (tool) {
    case "write_file":
      return `Writing ${path || "file"}`;
    case "read_file":
      return `Reading ${path || "file"}`;
    case "list_files":
      return "Listing files";
    case "delete_file":
      return `Deleting ${path || "file"}`;
    case "grep_codebase":
      return "Searching codebase";
    case "run_command":
      return `Running ${name || "command"}`;
    case "run_typecheck":
      return "Typechecking";
    case "run_lint":
      return "Linting";
    case "install_package":
      return `Installing ${name || "package"}`;
    case "context_build":
      return "Building context";
    default:
      return tool;
  }
}

function getStatusColor(event: TimelineEvent): string {
  switch (event.status) {
    case "running":
      return "bg-accent";
    case "pass":
    case "done":
    case "success":
    case "completed":
    case "staged":
      return "bg-green-400";
    case "fail":
    case "error":
    case "rejected":
      return "bg-red-400";
    default:
      return "bg-text-muted/40";
  }
}
