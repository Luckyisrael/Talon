"use client";

import { memo, useEffect, useMemo, useState } from "react";
import Markdown from "@/components/ui/Markdown";
import type { DecisionCard, PlanTask, TraceRow } from "@/lib/types";
import Meter from "./Meter";

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

/**
 * progressHint distills streaming command output to one readable line for
 * the running tool row: the latest gradle `> Task :x` line wins, then a
 * trailing percentage, else the last non-empty line. Capped so a verbose
 * build never stretches the row.
 */
export function progressHint(text: string): string | undefined {
  const lines = text.split("\n").map((l) => l.trim()).filter(Boolean);
  if (lines.length === 0) return undefined;
  for (let i = lines.length - 1; i >= 0; i--) {
    const m = lines[i].match(/^>\s*Task\s+(.+)$/);
    if (m) return m[1].slice(0, 60);
  }
  for (let i = lines.length - 1; i >= 0; i--) {
    const m = lines[i].match(/(\d{1,3})%/);
    if (m) return `${m[1]}% — ${lines[i].slice(0, 48)}`;
  }
  return lines[lines.length - 1].slice(0, 60);
}

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
    case "edit_file":
    case "multi_edit":
      return { label: "Edited", target: input?.path || "" };
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
    case "read_skill": {
      // The live stream hands us a ready phrase in `name` ("Consulting the
      // wallet-mwa skill"); a restored turn has only the bare skill name in its
      // stored input. Show the phrase as-is, or build one from the bare name —
      // there is no file to point at, so the label carries everything.
      const n = input?.name || input?.path || "";
      const label = !n ? "Consulted a skill" : /\s/.test(n) ? n : `Consulted the ${n} skill`;
      return { label, target: "" };
    }
    case "compact":
      // A context compaction is not a real tool; its whole description is the
      // phrase the backend sends in `name`.
      return { label: input?.name || "Compacted context", target: "" };
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
        // Explicit always — an undefined status renders as a blank glyph.
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
    <div className="flex min-w-0 flex-col gap-px">
      {grouped.map((row) => (
        <TraceRowItem key={row.id} row={row} />
      ))}
    </div>
  );
}

export default memo(AgentTrace);

// ── Plan progress — the settled plan, checked off live ──
//
// Fed by the `plan:`-prefixed task_update stream. It shows the whole plan up
// front (screens and features seeded at build start) and flips each item as the
// agent finishes it, so the developer watches the build close on the plan rather
// than guessing how far along it is. It states only what the backend reported:
// an item is checked when — and only when — a compiling file matched it or the
// model marked it done, so the count never overstates progress.
export const PlanProgress = memo(function PlanProgress({ tasks }: { tasks?: PlanTask[] }) {
  if (!tasks || tasks.length === 0) return null;
  const done = tasks.filter((t) => t.status === "completed").length;

  return (
    <div className="my-1.5 rounded-md border border-graphite bg-white/[0.02] px-2 py-1.5">
      <div className="mb-1 flex items-baseline justify-between gap-2">
        <span className="font-mono text-xs uppercase tracking-wide text-fog">Build plan</span>
        <span className="font-mono text-xs text-ash">
          {done}/{tasks.length} done
        </span>
      </div>
      <div className="flex flex-col gap-0.5">
        {tasks.map((t, i) => (
          <PlanProgressItem key={t.id} task={t} index={i} />
        ))}
      </div>
    </div>
  );
});

function SpinnerRing({ children }: { children?: React.ReactNode }) {
  const size = 24,
    stroke = 2;
  const r = (size - stroke) / 2;
  const c = 2 * Math.PI * r;
  return (
    <span className="relative inline-flex shrink-0 items-center justify-center" style={{ width: size, height: size }}>
      <svg width={size} height={size} className="absolute inset-0" style={{ animation: "spin 1.1s linear infinite" }}>
        <circle cx={size / 2} cy={size / 2} r={r} fill="none" stroke="var(--color-graphite)" strokeWidth={stroke} />
        <circle
          cx={size / 2} cy={size / 2} r={r} fill="none"
          stroke="var(--color-fog)" strokeWidth={stroke} strokeLinecap="round"
          strokeDasharray={`${c * 0.28} ${c * 0.72}`}
        />
      </svg>
      <span className="relative font-mono text-[10.5px] font-medium tabular-nums text-mist">{children}</span>
    </span>
  );
}

