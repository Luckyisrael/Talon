"use client";

import { Suspense, useEffect, useState } from "react";
import { useRouter, useSearchParams } from "next/navigation";
import Link from "next/link";
import { PillButton } from "@/components/ui";
import { billing as billingApi } from "@/lib/api";
import { isAuthenticated } from "@/lib/auth";

// What each self-serve tier includes, kept in step with the pricing page and the
// limits the backend enforces. Only Pro is self-serve; anything else falls back
// to Pro's copy rather than rendering blank.
const TIER_COPY: Record<string, { name: string; points: string[] }> = {
  pro: {
    name: "Pro",
    points: [
      "500 AI prompts per cycle",
      "25 hours of live device time",
      "Everything in Free",
    ],
  },
};

/**
 * The test-mode checkout. A real processor would host this page and collect the
 * card; here it stands in for that step so the upgrade path runs end-to-end
 * without live payments. It reads the tier from the query string, so it must sit
 * under a Suspense boundary (useSearchParams forces client rendering).
 */
function CheckoutInner() {
  const router = useRouter();
  const params = useSearchParams();
  const tier = params.get("tier") || "pro";
  const plan = TIER_COPY[tier] ?? TIER_COPY.pro;

  const [ready, setReady] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    // Completing checkout needs the caller's identity, so an unauthenticated
    // visitor is sent to sign in and returned here afterwards.
    if (!isAuthenticated()) {
      const here = window.location.pathname + window.location.search;
      router.replace(`/login?next=${encodeURIComponent(here)}`);
      return;
    }
    // Deferred past the commit so the ready flag doesn't cascade-render
    // inside the effect (react-hooks/set-state-in-effect).
    const t = window.setTimeout(() => setReady(true), 0);
    return () => window.clearTimeout(t);
  }, [router]);

  async function handleComplete() {
    setSubmitting(true);
    setError(null);
    try {
      // Simulates the provider confirming payment. The server signs the event
      // and runs it through the same verified-webhook path a live payment would,
      // then grants the tier — nothing is trusted from this click directly.
      await billingApi.testComplete(tier);
      router.replace("/settings?upgrade=success");
    } catch (err) {
      setError(err instanceof Error ? err.message : "Could not complete the test upgrade.");
      setSubmitting(false);
    }
  }

  if (!ready) {
    return (
      <div className="flex items-center justify-center py-20">
        <p className="gauge">Preparing checkout</p>
      </div>
    );
  }

  return (
    <div className="card">
      <div className="flex items-center justify-between mb-1">
        <h1 className="text-[17px] font-medium tracking-tight text-paper">Checkout</h1>
        <span className="badge">TEST MODE</span>
      </div>
      <p className="text-sm text-fog mb-6">
        Upgrading to <span className="text-mist">{plan.name}</span>.
      </p>

      <ul className="space-y-2.5 mb-6">
        {plan.points.map((p) => (
          <li key={p} className="text-sm text-mist">
            · {p}
          </li>
        ))}
      </ul>

      {/* The honest core of this page: payments are not live yet, so there is no
          card field and nothing is charged. This simulates the provider's hosted
          checkout so the upgrade path works end-to-end in test mode. */}
      <div className="rounded-md border border-graphite bg-white/5 p-4 mb-6">
        <p className="text-sm text-mist mb-1">This is a simulated checkout.</p>
        <p className="font-mono text-xs text-ash leading-relaxed">
          No card is entered and no real payment is taken. Completing it grants the {plan.name}{" "}
          tier through the same verified path a real payment would use, so the flow can be
          tested before live payments are switched on.
        </p>
      </div>

      {error && <p className="text-sm text-coral-red mb-4">{error}</p>}

      <PillButton
        variant="primary"
        onClick={handleComplete}
        disabled={submitting}
        className="w-full"
      >
        {submitting ? "Completing…" : "Complete test upgrade"}
      </PillButton>

      <Link
        href="/settings"
        className="mt-4 block text-center text-sm text-fog underline underline-offset-2 hover:text-mist transition-colors"
      >
        Cancel and go back
      </Link>
    </div>
  );
}

export default function CheckoutPage() {
  return (
    <div className="min-h-screen flex flex-col items-center justify-center bg-void px-6 py-16">
      <div className="w-full max-w-md">
        <Suspense
          fallback={
            <div className="flex items-center justify-center py-20">
              <p className="gauge">Loading checkout</p>
            </div>
          }
        >
          <CheckoutInner />
        </Suspense>
      </div>
    </div>
  );
}
