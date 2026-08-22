"use client";

import { useEffect, useState } from "react";
import { useRouter } from "next/navigation";
import AppShell from "@/components/layout/AppShell";
import { PillButton, StatusBadge } from "@/components/ui";
import { me as meApi, usage as usageApi, billing as billingApi } from "@/lib/api";
import { isAuthenticated, signOut } from "@/lib/auth";
import type { MeProfile, Usage } from "@/lib/types";

/**
 * One metered allowance, drawn honestly: the fill is neutral until you are
 * close to the ceiling, then turns filament-amber — the palette's "a human
 * should look at this" colour — because nearing a quota is exactly that.
 */
function QuotaBar({
  label,
  used,
  limit,
  unit,
}: {
  label: string;
  used: number;
  limit: number;
  unit: string;
}) {
  const pct = limit > 0 ? Math.min(1, used / limit) : 0;
  const near = pct >= 0.9;
  const remaining = Math.max(0, limit - used);

  return (
    <div>
      <div className="flex items-baseline justify-between mb-2">
        <span className="text-small text-paper">{label}</span>
        <span className="font-mono text-tiny text-graphite tabular-nums">
          {Math.round(used)} / {limit} {unit}
        </span>
      </div>
      <div className="h-1.5 w-full rounded-1 bg-riser overflow-hidden">
        <div
          className={`h-full rounded-1 transition-[width] ${near ? "bg-filament" : "bg-graphite"}`}
          style={{ width: `${pct * 100}%` }}
        />
      </div>
      <p className="mt-2 text-tiny text-dust">
        {Math.round(remaining)} {unit} remaining this cycle
      </p>
    </div>
  );
}

/** A billing event rendered in plain words. Unknown types fall back to the raw
    name rather than being hidden — the audit log stays visible even as it grows. */
const EVENT_LABELS: Record<string, string> = {
  "subscription.active": "Subscription activated",
  "subscription.canceled": "Subscription canceled",
  "payment.failed": "Payment failed",
};

function formatDate(iso?: string): string | null {
  if (!iso) return null;
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return null;
  return d.toLocaleDateString(undefined, {
    year: "numeric",
    month: "long",
    day: "numeric",
  });
}

