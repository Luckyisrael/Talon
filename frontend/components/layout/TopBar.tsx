"use client";

import Link from "next/link";
import { useEffect, useState } from "react";
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
  const [email, setEmail] = useState<string | null>(null);
  useEffect(() => setEmail(getEmail()), []);

  function handleSignOut() {
    signOut();
    router.push("/login");
  }

  return (
    <header className="flex h-12 flex-shrink-0 items-center justify-between border-b border-rail bg-ink px-4">
      {/* The brand goes to the app home — the dashboard — not the marketing
          landing, because everyone who sees this bar is already signed in. */}
      <Link href="/dashboard" className="flex items-center gap-2.5">
        {/* A square, not a dot: the only round mark in the product is the signal
            lamp, and a brand dot beside it would read as device state. */}
        <span className="inline-block h-2 w-2 rounded-[1px] bg-paper" />
        <span className="hidden text-small font-semibold text-paper sm:inline">
          SeekerStudio
        </span>
      </Link>

      <div className="flex items-center gap-3">
        {/* The identity cluster is the way into account settings — the only
            place usage and plan live. */}
        <Link href="/settings" className="group flex items-center gap-3">
          {email && (
            <span
              className="hidden font-mono text-tiny text-graphite transition-colors group-hover:text-paper sm:inline"
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
