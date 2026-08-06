"use client";

import Link from "next/link";
import { Menu } from "iconsax-reactjs";
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
    <header className="h-14 flex items-center justify-between px-4 bg-surface-1">
      <div className="flex items-center gap-3">
        {onMenuToggle && (
          <button
            onClick={onMenuToggle}
            className="md:hidden p-2 rounded-lg hover:bg-surface-3"
            aria-label="Toggle menu"
          >
            <Menu size="20" className="text-text-secondary" variant="Linear" />
          </button>
        )}

        <Link href="/" className="flex items-center gap-3">
          <div className="w-3 h-3 rounded-full bg-text-primary" />
          <span className="font-semibold text-sm hidden sm:inline text-text-primary">SeekerStudio</span>
        </Link>

        {projectName && (
          <>
            <span className="text-text-muted">/</span>
            <span className="text-sm text-text-secondary truncate max-w-[200px]">
              {projectName}
            </span>
          </>
        )}
      </div>

      <div className="flex items-center gap-3">
        {projectName && (
          <div className="hidden sm:flex items-center gap-2 text-xs text-text-secondary">
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
