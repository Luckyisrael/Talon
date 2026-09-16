"use client";

/**
 * Topbar — the 48px strip spanning the canvas and the icon rail.
 *
 * It does NOT span the device panel: the device column runs floor to ceiling.
 * One earned hairline underneath it, and that is the only vertical line in
 * the chrome.
 */

import { useEffect, useState } from "react";

interface TopbarProps {
  projectName: string;
  projectStatus?: string;
  /** When the last verified build happened, for the status pill. */
  verifiedAgo?: string | null;
  onExit: () => void;
  onToggleTerminal: () => void;
  onOpenPalette: () => void;
  onExport?: () => void;
}

export default function Topbar({
  projectName,
  projectStatus,
  verifiedAgo,
  onExit,
  onToggleTerminal,
  onOpenPalette,
  onExport,
}: TopbarProps) {
  const [mod, setMod] = useState("⌘");
  useEffect(() => {
    setMod(/Mac|iPhone|iPad/.test(navigator.platform) ? "⌘" : "Ctrl");
  }, []);

  return (
    <header
      className="surface-1 relative z-30 flex h-12 flex-shrink-0 items-center gap-2.5 px-3 shadow-[0_1px_0_var(--seam)]"
    >
      <button
        type="button"
        onClick={onExit}
        className="group flex items-center gap-2 rounded-lg py-1 pr-2 transition-colors duration-150 hover:bg-[var(--surface-2)]"
        aria-label="Back to your projects"
      >
        <span
          aria-hidden
          className="grid h-[22px] w-[22px] place-items-center rounded-[7px] bg-gradient-to-br from-[#7b5cff] to-[#4f7cf6]"
        >
          <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2} strokeLinecap="round" strokeLinejoin="round" className="text-white">
            <path d="M13 2L4.5 13.5H11l-1 8.5L19.5 10H13z" />
          </svg>
        </span>
        <span className="min-w-0">
          <span className="block truncate text-sm font-semibold text-[var(--label)]">
            {projectName}
          </span>
        </span>
      </button>
      {projectStatus ? (
        <span className="gauge hidden flex-shrink-0 sm:inline">{projectStatus}</span>
      ) : null}

      <span className="flex-1" />

      {verifiedAgo ? (
        <span className="hidden items-center gap-[7px] rounded-full bg-[rgb(53_208_160/12%)] px-2.5 py-0.5 text-xs font-semibold text-[var(--ok)] sm:inline-flex">
          <span className="h-[6px] w-[6px] animate-breathe rounded-full bg-current" />
          Build verified · {verifiedAgo}
        </span>
      ) : null}

      <span className="hidden items-center gap-1.5 text-xs text-[var(--label-3)] sm:flex">
        <kbd className="rounded bg-[var(--surface-2)] px-[5px] py-[3px] font-mono text-[11px] leading-none text-[var(--label-2)]">
          {mod}
        </kbd>
        <kbd className="rounded bg-[var(--surface-2)] px-[5px] py-[3px] font-mono text-[11px] leading-none text-[var(--label-2)]">
          K
        </kbd>
      </span>

      <TopbarButton label="Command palette" onClick={onOpenPalette}>
        <path d="M18 3a3 3 0 0 0-3 3v12a3 3 0 1 0 3-3H6a3 3 0 1 0 3 3V6a3 3 0 1 0-3 3h12a3 3 0 1 0-3-3" />
      </TopbarButton>
      <TopbarButton label="Terminal" onClick={onToggleTerminal}>
        <rect x="3" y="4" width="18" height="16" rx="2.5" />
        <path d="M7 9l3.5 3L7 15M13 15h4" />
      </TopbarButton>
      <TopbarButton label="Export APK" onClick={onExport} disabled={!onExport}>
        <path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4M7 10l5 5 5-5M12 15V3" />
      </TopbarButton>
    </header>
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
      <svg
        aria-hidden
        width="17"
        height="17"
        viewBox="0 0 24 24"
        fill="none"
        stroke="currentColor"
        strokeWidth={1.75}
        strokeLinecap="round"
        strokeLinejoin="round"
      >
        {children}
      </svg>
      <span
        aria-hidden
        className="pointer-events-none absolute left-[calc(100%+10px)] top-1/2 z-60 -translate-y-1/2 rounded-md bg-[#2c2c2e] px-2 py-[3px] text-xs leading-[18px] whitespace-nowrap text-[#f9fafb] opacity-0 shadow-[0_6px_24px_rgb(0_0_0/0.4)] transition-opacity duration-120 group-hover:opacity-100"
      >
        {label}
      </span>
    </button>
  );
}
