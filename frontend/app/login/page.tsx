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
    <div className="min-h-screen flex items-center justify-center px-4 bg-background">
      <div className="w-full max-w-sm">
        <div className="flex justify-center mb-8">
          <Link href="/" className="flex items-center gap-3">
            <div className="w-3 h-3 rounded-full bg-text-primary" />
            <span className="text-xl font-bold text-text-primary">SeekerStudio</span>
          </Link>
        </div>

        <div className="bg-surface-3 rounded-[20px] p-6">
          <h1 className="text-xl font-bold text-text-primary text-center mb-1">Welcome back</h1>
          <p className="text-sm text-text-secondary text-center mb-6">Sign in to your account</p>

          <form onSubmit={handleSubmit}>
            <label className="block mb-4">
              <span className="text-xs font-mono uppercase tracking-wider text-text-secondary">Email</span>
              <input
                type="email"
                value={email}
                onChange={(e) => setEmail(e.target.value)}
                placeholder="you@example.com"
                autoComplete="email"
                required
                className="mt-2 w-full h-11 px-4 rounded-xl bg-surface-2 text-text-primary placeholder:text-text-muted focus:outline-none focus:ring-2 focus:ring-accent/50 transition-all"
                autoFocus
              />
            </label>

            <label className="block mb-4">
              <span className="text-xs font-mono uppercase tracking-wider text-text-secondary">Password</span>
              <input
                type="password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                placeholder="••••••••"
                autoComplete="current-password"
                required
                className="mt-2 w-full h-11 px-4 rounded-xl bg-surface-2 text-text-primary placeholder:text-text-muted focus:outline-none focus:ring-2 focus:ring-accent/50 transition-all"
              />
            </label>

            {error && <p className="text-sm text-error mb-4">{error}</p>}

            <PillButton type="submit" disabled={loading} className="w-full">
              {loading ? <Spinner size="sm" color="currentColor" /> : "Sign In"}
            </PillButton>
          </form>
        </div>

        <p className="text-sm text-text-secondary text-center mt-6">
          Don&apos;t have an account?{" "}
          <Link href="/register" className="text-accent hover:text-accent-hover transition-colors">
            Sign up
          </Link>
        </p>
      </div>
    </div>
  );
}
