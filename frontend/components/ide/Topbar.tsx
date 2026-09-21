"use client";

/**
 * Topbar — the strip spanning the sidebar and the canvas.
 *
 * Files, Terminal, Memory, Triage, and Device all live here now as dock
 * toggles (moved out of the old left icon rail, which is project navigation
 * now — see IconRail.tsx). Opening two or more shows the split-layout
 * button so the open panels can tile side by side or stacked, same pattern
 * as Claude Desktop's multi-artifact view.
 */

import { useEffect, useState } from "react";
import type { DockPanel } from "./IDELayout";

interface TopbarProps {
  projectName: string;
  projectStatus?: string;
  verifiedAgo?: string | null;
  gitBranch?: string | null;
  gitDirty?: number | null;
  gitAheadBehind?: string | null;
  onExit: () => void;
  onOpenPalette: () => void;
  onExport?: () => void;
  openPanels: Record<DockPanel, boolean>;
  onTogglePanel: (panel: DockPanel) => void;
  changedCount?: number;
  dockSplit: "col" | "row";
  onToggleSplit: () => void;
}

const DOCK_ICONS: Record<DockPanel, { label: string; path: string }> = {
  files: { label: "Files", path: "M3 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z" },
  terminal: { label: "Terminal", path: "M3 4h18v16H3zM7 9l3.5 3L7 15M13 15h4" },
  memory: {
    label: "Memory",
    path: "M12 3a5 5 0 0 0-5 5v1a4 4 0 0 0-2 7l1 1v2h12v-2l1-1a4 4 0 0 0-2-7V8a5 5 0 0 0-5-5z",
  },
  triage: { label: "Triage", path: "M12 8v5M12 16h.01M10.3 3.9 2.6 17a2 2 0 0 0 1.7 3h15.4a2 2 0 0 0 1.7-3L13.7 3.9a2 2 0 0 0-3.4 0z" },
  device: { label: "Device preview", path: "M7 2h10v20H7z|M11 18h2" },
};

export default function Topbar({
  projectName,
  projectStatus,
  verifiedAgo,
  gitBranch,
  gitDirty,
  gitAheadBehind,
  onExit,
  onOpenPalette,
  onExport,
  openPanels,
  onTogglePanel,
  changedCount = 0,
  dockSplit,
  onToggleSplit,
}: TopbarProps) {
  const [mod, setMod] = useState("⌘");
  useEffect(() => {
    setMod(/Mac|iPhone|iPad/.test(navigator.platform) ? "⌘" : "Ctrl");
  }, []);

  const openCount = (Object.values(openPanels) as boolean[]).filter(Boolean).length;

  return (
    <header className="surface-1 relative z-30 flex h-12 flex-shrink-0 items-center gap-2.5 px-3 shadow-[0_1px_0_var(--seam)]">
      <button
        type="button"
        onClick={onExit}
        className="group flex items-center gap-2 rounded-lg py-1 pr-2 transition-colors duration-150 hover:bg-[var(--surface-2)]"
        aria-label="Back to your projects"
      >
        <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2} strokeLinecap="round" strokeLinejoin="round" className="text-[var(--label-3)]">
          <path d="M15 18l-6-6 6-6" />
        </svg>
        <span className="min-w-0">
          <span className="block truncate text-sm font-semibold text-[var(--label)]">{projectName}</span>
        </span>
      </button>
      {projectStatus ? (
        <span className="gauge hidden flex-shrink-0 sm:inline">{projectStatus}</span>
      ) : null}
      {gitBranch ? (
        <span
          className="hidden sm:inline-flex items-center gap-1.5 rounded-full bg-[var(--surface-2)] px-2.5 py-0.5 font-mono text-xs text-[var(--label-2)]"
          title={`${gitBranch}${gitDirty ? ` · ${gitDirty} dirty` : " · clean"}${gitAheadBehind ? ` · ${gitAheadBehind}` : ""}`}
        >
          <span className={`h-2 w-2 rounded-full ${gitDirty ? "bg-amber-400" : "bg-emerald-400"}`} />
          {gitBranch}
          {gitDirty ? ` · ${gitDirty}` : ""}
          {gitAheadBehind ? ` · ${gitAheadBehind}` : ""}
        </span>
      ) : null}

      <span className="flex-1" />

      {verifiedAgo ? (
        <span className="hidden items-center gap-[7px] rounded-full bg-[rgb(53_208_160/12%)] px-2.5 py-0.5 text-xs font-semibold text-[var(--ok)] sm:inline-flex">
          <span className="h-[6px] w-[6px] animate-breathe rounded-full bg-current" />
          Build verified · {verifiedAgo}
        </span>
      ) : null}

      <span className="hidden items-center gap-1.5 text-xs text-[var(--label-3)] sm:flex">
        <kbd className="rounded bg-[var(--surface-2)] px-[5px] py-[3px] font-mono text-[11px] leading-none text-[var(--label-2)]">{mod}</kbd>
        <kbd className="rounded bg-[var(--surface-2)] px-[5px] py-[3px] font-mono text-[11px] leading-none text-[var(--label-2)]">K</kbd>
      </span>

      <div className="flex items-center gap-0.5">
        {(Object.keys(DOCK_ICONS) as DockPanel[]).map((key) => (
          <DockButton
            key={key}
            label={DOCK_ICONS[key].label}
            active={openPanels[key]}
            onClick={() => onTogglePanel(key)}
            badge={key === "files" ? changedCount : undefined}
          >
            {DOCK_ICONS[key].path.split("|").map((d, i) => (
              <path key={i} d={d} />
            ))}
          </DockButton>
        ))}

        {openCount > 1 && (
          <>
            <span className="mx-1 h-4.5 w-px bg-[var(--seam-strong)]" />
            <TopbarButton label={dockSplit === "row" ? "Stack panels" : "Split panels side by side"} onClick={onToggleSplit}>
              {dockSplit === "row" ? <path d="M3 12h18M3 3h18v18H3z" /> : <path d="M12 3v18M3 3h18v18H3z" />}
            </TopbarButton>
          </>
        )}

        <span className="mx-1 h-4.5 w-px bg-[var(--seam-strong)]" />
        <TopbarButton label="Command palette" onClick={onOpenPalette}>
          <path d="M18 3a3 3 0 0 0-3 3v12a3 3 0 1 0 3-3H6a3 3 0 1 0 3 3V6a3 3 0 1 0-3 3h12a3 3 0 1 0-3-3" />
        </TopbarButton>
        <TopbarButton label="Export APK" onClick={onExport} disabled={!onExport}>
          <path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4M7 10l5 5 5-5M12 15V3" />
        </TopbarButton>
      </div>
    </header>
  );
}

