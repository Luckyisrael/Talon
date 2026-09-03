"use client";

import Link from "next/link";
import { useSyncExternalStore } from "react";
import { useRouter } from "next/navigation";
import { StatusBadge } from "@/components/ui";
import { getEmail, signOut } from "@/lib/auth";

interface TopBarProps {
  tier?: "free" | "pro";
}

export default function TopBar({ tier = "free" }: TopBarProps) {
  const router = useRouter();
  // Read on the client only. The email lives in localStorage, so rendering it
  // during SSR would hydrate a mismatch (null on the server, a string here).
  // useSyncExternalStore carries the server snapshot (null) through hydration
  // instead of a setState-in-effect.
  const email = useSyncExternalStore(
    () => () => {},
    () => getEmail(),
    () => null,
  );

  function handleSignOut() {
    signOut();
    router.push("/login");
  }

  return (
    <header className="flex h-12 flex-shrink-0 items-center justify-between border-b border-graphite bg-void px-4">
      {/* The brand goes to the app home — the dashboard — not the marketing
          landing, because everyone who sees this bar is already signed in. */}
      <Link href="/dashboard" className="flex items-center gap-2">
        <svg width="18" height="18" viewBox="0 0 18 18" fill="none" aria-hidden>
          <rect x="1" y="1" width="16" height="16" rx="4" stroke="#ffffff" strokeWidth="1.5" />
          <circle cx="9" cy="9" r="2.5" fill="#e4f222" />
        </svg>
        <span className="text-base font-medium tracking-tight text-paper">
          SeekerStudio
        </span>
      </Link>

      <div className="flex items-center gap-3">
        {/* The identity cluster is the way into account settings — the only
            place usage and plan live. */}
        <Link href="/settings" className="group flex items-center gap-3">
          {email && (
            <span
              className="hidden font-mono text-xs text-fog transition-colors group-hover:text-mist sm:inline"
              title={email}
            >
              {email}
            </span>
          )}
          <StatusBadge variant={tier} />
        </Link>
        <button type="button" onClick={handleSignOut} className="btn btn-ghost">
          Sign out
        </button>
      </div>
    </header>
  );
}
