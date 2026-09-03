"use client";

import { useState } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import PillButton from "@/components/ui/PillButton";
import { auth } from "@/lib/api";
import { setToken, setEmail as persistEmail } from "@/lib/auth";

export default function RegisterPage() {
  const router = useRouter();
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    setLoading(true);
    setError("");

    try {
      const data = await auth.register(email, password);
      setToken(data.token);
      persistEmail(email);
      router.push("/dashboard");
    } catch (err) {
      setError(err instanceof Error ? err.message : "Something went wrong");
    } finally {
      setLoading(false);
    }
  }

  return (
    <div className="min-h-screen flex items-center justify-center px-4 bg-void">
      <div className="w-full max-w-sm">
        <div className="flex justify-center mb-8">
          <Link href="/" className="flex items-center gap-2">
            <svg width="18" height="18" viewBox="0 0 18 18" fill="none" aria-hidden>
              <rect x="1" y="1" width="16" height="16" rx="4" stroke="#ffffff" strokeWidth="1.5" />
              <circle cx="9" cy="9" r="2.5" fill="#e4f222" />
            </svg>
            <span className="text-base font-medium tracking-tight text-paper">SeekerStudio</span>
          </Link>
        </div>

        <div className="card">
          <h1 className="text-[17px] font-medium tracking-tight text-paper text-center mb-1">Create account</h1>
          <p className="text-sm text-fog text-center mb-6">Start building Solana Mobile apps</p>

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
                autoComplete="new-password"
                minLength={10}
                required
                className="field mt-2"
              />
              {/* The API enforces this. Saying so up front beats a rejected form. */}
              <span className="mt-2 block font-mono text-xs text-ash">At least 10 characters.</span>
            </label>

            {error && <p className="text-sm text-coral-red mb-4">{error}</p>}

            <PillButton type="submit" disabled={loading} className="w-full">
              {loading ? "Creating account…" : "Create account"}
            </PillButton>
          </form>
        </div>

        <p className="text-sm text-fog text-center mt-6">
          Already have an account?{" "}
          <Link href="/login" className="text-mist underline underline-offset-2 hover:text-paper transition-colors">
            Sign in
          </Link>
        </p>
      </div>
    </div>
  );
}
