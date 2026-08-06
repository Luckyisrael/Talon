"use client";

import { useState } from "react";
import { useRouter } from "next/navigation";
import { ArrowRight } from "iconsax-reactjs";
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
      <h1 className="text-4xl md:text-6xl font-bold tracking-tight text-text-primary max-w-3xl mb-4">
        Build Solana Mobile Apps
      </h1>
      <p className="text-lg text-text-secondary max-w-xl mb-10">
        with AI
      </p>

      <form onSubmit={handleSubmit} className="w-full max-w-2xl mb-8">
        <div className="relative">
          <input
            type="text"
            value={prompt}
            onChange={(e) => setPrompt(e.target.value)}
            placeholder="Describe the app you want to build..."
            className="w-full h-14 px-6 pr-32 rounded-2xl bg-surface-3 text-text-primary placeholder:text-text-muted focus:outline-none focus:ring-2 focus:ring-accent/50 transition-all text-base"
          />
          <div className="absolute right-2 top-1/2 -translate-y-1/2 flex items-center gap-2">
            <PillButton type="submit" size="md" disabled={!prompt.trim()}>
              <ArrowRight size="16" variant="Linear" />
            </PillButton>
          </div>
        </div>
      </form>

      <div className="flex flex-wrap justify-center gap-2 max-w-2xl">
        {suggestions.map((s) => (
          <button
            key={s}
            onClick={() => {
              localStorage.setItem("pendingPrompt", s);
              router.push("/register");
            }}
            className="h-9 px-4 rounded-full text-sm text-text-secondary bg-surface-3 hover:bg-surface-4 transition-colors"
          >
            {s}
          </button>
        ))}
      </div>
    </section>
  );
}
