"use client";

import Link from "next/link";
import { PillButton, StatusBadge } from "@/components/ui";

interface TopBarProps {
  projectName?: string;
  tier?: "free" | "pro";
  promptsUsed?: number;
  promptsLimit?: number;
  onMenuToggle?: () => void;
}

export default function TopBar({
  projectName,
  tier = "free",
  promptsUsed = 0,
  promptsLimit = 20,
  onMenuToggle,
}: TopBarProps) {
  return (
    <header className="h-14 flex items-center justify-between px-4 hairline-bottom bg-surface-container-low">
      {/* Left: Logo + Menu */}
      <div className="flex items-center gap-3">
        {onMenuToggle && (
          <button
            onClick={onMenuToggle}
            className="md:hidden p-2 rounded-lg hover:bg-surface-container-high"
            aria-label="Toggle menu"
          >
            <svg className="w-5 h-5" fill="none" viewBox="0 0 24 24" stroke="currentColor">
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M4 6h16M4 12h16M4 18h16" />
            </svg>
          </button>
        )}

        <Link href="/" className="flex items-center gap-2">
          <div className="w-7 h-7 rounded-lg solana-gradient flex items-center justify-center">
            <span className="text-white font-bold text-xs">S</span>
          </div>
          <span className="font-semibold text-sm hidden sm:inline">SeekerStudio</span>
        </Link>

        {projectName && (
          <>
            <span className="text-outline-variant">/</span>
            <span className="text-sm text-on-surface-variant truncate max-w-[200px]">
              {projectName}
            </span>
          </>
        )}
      </div>

      {/* Right: Usage + Tier */}
      <div className="flex items-center gap-3">
        {projectName && (
          <div className="hidden sm:flex items-center gap-2 text-xs text-on-surface-variant">
            <span className="font-mono">{promptsUsed}/{promptsLimit}</span>
            <span>prompts</span>
          </div>
        )}

        <StatusBadge variant={tier} />

        <Link href="/dashboard">
          <PillButton variant="ghost" size="sm">
            Dashboard
          </PillButton>
        </Link>
      </div>
    </header>
  );
}
