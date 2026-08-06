import Link from "next/link";
import { Hero, Features, PhoneMockup, Footer } from "@/components/landing";

export default function Home() {
  return (
    <div className="min-h-screen flex flex-col bg-surface">
      {/* Nav */}
      <nav className="h-14 flex items-center justify-between px-4 hairline-bottom">
        <Link href="/" className="flex items-center gap-2">
          <div className="w-7 h-7 rounded-lg solana-gradient flex items-center justify-center">
            <span className="text-white font-bold text-xs">S</span>
          </div>
          <span className="font-semibold text-sm">SeekerStudio</span>
        </Link>

        <div className="flex items-center gap-3">
          <Link href="/login" className="text-sm text-on-surface-variant hover:text-on-surface transition-colors">
            Sign in
          </Link>
          <Link href="/register">
            <button className="pill h-9 px-4 text-sm font-medium bg-primary text-on-primary hover:bg-primary-dark transition-colors">
              Get Started
            </button>
          </Link>
        </div>
      </nav>

      {/* Main */}
      <main className="flex-1">
        <Hero />
        <Features />
        <PhoneMockup />
      </main>

      {/* Footer */}
      <Footer />
    </div>
  );
}
