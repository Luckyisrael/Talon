"use client";

import { useState } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import PillButton from "@/components/ui/PillButton";
import Spinner from "@/components/ui/Spinner";

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
      const res = await fetch("http://localhost:8080/v1/auth/register", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ email, password }),
      });

      if (!res.ok) {
        const data = await res.json();
        throw new Error(data.error || "Registration failed");
      }

      const data = await res.json();
      localStorage.setItem("token", data.token);
      router.push("/dashboard");
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
          <h1 className="text-xl font-bold text-text-primary text-center mb-1">Create account</h1>
          <p className="text-sm text-text-secondary text-center mb-6">Start building Solana Mobile apps</p>

          <form onSubmit={handleSubmit}>
            <label className="block mb-4">
              <span className="text-xs font-mono uppercase tracking-wider text-text-secondary">
                Email
              </span>
              <input
                type="email"
                value={email}
                onChange={(e) => setEmail(e.target.value)}
                placeholder="you@example.com"
                className="mt-2 w-full h-11 px-4 rounded-xl bg-surface-2 text-text-primary placeholder:text-text-muted focus:outline-none focus:ring-2 focus:ring-accent/50 transition-all"
                autoFocus
              />
            </label>

            <label className="block mb-4">
              <span className="text-xs font-mono uppercase tracking-wider text-text-secondary">
                Password
              </span>
              <input
                type="password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                placeholder="••••••••"
                className="mt-2 w-full h-11 px-4 rounded-xl bg-surface-2 text-text-primary placeholder:text-text-muted focus:outline-none focus:ring-2 focus:ring-accent/50 transition-all"
              />
            </label>

            {error && (
              <p className="text-sm text-error mb-4">{error}</p>
            )}

            <PillButton
              type="submit"
              disabled={loading}
              className="w-full"
            >
              {loading ? <Spinner size="sm" color="currentColor" /> : "Create Account"}
            </PillButton>
          </form>
        </div>

        <p className="text-sm text-text-secondary text-center mt-6">
          Already have an account?{" "}
          <Link href="/login" className="text-accent hover:text-accent-hover transition-colors">
            Sign in
          </Link>
        </p>
      </div>
    </div>
  );
}
