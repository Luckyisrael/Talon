"use client";

import Link from "next/link";
import { PillButton, StatusBadge } from "@/components/ui";

interface TopBarProps {
  projectName?: string;
  tier?: "free" | "pro";
  promptsUsed?: number;
  promptsLimit?: number;
}

export default function TopBar({
  projectName,
  tier = "free",
  promptsUsed = 0,
  promptsLimit = 20,
}: TopBarProps) {
  return (
    <header className="h-12 flex items-center justify-between px-4 bg-ink border-b border-rail">
      <div className="flex items-center gap-3">
        <Link href="/" className="flex items-center gap-2.5">
          {/* A square, not a dot: the only round mark in the product is the
              signal lamp, and a brand dot next to it would read as device state. */}
          <span className="inline-block w-2 h-2 rounded-[1px] bg-paper" />
          <span className="font-semibold text-small hidden sm:inline text-paper">
            SeekerStudio
          </span>
        </Link>

        {projectName && (
          <>
            <span className="text-dust">/</span>
            {/* A path segment — the machine's word, so mono. */}
            <span className="text-small font-mono text-graphite truncate max-w-[200px]">
              {projectName}
            </span>
          </>
        )}
      </div>

      <div className="flex items-center gap-3">
        {projectName && (
          <div className="hidden sm:flex items-center gap-1.5 text-tiny text-dust">
            <span className="font-mono tnum text-paper">
              {promptsUsed}/{promptsLimit}
            </span>
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
