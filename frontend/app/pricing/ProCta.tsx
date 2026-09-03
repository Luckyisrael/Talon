"use client";

import { useSyncExternalStore } from "react";
import Link from "next/link";
import { isAuthenticated } from "@/lib/auth";

/**
 * The Pro call-to-action, which differs by who is looking. A signed-in visitor
 * can start the upgrade now — it runs in test mode, so the checkout page says
 * plainly that no card is charged. Everyone else is pointed at the waitlist,
 * because Pro is not a public self-serve purchase yet.
 *
 * The signed-out path is what renders on the server and at hydration; auth is
 * resolved only after mount, so there is no hydration mismatch and a logged-out
 * crawler sees the honest waitlist copy.
 */
export default function ProCta() {
  // Client-only auth read with a server snapshot of false, so the signed-out
  // path renders on the server and at hydration with no mismatch — and no
  // setState-in-effect.
  const authed = useSyncExternalStore(
    () => () => {},
    () => isAuthenticated(),
    () => false,
  );

  const linkClass =
    "btn-go mt-auto h-10 px-5 rounded-md text-sm inline-flex items-center justify-center";

  if (authed) {
    return (
      <>
        <Link href="/checkout?tier=pro" className={linkClass}>
          Upgrade to Pro
        </Link>
        <p className="mt-3 font-mono text-xs text-ash text-center">
          Test mode — no card is charged yet.
        </p>
      </>
    );
  }

  return (
    <>
      <Link href="/waitlist" className={linkClass}>
        Join the waitlist
      </Link>
      <p className="mt-3 font-mono text-xs text-ash text-center">
        Pro isn&apos;t a self-serve purchase yet — join the waitlist and we&apos;ll reach out
        when it opens.
      </p>
    </>
  );
}
