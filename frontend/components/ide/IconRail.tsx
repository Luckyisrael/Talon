"use client";

/**
 * IconRail — the left edge of the rebuilt project screen.
 *
 * Replaces the old top-bar tab switcher and the left panel's own header. The
 * whole navigation surface is five icons; the active one carries an accent
 * bar, and each has a DSH-style tooltip (dark, small, no border).
 *
 * The rail is 56px — wide enough for a 40px hit target, narrow enough that
 * the canvas keeps every remaining pixel. Below md the rail still shows, but
 * the conversation becomes a drawer reached from it.
 */

import { useEffect, useState } from "react";

export type RailView = "chat" | "files" | "memory" | "triage";

interface IconRailProps {
  view: RailView;
  onView: (view: RailView) => void;
  onToggleTerminal: () => void;
  onOpenPalette: () => void;
  /** The number of changed files this turn, or 0 — the files badge. */
  changedCount?: number;
  terminalOpen: boolean;
}

// One stroke family, one viewBox. The icons are inline so the rail never
// depends on an icon package or a sprite that could fail to load.
const ICONS = {
  msg: "M21 11.5a8.4 8.4 0 0 1-8.5 8.4c-1.5 0-3-.4-4.2-1.1L3 20l1.2-5.3A8.4 8.4 0 1 1 21 11.5z",
  files:
    "M4 4.5A1.5 1.5 0 0 1 5.5 3H10l2 2.5h6.5A1.5 1.5 0 0 1 20 7v12.5a1.5 1.5 0 0 1-1.5 1.5h-13A1.5 1.5 0 0 1 4 19.5z",
  term: "M3 4h18v16H3zM7 9l3.5 3L7 15M13 15h4",
  cmd: "M18 3a3 3 0 0 0-3 3v12a3 3 0 1 0 3-3H6a3 3 0 1 0 3 3V6a3 3 0 1 0-3 3h12a3 3 0 1 0-3-3",
  set: "M12 15a3 3 0 1 0 0-6 3 3 0 0 0 0 6z",
} as const;

function RailButton({
  icon,
  label,
  active,
  badge,
  onClick,
}: {
  icon: keyof typeof ICONS;
  label: string;
  active?: boolean;
  badge?: number;
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      aria-pressed={active}
      title={label}
      className={`relative grid h-10 w-10 place-items-center rounded-[11px] transition-colors duration-150 ease-out ${
        active
          ? "bg-[var(--accent-soft)] text-[var(--accent-2)]"
          : "text-[var(--label-3)] hover:bg-[var(--surface-2)] hover:text-[var(--label-2)]"
      }`}
    >
      {active ? (
        <span
          aria-hidden
          className="absolute -left-[14px] top-1/2 h-[18px] w-[3px] -translate-y-1/2 rounded-r-[3px] bg-[var(--accent)]"
        />
      ) : null}
      <svg
        aria-hidden
        width="19"
        height="19"
        viewBox="0 0 24 24"
        fill="none"
        stroke="currentColor"
        strokeWidth={1.75}
        strokeLinecap="round"
        strokeLinejoin="round"
      >
        {icon === "set" ? (
          <>
            <circle cx="12" cy="12" r="3" />
            <path d="M19.4 15a1.6 1.6 0 0 0 .3 1.8 2 2 0 1 1-2.8 2.8 1.6 1.6 0 0 0-2.7 1.1 2 2 0 1 1-4 0 1.6 1.6 0 0 0-2.7-1.1 2 2 0 1 1-2.8-2.8A1.6 1.6 0 0 0 3 15a2 2 0 1 1 0-4 1.6 1.6 0 0 0 1.6-2.7 2 2 0 1 1 2.8-2.8A1.6 1.6 0 0 0 10 4.6a2 2 0 1 1 4 0 1.6 1.6 0 0 0 2.7 1.1 2 2 0 1 1 2.8 2.8A1.6 1.6 0 0 0 21 11a2 2 0 1 1 0 4z" />
          </>
        ) : (
          <path d={ICONS[icon]} />
        )}
      </svg>
      {badge ? (
        <span className="absolute -right-0.5 -top-0.5 grid h-4 min-w-4 place-items-center rounded-full bg-[var(--accent)] px-1 font-mono text-[9px] font-bold text-white">
          {badge}
        </span>
      ) : null}
      {/* tooltip: dark, small, no border */}
      <span
        aria-hidden
        className="pointer-events-none absolute left-[calc(100%+10px)] top-1/2 z-60 -translate-y-1/2 rounded-md bg-[#2c2c2e] px-2 py-[3px] text-xs leading-[18px] whitespace-nowrap text-[#f9fafb] opacity-0 shadow-[0_6px_24px_rgb(0_0_0/0.4)] transition-opacity duration-120 group-hover:opacity-100"
      >
        {label}
      </span>
    </button>
  );
}

export default function IconRail({
  view,
  onView,
  onToggleTerminal,
  onOpenPalette,
  changedCount = 0,
  terminalOpen,
}: IconRailProps) {
  // ⌘K belongs to the screen, but the rail advertises it; keep the hint in
  // sync with the real key the layout listens for.
  const [mod, setMod] = useState("⌘");
  useEffect(() => {
    setMod(/Mac|iPhone|iPad/.test(navigator.platform) ? "⌘" : "Ctrl");
  }, []);

  return (
    <nav
      aria-label="Project navigation"
      className="surface-1 flex h-full flex-col items-center gap-1 py-2.5"
    >
      <RailButton
        icon="msg"
        label="Conversation"
        active={view === "chat"}
        onClick={() => onView("chat")}
      />
      <RailButton
        icon="files"
        label="Changed files"
        active={view === "files"}
        badge={changedCount}
        onClick={() => onView("files")}
      />
      <RailButton
        icon="term"
        label={terminalOpen ? "Hide terminal" : "Show terminal"}
        active={terminalOpen}
        onClick={onToggleTerminal}
      />

      <div className="mt-auto flex flex-col items-center gap-2">
        <RailButton icon="cmd" label={`Commands (${mod}K)`} onClick={onOpenPalette} />
        <RailButton icon="set" label="Project settings" onClick={() => onView("memory")} />
      </div>
    </nav>
  );
}
