"use client";

import { useEffect, useRef, useState } from "react";
import { useRouter } from "next/navigation";
import Link from "next/link";
import { auth } from "@/lib/api";
import { setRefreshToken, setToken, setEmail as persistEmail } from "@/lib/auth";

/**
 * The OAuth landing page: the provider redirected here with a single-use
 * code, which this page exchanges for the session JWT. The token never
 * appears in a URL — it crosses from the exchange response into SecureStore
 * (web: lib/auth storage) and the URL is consumed.
 *
 * Errors are named plainly: "denied" (the user refused consent),
 * "provider-unavailable" (the provider hiccuped — retry), "server-error".
 */
export default function AuthCallbackPage() {
  const router = useRouter();
  const [phase, setPhase] = useState<"exchanging" | "done" | "error">("exchanging");
  const [error, setError] = useState("");
  const exchanged = useRef(false);

  useEffect(() => {
    // Exactly once: the code is single-use and React 18 double-invokes
    // effects in dev. A ref, not a flag in state, guards the boundary.
    if (exchanged.current) return;
    exchanged.current = true;

    const params = new URLSearchParams(window.location.search);
    const code = params.get("code");
    const next = params.get("next");

    if (!code) {
      setPhase("error");
      setError("The sign-in response carried no code — start again from the sign-in page.");
      return;
    }

    // A bounded exchange: the single-use code lives 60s, and a fetch that
    // can neither succeed nor fail (unreachable API, hung socket) must not
    // freeze the page forever. 20s is honest — long past any real exchange,
    // short enough to name the problem while the user is still looking.
    const exchangeWithTimeout = Promise.race([
      auth.exchange(code),
      new Promise<never>((_, reject) =>
        setTimeout(
          () => reject(new Error("The sign-in exchange timed out — the API did not answer. Check that the backend is running, then sign in again.")),
          20000,
        ),
      ),
    ]);

    exchangeWithTimeout
      .then((data) => {
        setToken(data.token);
        if (data.refresh_token) setRefreshToken(data.refresh_token);
        // The email cache is what the top bar reads instantly on load; /me
        // remains the truth it is reconciled against.
        if (data.user?.email) persistEmail(data.user.email);
        // Same-origin paths only; the redirect was sanitized server-side
        // too, this is the second lock on the same door.
        const target = next && next.startsWith("/") && !next.startsWith("//") ? next : "/dashboard";
        router.push(target);
      })
      .catch((err) => {
        setPhase("error");
        setError(err instanceof Error ? err.message : "The code exchange failed — sign in again.");
      });
  }, [router]);

  return (
    <div className="min-h-screen flex items-center justify-center px-4 bg-void">
      <div className="w-full max-w-sm">
        <div className="card text-center">
          <h1 className="text-[17px] font-medium tracking-tight text-paper mb-2">
            {phase === "exchanging" && "Finishing sign-in…"}
            {phase === "done" && "Signed in"}
            {phase === "error" && "Sign-in failed"}
          </h1>
          <p className="text-sm text-fog">
            {phase === "exchanging" && "Exchanging the one-time code for your session."}
            {phase === "done" && "Taking you to your projects."}
            {phase === "error" && error}
          </p>
          {phase === "error" && (
            <Link
              href="/login"
              className="inline-block mt-6 h-10 px-4 leading-10 rounded-lg border border-smoke bg-gradient-to-b from-carbon to-void text-sm text-paper transition-colors hover:border-mist/40"
            >
              Back to sign-in
            </Link>
          )}
        </div>
      </div>
    </div>
  );
}