function DockButton({
  label,
  active,
  badge,
  onClick,
  children,
}: {
  label: string;
  active: boolean;
  badge?: number;
  onClick: () => void;
  children: React.ReactNode;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      aria-pressed={active}
      title={label}
      aria-label={label}
      className={`group relative grid size-8 place-items-center rounded-[9px] transition-colors duration-150 ease-out ${
        active ? "bg-[var(--accent-soft)] text-[var(--accent-2)]" : "text-[var(--label-3)] hover:bg-[var(--surface-2)] hover:text-[var(--label)]"
      }`}
    >
      <svg aria-hidden width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={1.75} strokeLinecap="round" strokeLinejoin="round">
        {children}
      </svg>
      {badge ? (
        <span className="absolute -right-0.5 -top-0.5 grid h-3.5 min-w-3.5 place-items-center rounded-full bg-[var(--accent)] px-[3px] font-mono text-[8.5px] font-bold text-black">
          {badge}
        </span>
      ) : null}
      <Tooltip label={label} />
    </button>
  );
}

function TopbarButton({
  label,
  onClick,
  disabled,
  children,
}: {
  label: string;
  onClick?: () => void;
  disabled?: boolean;
  children: React.ReactNode;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      disabled={disabled}
      title={label}
      aria-label={label}
      className="group relative grid size-8 place-items-center rounded-[9px] text-[var(--label-2)] transition-colors duration-150 ease-out hover:bg-[var(--surface-2)] hover:text-[var(--label)] disabled:opacity-40"
    >
      <svg aria-hidden width="17" height="17" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={1.75} strokeLinecap="round" strokeLinejoin="round">
        {children}
      </svg>
      <Tooltip label={label} />
    </button>
  );
}

function Tooltip({ label }: { label: string }) {
  return (
    <span
      aria-hidden
      className="pointer-events-none absolute right-0 top-[calc(100%+8px)] z-60 whitespace-nowrap rounded-md bg-[#2c2c2e] px-2 py-[3px] text-xs leading-[18px] text-[#f9fafb] opacity-0 shadow-[0_6px_24px_rgb(0_0_0/0.4)] transition-opacity duration-120 group-hover:opacity-100"
    >
      {label}
    </span>
  );
}
