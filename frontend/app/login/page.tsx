"use client";

import { useState } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import PillButton from "@/components/ui/PillButton";
import Spinner from "@/components/ui/Spinner";
import { auth } from "@/lib/api";
import { setToken } from "@/lib/auth";

export default function LoginPage() {
  const router = useRouter();
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");

  /**
   * An expired session bounces here with ?next=<where you were>. Returning you
   * to that page is the difference between an interruption and losing your place.
   *
   * Read at submit time from the URL rather than through `useSearchParams`: that
   * hook opts the whole client tree out of prerendering — it fails the build
   * outright unless the tree sits in a Suspense boundary — and nothing here
   * *renders* the value. It is only ever needed once, in this handler, after a
   * real interaction, by which point the URL is authoritative.
   */
  function destination(): string {
    const next = new URLSearchParams(window.location.search).get("next");
    // Same-origin paths only. An open redirect on the sign-in page would let a
    // crafted link hand a freshly-authenticated visitor to another site.
    return next && next.startsWith("/") && !next.startsWith("//") ? next : "/dashboard";
  }

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    setLoading(true);
    setError("");

    try {
      const data = await auth.login(email, password);
      setToken(data.token);
      router.push(destination());
    } catch (err) {
      setError(err instanceof Error ? err.message : "Something went wrong");
    } finally {
      setLoading(false);
    }
  }

  return (
    <div className="min-h-screen flex items-center justify-center px-4 bg-ink">
      <div className="w-full max-w-sm">
        <div className="flex justify-center mb-8">
          <Link href="/" className="flex items-center gap-2.5">
            <span className="inline-block w-2.5 h-2.5 rounded-[1px] bg-paper" />
            <span className="text-lead font-semibold text-paper">SeekerStudio</span>
          </Link>
        </div>

        <div className="bay p-6">
          <h1 className="text-lead font-semibold text-paper text-center mb-1">Welcome back</h1>
          <p className="text-small text-graphite text-center mb-6">Sign in to your account</p>

          <form onSubmit={handleSubmit}>
            <label className="block mb-4">
              <span className="gauge">Email</span>
              <input
                type="email"
                value={email}
                onChange={(e) => setEmail(e.target.value)}
                placeholder="you@example.com"
                autoComplete="email"
                required
                className="field mt-2"
                autoFocus
              />
            </label>

            <label className="block mb-4">
              <span className="gauge">Password</span>
              <input
                type="password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                placeholder="••••••••"
                autoComplete="current-password"
                required
                className="field mt-2"
              />
            </label>

            {error && <p className="text-small text-fault mb-4">{error}</p>}

            <PillButton type="submit" disabled={loading} className="w-full">
              {loading ? <Spinner size="sm" color="currentColor" /> : "Sign in"}
            </PillButton>
          </form>
        </div>

        <p className="text-small text-graphite text-center mt-6">
          Don&apos;t have an account?{" "}
          <Link href="/register" className="text-paper underline underline-offset-2 hover:text-white transition-colors">
            Sign up
          </Link>
        </p>
      </div>
    </div>
  );
}
