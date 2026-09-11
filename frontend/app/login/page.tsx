"use client";

import { useEffect, useState } from "react";
import Link from "next/link";
import { auth } from "@/lib/api";

/**
 * Sign-in is OAuth: GitHub and Google — email/password is gone. The buttons
 * render from the server's honest provider list: a provider without
 * credentials on the server shows disabled with its reason, never a dead
 * click.
 *
 * The flow: the browser navigates to /v1/auth/{provider}/start (the backend
 * 302s to the provider's consent screen), the provider returns the browser
 * to /auth/callback?code=…, and that page exchanges the single-use code.
 */
export default function LoginPage() {
  const [providers, setProviders] = useState<{ id: string; name: string }[] | null>(null);
  const [unreachable, setUnreachable] = useState(false);

  useEffect(() => {
    // One fetch on mount: the provider list is a precondition for rendering
    // any button honestly. (The auth-gate exception to no-fetch-in-effect:
    // nothing can render until this answers, and the value is page-static.)
    auth
      .providers()
      .then((data) => setProviders(data.providers ?? []))
      .catch(() => {
        setProviders([]);
        setUnreachable(true);
      });
  }, []);

  /**
   * The `next` param survives the whole OAuth hop: login binds it into the
   * state token via the redirect, the callback lands with the code appended,
   * and the exchange page routes there after signing in. Same-origin paths
   * only — an open redirect on a sign-in page is a phishing primitive.
   */
  function start(providerID: string) {
    const next = new URLSearchParams(window.location.search).get("next");
    const target = next && next.startsWith("/") && !next.startsWith("//") ? next : "/dashboard";
    window.location.href = auth.startURL(providerID, `/auth/callback?next=${encodeURIComponent(target)}`);
  }

  const allProviders = [
    { id: "github", name: "GitHub" },
    { id: "google", name: "Google" },
  ];
  const isConfigured = (id: string) => providers?.some((p) => p.id === id) ?? false;

  return (
    <div className="min-h-screen flex items-center justify-center px-4 bg-void">
      <div className="w-full max-w-sm">
        <div className="flex justify-center mb-8">
          <Link href="/" className="flex items-center gap-2">
            <svg width="18" height="18" viewBox="0 0 18 18" fill="none" aria-hidden>
              <rect x="1" y="1" width="16" height="16" rx="4" stroke="#ffffff" strokeWidth="1.5" />
              <circle cx="9" cy="9" r="2.5" fill="#e4f222" />
            </svg>
            <span className="text-base font-medium tracking-tight text-paper">Talon</span>
          </Link>
        </div>

        <div className="card">
          <h1 className="text-[17px] font-medium tracking-tight text-paper text-center mb-1">Welcome back</h1>
          <p className="text-sm text-fog text-center mb-6">Sign in with GitHub or Google</p>

          <div className="flex flex-col gap-3">
            {allProviders.map((p) => {
              // providers===null means the list is still loading: hold every
              // click rather than guessing which are live.
              const state = providers === null ? "loading" : isConfigured(p.id) ? "on" : "off";
              return (
                <button
                  key={p.id}
                  type="button"
                  disabled={state !== "on"}
                  onClick={() => start(p.id)}
                  className={
                    state === "off"
                      ? "h-11 w-full rounded-lg border border-smoke/50 bg-ash/20 px-4 text-sm text-fog cursor-not-allowed"
                      : state === "loading"
                        ? "h-11 w-full rounded-lg border border-smoke bg-ash/10 px-4 text-sm text-fog/60 cursor-wait"
                        : "h-11 w-full rounded-lg border border-smoke bg-gradient-to-b from-carbon to-void px-4 text-sm text-paper transition-colors hover:border-mist/40 hover:from-ash/10"
                  }
                >
                  Continue with {p.name}
                  {state === "off" && (
                    <span className="block text-xs font-normal text-fog/70">not configured on this server</span>
                  )}
                </button>
              );
            })}
          </div>

          {providers !== null && providers.length === 0 && (
            <p className="text-sm text-coral-red mt-4">
              {unreachable
                ? "Could not reach the sign-in service — check your connection and reload."
                : "No sign-in providers are configured on this server. An operator must set AUTH_GITHUB_CLIENT_ID/SECRET or AUTH_GOOGLE_CLIENT_ID/SECRET."}
            </p>
          )}
        </div>

        <p className="text-xs text-fog/60 text-center mt-6">
          Your provider identity is your account — no passwords to keep, lose, or leak.
        </p>
      </div>
    </div>
  );
}
