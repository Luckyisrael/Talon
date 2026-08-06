"use client";

import { useState } from "react";
import { useRouter } from "next/navigation";
import PillButton from "@/components/ui/PillButton";

const suggestions = [
  "Create a Multiplayer Game",
  "Build a Wallet App",
  "Make a Social Feed",
  "Design a Fitness Tracker",
];

export default function Hero() {
  const router = useRouter();
  const [prompt, setPrompt] = useState("");

  function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    if (prompt.trim()) {
      localStorage.setItem("pendingPrompt", prompt.trim());
      router.push("/register");
    }
  }

  return (
    <section className="flex flex-col items-center justify-center text-center px-4 py-16 md:py-24">
      {/* Headline */}
      <h1 className="text-4xl md:text-6xl font-bold tracking-tight text-on-surface max-w-3xl mb-4">
        Build Solana Mobile Apps
      </h1>
      <p className="text-lg text-on-surface-variant max-w-xl mb-10">
        with AI
      </p>

      {/* AI Chat Input — Rork pattern */}
      <form onSubmit={handleSubmit} className="w-full max-w-2xl mb-8">
        <div className="relative">
          <input
            type="text"
            value={prompt}
            onChange={(e) => setPrompt(e.target.value)}
            placeholder="Describe the app you want to build..."
            className="w-full h-14 px-6 pr-32 rounded-2xl bg-surface-container-high border border-outline-variant text-on-surface placeholder:text-on-surface-variant/60 focus:outline-none focus:border-primary transition-colors text-base"
          />
          <div className="absolute right-2 top-1/2 -translate-y-1/2 flex items-center gap-2">
            <PillButton type="submit" size="md" disabled={!prompt.trim()}>
              <svg className="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M13 7l5 5m0 0l-5 5m5-5H6" />
              </svg>
            </PillButton>
          </div>
        </div>
      </form>

      {/* Suggestion chips */}
      <div className="flex flex-wrap justify-center gap-2 max-w-2xl">
        {suggestions.map((s) => (
          <button
            key={s}
            onClick={() => {
              localStorage.setItem("pendingPrompt", s);
              router.push("/register");
            }}
            className="pill h-9 px-4 text-sm text-on-surface-variant bg-surface-container hover:bg-surface-container-high transition-colors border border-outline-variant"
          >
            {s}
          </button>
        ))}
      </div>
    </section>
  );
}
