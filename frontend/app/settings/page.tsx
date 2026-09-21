"use client";

import { useEffect, useState } from "react";
import { useRouter } from "next/navigation";
import AppShell from "@/components/layout/AppShell";
import { PillButton, StatusBadge } from "@/components/ui";
import { auth, me as meApi, usage as usageApi, billing as billingApi, git as gitApi } from "@/lib/api";
import { isAuthenticated, signOut } from "@/lib/auth";
import type { GitStatus, MeProfile, Usage } from "@/lib/types";

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
        <span className="text-sm text-mist">{label}</span>
        <span className="font-mono text-xs text-fog tabular-nums">
          {Math.round(used)} / {limit} {unit}
        </span>
      </div>
      <div className="h-1.5 w-full rounded-full bg-white/5 overflow-hidden">
        <div
          className={`h-full rounded-full transition-[width] ${near ? "bg-coral-red" : "bg-fog"}`}
          style={{ width: `${pct * 100}%` }}
        />
      </div>
      <p className="mt-2 font-mono text-xs text-ash">
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

  // Source control (GitHub). `gitStatus` is the server's answer to what the
  // user can do right now; `gitBanner` reflects the OAuth callback's outcome.
  const [gitStatus, setGitStatus] = useState<GitStatus | null>(null);
  const [gitBusy, setGitBusy] = useState<"connecting" | "disconnecting" | null>(null);
  const [gitError, setGitError] = useState<string | null>(null);
  const [gitBanner, setGitBanner] = useState<{ kind: "ok" | "err" | "info"; text: string } | null>(
    null,
  );

  useEffect(() => {
    if (!isAuthenticated()) {
      router.push("/login");
      return;
    }

    let cancelled = false;
    // The profile is the page's spine, so its failure is fatal; usage is a
    // best-effort extra — if /v1/usage is down we still show identity and plan
    // rather than blanking the whole screen.
    Promise.all([
      meApi.get(),
      usageApi.get().catch(() => null),
      gitApi.status().catch(() => null),
    ])
      .then(([p, u, g]) => {
        if (cancelled) return;
        setProfile(p);
        setNameInput(p.display_name);
        setUsageData(u);
        setGitStatus(g);

        // The OAuth callback returns the browser here with ?github=connected|
        // error|cancelled. We read it from the URL rather than useSearchParams
        // (which would push this static route behind a Suspense boundary or
        // fail the production build), show a one-line result, then scrub the
        // param so a reload doesn't replay it. `g` above is already the
        // post-callback status, so a "connected" return needs no extra fetch.
        const outcome = new URLSearchParams(window.location.search).get("github");
        if (outcome) {
          setGitBanner(
            outcome === "connected"
              ? { kind: "ok", text: "GitHub connected." }
              : outcome === "cancelled"
                ? { kind: "info", text: "GitHub connection cancelled." }
                : { kind: "err", text: "GitHub connection failed — please try again." },
          );
          const url = new URL(window.location.href);
          url.searchParams.delete("github");
          window.history.replaceState({}, "", url.pathname + url.search + url.hash);
        }
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
    // Revoke server-side best-effort; local tokens clear regardless.
    void auth.revoke().finally(() => {
      signOut();
      router.push("/login");
    });
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

  async function handleGitConnect() {
    setGitBusy("connecting");
    setGitError(null);
    try {
      // Mirror billing.upgrade: the server mints an authorized URL and we hand
      // the browser to it. A Bearer token can't ride a top-level navigation, so
      // the server finishes the flow on its public callback and redirects back
      // here with ?github=…, which the effect above reads.
      const { url } = await gitApi.connect();
      window.location.assign(url);
    } catch (err) {
      setGitError(err instanceof Error ? err.message : "Could not start the GitHub connection.");
      setGitBusy(null);
    }
  }

  async function handleGitDisconnect() {
    setGitBusy("disconnecting");
    setGitError(null);
    try {
      await gitApi.disconnect();
      setGitStatus(await gitApi.status());
      setGitBanner(null);
    } catch (err) {
      setGitError(err instanceof Error ? err.message : "Could not disconnect GitHub.");
    } finally {
      setGitBusy(null);
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
        <h1 className="text-[22px] font-medium tracking-tight text-paper mb-1">Account</h1>
        <p className="text-sm text-fog mb-8">Your identity, plan, and usage.</p>

        {loading && (
          <div className="flex items-center justify-center py-20">
            <p className="gauge">Opening your account</p>
          </div>
        )}

        {!loading && error && (
          <div className="card">
            <p className="text-sm text-fog mb-4">{error}</p>
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
            <div className="card">
              <div className="flex items-center justify-between">
                <div>
                  <p className="gauge mb-2">Signed in as</p>
                  <p className="font-mono text-sm text-mist">{profile.email}</p>
                </div>
                <StatusBadge variant={shellTier}>{tier.toUpperCase()}</StatusBadge>
              </div>

              <div className="mt-6 pt-5 border-t border-graphite">
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
                {nameError && <p className="mt-2 text-xs text-coral-red">{nameError}</p>}
                {nameSaved && !nameError && (
                  <p className="mt-2 text-xs text-ash">Saved.</p>
                )}
              </div>

              <div className="mt-6 pt-5 border-t border-graphite flex items-center justify-between">
                <span className="text-sm text-fog">End this session on this device</span>
                <button type="button" onClick={handleSignOut} className="btn">
                  Sign out
                </button>
              </div>
            </div>

            {/* Plan — the honest paid state. Free users get a real upgrade path;
                paid users see renewal, card last-4, and any pending cancel, all
                from the subscription row that verified webhooks write. */}
            <div className="card">
              <div className="flex items-center justify-between mb-5">
                <p className="gauge">Plan</p>
                <StatusBadge variant={shellTier}>{tier.toUpperCase()}</StatusBadge>
              </div>

              {!isPaid && (
                <div>
                  <p className="text-sm text-fog mb-4">
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
                  <p className="mt-3 text-xs text-ash">
                    Test mode — no card is charged and no real payment is taken.
                  </p>
                </div>
              )}

              {isPaid && sub && (
                <div className="space-y-3">
                  {sub.cancel_at_period_end ? (
                    <p className="text-sm text-mist">
                      Your plan is set to cancel
                      {formatDate(sub.current_period_end)
                        ? ` on ${formatDate(sub.current_period_end)}`
                        : " at the end of this period"}
                      . You keep Pro until then, then return to Free.
                    </p>
                  ) : (
                    <p className="text-sm text-mist">
                      {formatDate(sub.current_period_end)
                        ? `Renews on ${formatDate(sub.current_period_end)}.`
                        : "Active subscription."}
                    </p>
                  )}

                  {sub.payment_last4 && (
                    <p className="text-xs text-fog font-mono">
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
                        <div className="rounded-md border border-graphite bg-white/5 p-4">
                          <p className="text-sm text-mist mb-3">
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
                <p className="text-sm text-fog">
                  You&apos;re on the {tier} plan. There&apos;s no self-serve billing to manage for
                  this tier.
                </p>
              )}

              {billingError && <p className="mt-3 text-xs text-coral-red">{billingError}</p>}
            </div>

            {/* Source control — the honest GitHub state, straight from GET
                /v1/git/status: whether the server has the OAuth app at all,
                whether this tier unlocks it, and whether an account is linked.
                The card never offers an action the server wouldn't honour. */}
            <div className="card">
              <div className="flex items-center justify-between mb-5">
                <p className="gauge">Source control</p>
                {gitStatus?.connected && gitStatus.login && (
                  <span className="font-mono text-xs text-fog">{gitStatus.login}</span>
                )}
              </div>

              {gitBanner && (
                <p
                  className={`mb-4 font-mono text-xs ${
                    gitBanner.kind === "ok"
                      ? "text-[#4cc069]"
                      : gitBanner.kind === "err"
                        ? "text-coral-red"
                        : "text-ash"
                  }`}
                >
                  {gitBanner.text}
                </p>
              )}

              {!gitStatus ? (
                <p className="text-sm text-fog">
                  Source control status is unavailable right now. Reload to try again.
                </p>
              ) : !gitStatus.configured ? (
                <p className="text-sm text-fog">
                  GitHub isn&apos;t set up on this server yet, so there&apos;s nothing to connect.
                  Once it&apos;s configured, you&apos;ll be able to push your projects to GitHub
                  from the editor.
                </p>
              ) : !gitStatus.paid ? (
                <p className="text-sm text-fog">
                  Pushing projects to GitHub is a Pro feature. Upgrade above to connect your
                  account and push straight from the editor.
                </p>
              ) : !gitStatus.connected ? (
                <div>
                  <p className="text-sm text-fog mb-4">
                    Connect your GitHub account to push your projects to a repository. We ask for
                    the <span className="font-mono text-xs">repo</span> scope so the editor can
                    create the repo and push to it — nothing more, and you can disconnect any time.
                  </p>
                  <PillButton
                    variant="primary"
                    onClick={handleGitConnect}
                    disabled={gitBusy === "connecting"}
                  >
                    {gitBusy === "connecting" ? "Opening GitHub…" : "Connect GitHub"}
                  </PillButton>
                </div>
              ) : (
                <div className="space-y-3">
                  <p className="text-sm text-mist">
                    Connected as <span className="font-mono">{gitStatus.login}</span>. Push a
                    project to GitHub from its editor toolbar.
                  </p>
                  <button
                    type="button"
                    onClick={handleGitDisconnect}
                    disabled={gitBusy === "disconnecting"}
                    className="btn"
                  >
                    {gitBusy === "disconnecting" ? "Disconnecting…" : "Disconnect GitHub"}
                  </button>
                </div>
              )}

              {gitError && <p className="mt-3 text-xs text-coral-red">{gitError}</p>}
            </div>

            {/* Usage — the real metered numbers from /v1/usage. Best-effort: if
                it failed to load we say so rather than showing zeros. */}
            <div className="card">
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
                  <div className="flex items-baseline justify-between border-t border-graphite pt-4">
                    <span className="text-sm text-mist">Model spend this cycle</span>
                    <span className="font-mono text-sm tabular-nums text-mist">
                      ${(usageData.cost_usd ?? 0).toFixed(4)}
                    </span>
                  </div>
                </div>
              ) : (
                <p className="text-sm text-fog">
                  Usage numbers are unavailable right now. Reload to try again.
                </p>
              )}
            </div>

            {/* Billing history — the audit log, in plain words. Only shown once
                there is something to show. */}
            {profile.history.length > 0 && (
              <div className="card">
                <p className="gauge mb-5">Billing history</p>
                <ul className="space-y-3">
                  {profile.history.map((ev, i) => (
                    <li
                      key={`${ev.type}-${ev.created_at}-${i}`}
                      className="flex items-center justify-between text-sm"
                    >
                      <span className="text-mist">{EVENT_LABELS[ev.type] ?? ev.type}</span>
                      <span className="font-mono text-xs text-fog tabular-nums">
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