export default function SettingsPage() {
  const router = useRouter();
  const [profile, setProfile] = useState<MeProfile | null>(null);
  const [usageData, setUsageData] = useState<Usage | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  // Display-name editing. `nameInput` tracks the field; the save is a no-op
  // until it differs from what the server holds.
  const [nameInput, setNameInput] = useState("");
  const [nameSaving, setNameSaving] = useState(false);
  const [nameSaved, setNameSaved] = useState(false);
  const [nameError, setNameError] = useState<string | null>(null);

  // Billing actions (upgrade / cancel). One in flight at a time.
  const [billingBusy, setBillingBusy] = useState<"upgrading" | "cancelling" | null>(null);
  const [billingError, setBillingError] = useState<string | null>(null);
  const [confirmCancel, setConfirmCancel] = useState(false);

  useEffect(() => {
    if (!isAuthenticated()) {
      router.push("/login");
      return;
    }

    let cancelled = false;
    // The profile is the page's spine, so its failure is fatal; usage is a
    // best-effort extra — if /v1/usage is down we still show identity and plan
    // rather than blanking the whole screen.
    Promise.all([meApi.get(), usageApi.get().catch(() => null)])
      .then(([p, u]) => {
        if (cancelled) return;
        setProfile(p);
        setNameInput(p.display_name);
        setUsageData(u);
      })
      .catch((err: unknown) => {
        if (!cancelled) {
          setError(err instanceof Error ? err.message : "Could not load your account.");
        }
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });

    return () => {
      cancelled = true;
    };
  }, [router]);

  function handleSignOut() {
    signOut();
    router.push("/login");
  }

  async function handleSaveName() {
    if (!profile) return;
    const next = nameInput.trim();
    if (next === profile.display_name) return;
    setNameSaving(true);
    setNameSaved(false);
    setNameError(null);
    try {
      const updated = await meApi.update(next);
      setProfile(updated);
      setNameInput(updated.display_name);
      setNameSaved(true);
    } catch (err) {
      setNameError(err instanceof Error ? err.message : "Could not save your name.");
    } finally {
      setNameSaving(false);
    }
  }

  async function handleUpgrade() {
    setBillingBusy("upgrading");
    setBillingError(null);
    try {
      // Start a hosted checkout and hand the browser off to it, exactly as a
      // real processor's flow would. The card is entered there, never here.
      const checkout = await billingApi.upgrade("pro");
      window.location.assign(checkout.url);
    } catch (err) {
      setBillingError(err instanceof Error ? err.message : "Could not start the upgrade.");
      setBillingBusy(null);
    }
  }

  async function handleCancel() {
    setBillingBusy("cancelling");
    setBillingError(null);
    try {
      // At period end: a downgrade that keeps Pro until the term runs out.
      await billingApi.cancel(false);
      const refreshed = await meApi.get();
      setProfile(refreshed);
      setConfirmCancel(false);
    } catch (err) {
      setBillingError(err instanceof Error ? err.message : "Could not cancel your plan.");
    } finally {
      setBillingBusy(null);
    }
  }

  const tier = profile?.tier ?? "free";
  const shellTier = tier === "free" ? "free" : "pro";
  const sub = profile?.subscription;
  const isPaid = tier !== "free";
  const nameChanged = !!profile && nameInput.trim() !== profile.display_name;

  return (
    <AppShell tier={shellTier}>
      <div className="max-w-2xl mx-auto px-4 py-8">
        <h1 className="text-title font-semibold text-paper mb-1">Account</h1>
        <p className="text-small text-graphite mb-8">Your identity, plan, and usage.</p>

        {loading && (
          <div className="flex items-center justify-center py-20">
            <p className="gauge">Opening your account</p>
          </div>
        )}

        {!loading && error && (
          <div className="bay p-6">
            <p className="text-small text-graphite mb-4">{error}</p>
            <button type="button" onClick={() => router.refresh()} className="btn">
              Try again
            </button>
          </div>
        )}

        {!loading && !error && profile && (
          <div className="space-y-4">
            {/* Identity — email is the login handle and is fixed; the display
                name is the one editable field (email + tier are not settable
                from here). Everything shown is real server state from /v1/me. */}
            <div className="bay p-6">
              <div className="flex items-center justify-between">
                <div>
                  <p className="gauge mb-2">Signed in as</p>
                  <p className="font-mono text-body text-paper">{profile.email}</p>
                </div>
                <StatusBadge variant={shellTier}>{tier.toUpperCase()}</StatusBadge>
              </div>

              <div className="mt-6 pt-5 border-t border-rail">
                <label className="block">
                  <span className="gauge">Display name</span>
                  <div className="mt-2 flex items-center gap-2">
                    <input
                      type="text"
                      value={nameInput}
                      onChange={(e) => {
                        setNameInput(e.target.value);
                        setNameSaved(false);
                        setNameError(null);
                      }}
                      placeholder="Add a display name"
                      maxLength={100}
                      className="field"
                    />
                    <button
                      type="button"
                      onClick={handleSaveName}
                      disabled={!nameChanged || nameSaving}
                      className="btn"
                    >
                      {nameSaving ? "Saving…" : "Save"}
                    </button>
                  </div>
                </label>
                {nameError && <p className="mt-2 text-tiny text-fault">{nameError}</p>}
                {nameSaved && !nameError && (
                  <p className="mt-2 text-tiny text-dust">Saved.</p>
                )}
              </div>

              <div className="mt-6 pt-5 border-t border-rail flex items-center justify-between">
                <span className="text-small text-graphite">End this session on this device</span>
                <button type="button" onClick={handleSignOut} className="btn">
                  Sign out
                </button>
              </div>
            </div>

            {/* Plan — the honest paid state. Free users get a real upgrade path;
                paid users see renewal, card last-4, and any pending cancel, all
                from the subscription row that verified webhooks write. */}
            <div className="bay p-6">
              <div className="flex items-center justify-between mb-5">
                <p className="gauge">Plan</p>
                <StatusBadge variant={shellTier}>{tier.toUpperCase()}</StatusBadge>
              </div>

              {!isPaid && (
                <div>
                  <p className="text-small text-graphite mb-4">
                    You&apos;re on the Free plan — 20 prompts and 60 minutes of device time each
                    cycle. Pro lifts that to 500 prompts and 25 hours.
                  </p>
                  <PillButton
                    variant="primary"
                    onClick={handleUpgrade}
                    disabled={billingBusy === "upgrading"}
                  >
                    {billingBusy === "upgrading" ? "Starting checkout…" : "Upgrade to Pro"}
                  </PillButton>
                  <p className="mt-3 text-tiny text-dust">
                    Test mode — no card is charged and no real payment is taken.
                  </p>
                </div>
              )}

              {isPaid && sub && (
                <div className="space-y-3">
                  {sub.cancel_at_period_end ? (
                    <p className="text-small text-paper">
                      Your plan is set to cancel
                      {formatDate(sub.current_period_end)
                        ? ` on ${formatDate(sub.current_period_end)}`
                        : " at the end of this period"}
                      . You keep Pro until then, then return to Free.
                    </p>
                  ) : (
                    <p className="text-small text-paper">
                      {formatDate(sub.current_period_end)
                        ? `Renews on ${formatDate(sub.current_period_end)}.`
                        : "Active subscription."}
                    </p>
                  )}

                  {sub.payment_last4 && (
                    <p className="text-tiny text-graphite font-mono">
                      Card on file · •••• {sub.payment_last4}
                    </p>
                  )}

                  {/* Cancel is offered only while the plan is genuinely active and
                      not already winding down — the UI never shows an action that
                      would do nothing. */}
                  {sub.status === "active" && !sub.cancel_at_period_end && (
                    <div className="pt-2">
                      {!confirmCancel ? (
                        <button
                          type="button"
                          onClick={() => {
                            setConfirmCancel(true);
                            setBillingError(null);
                          }}
                          className="btn btn-stop"
                        >
                          Cancel subscription
                        </button>
                      ) : (
                        <div className="rounded-1 border border-rail bg-riser p-4">
                          <p className="text-small text-paper mb-3">
                            Cancel Pro?{" "}
                            {formatDate(sub.current_period_end)
                              ? `You'll keep it until ${formatDate(sub.current_period_end)}, then return to Free.`
                              : "You'll keep it until the end of the current period, then return to Free."}
                          </p>
                          <div className="flex items-center gap-2">
                            <button
                              type="button"
                              onClick={() => setConfirmCancel(false)}
                              disabled={billingBusy === "cancelling"}
                              className="btn"
                            >
                              Keep Pro
                            </button>
                            <button
                              type="button"
                              onClick={handleCancel}
                              disabled={billingBusy === "cancelling"}
                              className="btn btn-stop"
                            >
                              {billingBusy === "cancelling" ? "Cancelling…" : "Cancel subscription"}
                            </button>
                          </div>
                        </div>
                      )}
                    </div>
                  )}
                </div>
              )}

              {isPaid && !sub && (
                // Paid tier with no subscription row is an admin-granted plan
                // (e.g. team/enterprise): state it plainly rather than inventing
                // a renewal date we do not have.
                <p className="text-small text-graphite">
                  You&apos;re on the {tier} plan. There&apos;s no self-serve billing to manage for
                  this tier.
                </p>
              )}

              {billingError && <p className="mt-3 text-tiny text-fault">{billingError}</p>}
            </div>

            {/* Usage — the real metered numbers from /v1/usage. Best-effort: if
                it failed to load we say so rather than showing zeros. */}
            <div className="bay p-6">
              <p className="gauge mb-5">Usage this cycle</p>
              {usageData ? (
                <div className="space-y-6">
                  <QuotaBar
                    label="AI prompts"
                    used={usageData.prompts_used}
                    limit={usageData.prompts_limit}
                    unit="prompts"
                  />
                  <QuotaBar
                    label="Live device time"
                    used={usageData.device_minutes}
                    limit={usageData.device_limit}
                    unit="min"
                  />
                </div>
              ) : (
                <p className="text-small text-graphite">
                  Usage numbers are unavailable right now. Reload to try again.
                </p>
              )}
            </div>

            {/* Billing history — the audit log, in plain words. Only shown once
                there is something to show. */}
            {profile.history.length > 0 && (
              <div className="bay p-6">
                <p className="gauge mb-5">Billing history</p>
                <ul className="space-y-3">
                  {profile.history.map((ev, i) => (
                    <li
                      key={`${ev.type}-${ev.created_at}-${i}`}
                      className="flex items-center justify-between text-small"
                    >
                      <span className="text-paper">{EVENT_LABELS[ev.type] ?? ev.type}</span>
                      <span className="font-mono text-tiny text-graphite tabular-nums">
                        {formatDate(ev.created_at) ?? ev.created_at}
                      </span>
                    </li>
                  ))}
                </ul>
              </div>
            )}
          </div>
        )}
      </div>
    </AppShell>
  );
}
