import Link from "next/link";
import { PillButton } from "@/components/ui";

export default function Hero() {
  return (
    <section className="flex flex-col items-center justify-center text-center px-4 py-20 md:py-32">
      {/* Badge */}
      <div className="pill tonal-surface px-4 py-1.5 text-xs font-mono text-on-surface-variant mb-8">
        Solana Mobile · Seeker Device
      </div>

      {/* Headline */}
      <h1 className="text-4xl md:text-6xl font-bold tracking-tight text-on-surface max-w-3xl mb-6">
        Build Solana Mobile Apps
        <br />
        <span className="solana-gradient text-transparent bg-clip-text">
          with AI
        </span>
      </h1>

      {/* Subheadline */}
      <p className="text-lg text-on-surface-variant max-w-xl mb-10">
        Describe your app. AI generates code, builds APK, and deploys to a live preview.
        Ship to the Seeker device in minutes.
      </p>

      {/* CTA */}
      <div className="flex flex-col sm:flex-row gap-4">
        <Link href="/register">
          <PillButton size="lg">
            Start Building
            <svg className="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor">
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M13 7l5 5m0 0l-5 5m5-5H6" />
            </svg>
          </PillButton>
        </Link>

        <Link href="/dashboard">
          <PillButton variant="secondary" size="lg">
            View Demo
          </PillButton>
        </Link>
      </div>
    </section>
  );
}
