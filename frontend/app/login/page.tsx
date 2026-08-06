"use client";

import { useState } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import PillButton from "@/components/ui/PillButton";
import Spinner from "@/components/ui/Spinner";

export default function LoginPage() {
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
      const res = await fetch("http://localhost:8080/v1/auth/login", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ email, password }),
      });

      if (!res.ok) {
        const data = await res.json();
        throw new Error(data.error || "Login failed");
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
    <div className="min-h-screen flex items-center justify-center px-4 bg-surface">
      <div className="w-full max-w-sm">
        {/* Logo */}
        <div className="flex justify-center mb-8">
          <Link href="/" className="flex items-center gap-2">
            <div className="w-10 h-10 rounded-xl solana-gradient flex items-center justify-center">
              <span className="text-white font-bold">S</span>
            </div>
          </Link>
        </div>

        {/* Card */}
        <div className="bg-surface-container rounded-2xl p-6">
          <h1 className="text-xl font-bold text-on-surface text-center mb-1">Welcome back</h1>
          <p className="text-sm text-on-surface-variant text-center mb-6">Sign in to your account</p>

          <form onSubmit={handleSubmit}>
            <label className="block mb-4">
              <span className="text-xs font-mono uppercase tracking-wider text-on-surface-variant">
                Email
              </span>
              <input
                type="email"
                value={email}
                onChange={(e) => setEmail(e.target.value)}
                placeholder="you@example.com"
                className="mt-2 w-full h-11 px-4 rounded-xl bg-surface border border-outline-variant text-on-surface placeholder:text-on-surface-variant/50 focus:outline-none focus:border-primary transition-colors"
                autoFocus
              />
            </label>

            <label className="block mb-4">
              <span className="text-xs font-mono uppercase tracking-wider text-on-surface-variant">
                Password
              </span>
              <input
                type="password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                placeholder="••••••••"
                className="mt-2 w-full h-11 px-4 rounded-xl bg-surface border border-outline-variant text-on-surface placeholder:text-on-surface-variant/50 focus:outline-none focus:border-primary transition-colors"
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
              {loading ? <Spinner size="sm" color="currentColor" /> : "Sign In"}
            </PillButton>
          </form>
        </div>

        <p className="text-sm text-on-surface-variant text-center mt-6">
          Don&apos;t have an account?{" "}
          <Link href="/register" className="text-primary hover:text-primary-dark transition-colors">
            Sign up
          </Link>
        </p>
      </div>
    </div>
  );
}