function StatusBadgeDot({ tone, children }: { tone: "green" | "red"; children?: React.ReactNode }) {
  return (
    <span
      className={`flex size-6 shrink-0 items-center justify-center rounded-full text-white ${
        tone === "red" ? "bg-coral-red" : "bg-pulse-green"
      }`}
      style={{ animation: "pop-in 300ms cubic-bezier(0.23,1,0.32,1) both" }}
    >
      {children}
    </span>
  );
}

const CheckGlyph = (
  <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="3.5" strokeLinecap="round" strokeLinejoin="round"><path d="M20 6L9 17l-5-5" /></svg>
);
const CrossGlyph = (
  <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="3.5" strokeLinecap="round"><path d="M18 6L6 18M6 6l12 12" /></svg>
);
function PlanProgressItem({ task, index }: { task: PlanTask; index: number }) {
  const [open, setOpen] = useState(false);
  const done = task.status === "completed";
  const failed = task.status === "failed";
  const active = task.status === "in_progress";
  const expandable = !!task.note;

  return (
    <div
      className="overflow-hidden"
      style={{ animation: `fade-up 450ms cubic-bezier(0.23,1,0.32,1) ${index * 80}ms both` }}
    >
      <button
        type="button"
        aria-expanded={expandable ? open : undefined}
        onClick={() => expandable && setOpen((v) => !v)}
        className="flex h-11 w-full items-center gap-2.5 px-2.5 text-left"
      >
        <span className="flex size-6 shrink-0 items-center justify-center">
          {done ? (
            <StatusBadgeDot tone="green">{CheckGlyph}</StatusBadgeDot>
          ) : failed ? (
            <StatusBadgeDot tone="red">{CrossGlyph}</StatusBadgeDot>
          ) : active ? (
            <SpinnerRing />
          ) : (
            <span className="h-2 w-2 rounded-[2px] border border-graphite" />
          )}
        </span>
        <span
          className={`min-w-0 flex-1 truncate text-xs font-medium ${
            done ? "text-fog line-through" : failed ? "text-coral-red" : "text-mist"
          }`}
        >
          {task.label}
        </span>
        {done && (
          <span className="inline-flex h-[22px] shrink-0 items-center rounded-full bg-pulse-green/10 px-2 text-[11.5px] font-medium text-[#4cc069]">
            Completed
          </span>
        )}
        {failed && (
          <span className="inline-flex h-[22px] shrink-0 items-center rounded-full bg-coral-red/10 px-2 text-[11.5px] font-medium text-coral-red">
            Failed
          </span>
        )}
        {expandable && (
          <span aria-hidden className="flex size-[18px] shrink-0 items-center justify-center text-ash">
            <svg
              width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.2" strokeLinecap="round" strokeLinejoin="round"
              className="transition-transform duration-300"
              style={{ transform: open ? "rotate(180deg)" : "rotate(0)" }}
            >
              <path d="M6 9l6 6 6-6" />
            </svg>
          </span>
        )}
      </button>

      {expandable && (
        <div
          className="grid transition-[grid-template-rows,opacity] duration-300"
          style={{
            gridTemplateRows: open ? "1fr" : "0fr",
            opacity: open ? 1 : 0,
            transitionTimingFunction: "cubic-bezier(0.23, 1, 0.32, 1)",
          }}
        >
          <div className="overflow-hidden">
            <div className="mb-2.5 grid grid-cols-[24px_1fr] gap-2.5 px-2.5">
              <span aria-hidden className="mx-auto h-full w-px bg-graphite" />
              <div className="flex flex-col gap-1.5">
                <div
                  className="flex items-center justify-between"
                  style={{ animation: "fade-up 300ms cubic-bezier(0.23,1,0.32,1) 120ms both" }}
                >
                  <span className="text-xs text-fog">{task.note}</span>
                </div>
              </div>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}

function TraceRowItem({ row }: { row: TraceRow }) {
  switch (row.kind) {
    case "narration":
      return <NarrationRow text={row.text || ""} streaming={row.streaming} />;
    case "thinking":
      return <ThinkingRow text={row.text || ""} streaming={row.streaming} />;
    case "tool":
      return <ToolRow row={row} />;
    case "decision":
      return row.decision ? <DecisionRow decision={row.decision} /> : null;
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
      <Markdown className={streaming ? "prose-streaming" : undefined}>{text}</Markdown>
      {streaming && (
        <span
          className="ml-0.5 inline-block h-3.5 w-[2px] translate-y-0.5 bg-acid-lime"
          style={{ animation: "breathe 1s var(--ease) infinite" }}
        />
      )}
    </div>
  );
}

// ── Thinking — the model's working, capped so the panel never drowns ──
//
// Header carries a sparkle that shimmers while deltas arrive and settles into
// "Thought for Xs" with real elapsed time when the stream ends. The body rides
// a timeline rail, expanded while working, capped once settled. No timers, no
// staged simulation: `streaming` is the only driver.

function ThinkingRow({ text, streaming }: { text: string; streaming?: boolean }) {
  const [manualExpanded, setManualExpanded] = useState<boolean | null>(null);
  // Mount time via lazy initializer (never a render-time ref read), and the
  // settled duration via a deferred commit — both keep the lint gate quiet.
  const [mountedAt] = useState(() => Date.now());
  const [settledSecs, setSettledSecs] = useState<number | null>(null);
  useEffect(() => {
    if (!streaming && settledSecs === null) {
      const t = window.setTimeout(
        () => setSettledSecs((Date.now() - mountedAt) / 1000),
        0,
      );
      return () => window.clearTimeout(t);
    }
  }, [streaming, settledSecs, mountedAt]);
  const show = !!text || !!streaming;
  if (!show) return null;

  // Expandable only when there is something to reveal beyond the cap.
  const clampable = text.length > 160;
  // Collapsed by default, streaming or not — the header ("Thinking…") is
  // the affordance; the body opens only on an explicit click.
  const expanded = manualExpanded ?? false;
  const settledLabel =
    settledSecs === null
      ? "Thought"
      : settledSecs < 60
        ? `Thought for ${settledSecs.toFixed(1)}s`
        : `Thought for ${Math.floor(settledSecs / 60)}m ${(settledSecs % 60).toFixed(0)}s`;

  return (
    <div className="flex flex-col">
      <button
        type="button"
        aria-expanded={expanded}
        onClick={() => setManualExpanded((v) => !(v ?? !!streaming))}
        className="flex w-fit items-center gap-2 rounded-md px-1 py-1 transition-colors hover:bg-white/5"
      >
        <svg width="14" height="14" viewBox="0 0 24 24" fill={streaming ? "var(--color-fog)" : "var(--color-ash)"} aria-hidden>
          <path d="M12 2l2.4 7.2L22 12l-7.6 2.8L12 22l-2.4-7.2L2 12l7.6-2.8z" />
        </svg>
        {streaming ? (
          <span
            className="bg-clip-text text-[13px] font-medium whitespace-nowrap text-transparent"
            style={{
              backgroundImage:
                "linear-gradient(90deg, var(--color-fog) 35%, var(--color-paper) 50%, var(--color-fog) 65%)",
              backgroundSize: "200% 100%",
              animation: "shimmer-text 1.4s linear infinite",
            }}
          >
            Thinking
          </span>
        ) : (
          <span className="text-[13px] font-medium whitespace-nowrap text-fog">
            {settledLabel}
          </span>
        )}
        {clampable && (
          <svg
            width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="var(--color-ash)" strokeWidth="2.2" strokeLinecap="round" strokeLinejoin="round"
            className="transition-transform duration-300"
            style={{ transform: expanded ? "rotate(180deg)" : "rotate(0)" }}
            aria-hidden
          >
            <path d="M6 9l6 6 6-6" />
          </svg>
        )}
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
          <div className="relative ml-[6px] mt-1 pl-4">
            <span aria-hidden className="absolute left-[3px] top-0 bottom-0 w-px bg-graphite" />
            <p
              className="font-mono text-[0.6875rem] leading-relaxed text-fog italic"
              style={expanded ? { animation: "fade-up 320ms cubic-bezier(0.23,1,0.32,1) both" } : undefined}
            >
              {text}
              {streaming && (
                <span
                  className="ml-0.5 inline-block h-3 w-[2px] translate-y-0.5 bg-acid-lime"
                  style={{ animation: "breathe 1s var(--ease) infinite" }}
                />
              )}
            </p>
          </div>
        </div>
      </div>
    </div>
  );
}

// ── Status glyph ──

function Glyph({ status }: { status?: TraceRow["status"] }) {
  if (status === "running") {
    return <Meter />;
  }
  if (status === "fail") {
    return <span className="text-coral-red leading-none">✕</span>;
  }
  if (status === "done") {
    return <span className="text-[#4cc069] leading-none">✓</span>;
  }
  if (status === "warning") {
    // A conflict the agent created or a checker that could not run — neither a
    // success nor a failure, so it stays neutral instead of hiding.
    return <span className="text-fog leading-none">!</span>;
  }
  return <span className="h-1 w-1 rounded-full bg-ash" />;
}

// ── Tool row ──
//
// Compact agent-run rows: verb icon, label, inline mono chip with the target.
// Hover swaps the icon for a chevron; rows carrying a summary expand to show
// what the tool actually did. Skill consultations keep their lime marker —
// a decision the agent made, not a file it touched.

function toolIcon(tool?: string) {
  switch (tool) {
    case "read_file":
    case "list_files":
    case "grep_codebase":
    case "fetch_url":
    case "web_search":
    case "github_search":
    case "context_build":
      return (
        <g fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z" /><path d="M14 2v6h6" /></g>
      );
    case "write_file":
    case "edit_file":
    case "multi_edit":
    case "delete_file":
    case "install_package":
      return (
        <g fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><path d="M17 3a2.8 2.8 0 1 1 4 4L7.5 20.5 2 22l1.5-5.5z" /></g>
      );
    case "run_command":
    case "run_shell":
    case "run_typecheck":
    case "run_lint":
    case "run_checks":
    case "build_apk":
    case "tail_logcat":
    case "test_wallet_roundtrip":
      return (
        <g fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><path d="M4 17l6-5-6-5M12 19h8" /></g>
      );
    case "read_skill":
      return <path d="M12 2l2.4 7.2L22 12l-7.6 2.8L12 22l-2.4-7.2L2 12l7.6-2.8z" />;
    default:
      return <circle cx="12" cy="12" r="4" />;
  }
}

function ToolRow({ row }: { row: TraceRow }) {
  const [open, setOpen] = useState(false);
  const running = row.status === "running";
  const decision = row.tool === "read_skill";
  const expandable = !!row.summary && !running;

  const icon = (
    <svg
      width="13" height="13" viewBox="0 0 24 24"
      fill={row.tool === "read_skill" ? "currentColor" : "none"}
      stroke="currentColor"
      className="text-fog"
      aria-hidden
    >
      {toolIcon(row.tool)}
    </svg>
  );
  const chevron = (
    <svg
      width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.2" strokeLinecap="round" strokeLinejoin="round"
      className="text-fog transition-[opacity,transform] duration-150"
      style={{ transform: open ? "rotate(0deg)" : "rotate(-90deg)" }}
      aria-hidden
    >
      <path d="M6 9l6 6 6-6" />
    </svg>
  );

  return (
    <div style={{ animation: "fade-up 300ms cubic-bezier(0.23,1,0.32,1) both" }}>
      <button
        type="button"
        aria-expanded={expandable ? open : undefined}
        onClick={() => expandable && setOpen((v) => !v)}
        className={`group/row flex h-7 w-full min-w-0 items-center gap-2 rounded-md px-1.5 text-left transition-colors duration-100 ${
          running
            ? "bg-acid-lime/10 shadow-[inset_2px_0_0_var(--color-acid-lime)]"
            : expandable
              ? "hover:bg-white/5"
              : ""
        }`}
      >
        <span className="relative flex size-4 shrink-0 items-center justify-center">
          {decision && !running && row.status !== "fail" ? (
            <span aria-hidden className="h-2 w-2 flex-shrink-0 rounded-[2px] bg-acid-lime" />
          ) : running ? (
            <Glyph status={row.status} />
          ) : (
            <>
              <span className={`transition-opacity duration-100 ${expandable ? "group-hover/row:opacity-0" : ""} ${open ? "opacity-0" : ""}`}>
                {icon}
              </span>
              {expandable && (
                <span className={`absolute transition-[opacity,transform] duration-150 ${open ? "opacity-100" : "opacity-0 group-hover/row:opacity-100"}`}>
                  {chevron}
                </span>
              )}
            </>
          )}
        </span>

        <span
          className={`shrink-0 text-[12.5px] font-medium ${
            running ? "text-acid-lime" : "text-mist"
          }`}
        >
          {row.label}
        </span>

        {/* Live progress hint while running (gradle task / percent) — the
            row reads at a glance instead of requiring the expanded tail. */}
        {running && row.progress ? (
          <span className="shrink-0 truncate font-mono text-[11.5px] text-fog">
            {row.progress}
          </span>
        ) : null}

        {/* The path is the machine's word for the thing, so it is set in mono and
            truncated from the left — the filename matters more than the folder. */}
        {row.target && (
          <span
            dir="rtl"
            title={row.target}
            className="inline-flex h-[22px] min-w-0 flex-1 cursor-pointer items-center truncate rounded-[6px] bg-white/5 px-1.5 font-mono text-[11.5px] text-fog"
          >
            <span dir="ltr" className="truncate">{row.target}</span>
          </span>
        )}
      </button>

      {expandable && (
        <div
          className="grid transition-[grid-template-rows,opacity] duration-300"
          style={{
            gridTemplateRows: open ? "1fr" : "0fr",
            opacity: open ? 1 : 0,
            transitionTimingFunction: "cubic-bezier(0.23, 1, 0.32, 1)",
          }}
        >
          <div className="min-h-0 overflow-hidden">
            <div className="mb-1 ml-2 mt-0.5 flex flex-col gap-0.5 border-l border-graphite py-0.5 pl-3.5">
              <span
                className={`break-words text-[11.5px] leading-[1.6] ${
                  row.status === "fail" ? "text-coral-red" : "text-fog"
                }`}
              >
                {row.summary}
              </span>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}

// ── Decision card — a recorded ADR or memory, shown inline ──

// A decision the agent committed to (write_adr) or a fact it chose to remember
// (write_memory) is not just another tool row: it is a choice the user should be
// able to see now and trust later. It gets a card marked in acid lime — the
// working colour, because a decision is the agent acting — mirroring the "What
// the agent knows" panel so the same record reads the same way in both places.
// A memory carries its own category; a warning keeps the fog tone, never red,
// unless it is a genuine failure.
function DecisionRow({ decision }: { decision: DecisionCard }) {
  const isADR = decision.variant === "adr";
  const title = isADR ? decision.decision : decision.content;
  if (!title) return null;

  const label = isADR ? "architecture decision" : decision.category || "fact";
  const tone = label.toLowerCase() === "warning" ? "text-fog" : "text-acid-lime";
  const sub = isADR ? decision.outcome : undefined;

  return (
    <div className="my-1 min-w-0 rounded-xl border border-graphite bg-carbon p-3 shadow-[inset_2px_0_0_var(--color-acid-lime)]">
      <div className="mb-1.5 flex items-center gap-2">
        <span aria-hidden className="h-2 w-2 flex-shrink-0 rounded-[2px] bg-acid-lime" />
        <span className={`gauge ${tone}`}>{label}</span>
        {!isADR && decision.metadata && (
          <span className="gauge min-w-0 truncate text-ash" title={decision.metadata}>
            {decision.metadata}
          </span>
        )}
      </div>
      <p className="break-words text-xs font-medium leading-relaxed text-mist">
        {title}
      </p>
      {sub && (
        <p className="mt-1 break-words font-mono text-xs leading-relaxed text-fog">{sub}</p>
      )}
    </div>
  );
}

// ── Collapsed run of read-only calls ──

function GroupRow({ row }: { row: TraceRow }) {
  const [expanded, setExpanded] = useState(false);
  const count = row.children?.length ?? 0;

  // A group with nothing in it is a wire artifact, not an act — render
  // nothing rather than a blank dot and a "0 calls" chip that reads as
  // broken. groupRows never emits one; this is the backstop for restored
  // or hand-shaped rows.
  if (count === 0) return null;

  return (
    <div>
      <button
        type="button"
        onClick={() => setExpanded((v) => !v)}
        aria-expanded={expanded}
        className={`flex w-full items-center gap-2 rounded-md px-1.5 py-1 text-left ${
          row.status === "running"
            ? "bg-acid-lime/10 shadow-[inset_2px_0_0_var(--color-acid-lime)]"
            : "hover:bg-white/5"
        }`}
      >
        <span className="flex h-4 w-3.5 flex-shrink-0 items-center justify-center font-mono text-xs">
          <Glyph status={row.status} />
        </span>
        <span className="text-xs text-fog">{row.summary}</span>
        <span className="gauge tnum ml-auto flex-shrink-0">
          {count} {count === 1 ? "call" : "calls"}
        </span>
        <svg
          aria-hidden
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          strokeWidth={2}
          className={`h-3 w-3 flex-shrink-0 text-ash transition-transform ${
            expanded ? "rotate-90" : ""
          }`}
        >
          <path strokeLinecap="round" strokeLinejoin="round" d="M9 5l7 7-7 7" />
        </svg>
      </button>

      {expanded && row.children && (
        <div className="ml-3 border-l border-graphite pl-2">
          {row.children.map((child) => (
            <TraceRowItem key={child.id} row={child} />
          ))}
        </div>
      )}
    </div>
  );
}
