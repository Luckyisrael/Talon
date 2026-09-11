import type { Metadata } from "next";
import Link from "next/link";
import { ArrowRight, TickCircle } from "iconsax-reactjs";
import { MarketingNav, MarketingFooter, socials } from "@/components/marketing";

export const metadata: Metadata = {
  title: "Early access",
  description:
    "SeekerStudio is in early access. The Free tier is open now — no queue, no card. Pro raises the limits and opens gradually.",
};

// The honest core of this page: there is no waitlist submit endpoint, and the
// Free tier is already open. So the page never mimes a signup form it can't
// back. It sends builders to the open Free signup now, and — since Pro isn't
// self-serve yet — points at the real channels (X, Discord) where openings are
// announced first. Every follow link comes from the shared socials source.
const FOLLOW_LABELS: Record<string, string> = {
  Twitter: "Follow on X",
  Discord: "Join the Discord",
};
const followChannels = socials
  .filter((s) => s.label in FOLLOW_LABELS)
  .map((s) => ({ ...s, cta: FOLLOW_LABELS[s.label] }));

// Free-tier bullets are the limits the backend actually enforces
// (backend/internal/api/usage.go): 20 prompts, 60 device-minutes.
const freePoints = [
  "20 AI prompts, 60 minutes of live device time",
  "The full browser IDE — editor, terminal, files",
  "Solana Mobile (MWA) scaffolding built in",
  "No credit card, no queue",
];

// What early builders are actually building with. Every claim maps to something
// that exists in the build — the same pillars the product is pitched on.
const pillars = [
  {
    title: "Native to Seeker",
    body: "A real React Native app for the device — Mobile Wallet Adapter and Seed Vault, not a website in a wrapper.",
  },
  {
    title: "Secure by construction",
    body: "It never asks for or touches a seed phrase or private key. The guardrail blocks it in code, not in a prompt.",
  },
  {
    title: "An agent, not autocomplete",
    body: "It plans, builds, verifies, and fixes its own errors — and the whole trace is visible while it works.",
  },
  {
    title: "It won't ship broken code",
    body: "Every build is typechecked and linted. If it fails, the agent repairs itself and re-runs before it reaches you.",
  },
];

export default function WaitlistPage() {
  return (
    <div className="min-h-screen flex flex-col bg-void">
      <MarketingNav />

      <main className="flex-1 px-6 pt-20 pb-16">
        {/* Hero — the thesis: the waitlist is only for Pro, and you don't need
            it to start, because Free is open today. */}
        <div className="text-center max-w-2xl mx-auto mb-14">
          <span className="badge">EARLY ACCESS</span>
          <h1 className="t-heading-sm mt-4 mb-4 text-balance">
            The waitlist is for Pro. Building is open now.
          </h1>
          <p className="t-body-sm text-fog">
            SeekerStudio is in early access. The Free tier is live today — a real allowance, no
            card, no queue. Pro raises the limits, and we&apos;re opening it gradually.
          </p>
          <div className="mt-8">
            <Link
              href="/login"
              className="btn-go h-11 px-6 rounded-md text-sm inline-flex items-center gap-2"
            >
              Start building free
              <ArrowRight size="16" variant="Linear" />
            </Link>
            <p className="mt-3 font-mono text-xs text-ash">
              20 prompts · 60 minutes device time · free
            </p>
          </div>
        </div>

        {/* Two ways in: start now on Free, or follow to catch Pro when it
            opens. Both are real — no dead form in sight. */}
        <div className="grid grid-cols-1 md:grid-cols-2 gap-2 max-w-3xl mx-auto">
          <div className="card flex flex-col">
            <div className="flex items-center justify-between mb-4">
              <h2 className="text-[17px] font-medium tracking-tight text-paper">Start now</h2>
              <span className="badge">FREE · OPEN</span>
            </div>
            <ul className="space-y-3 mb-8">
              {freePoints.map((p) => (
                <li key={p} className="flex items-start gap-2.5 text-sm text-mist">
                  <TickCircle
                    size="16"
                    variant="Linear"
                    className="text-fog mt-0.5 flex-shrink-0"
                  />
                  <span>{p}</span>
                </li>
              ))}
            </ul>
            <Link
              href="/login"
              className="btn-go mt-auto h-10 px-5 rounded-md text-sm inline-flex items-center justify-center gap-2"
            >
              Create your account
              <ArrowRight size="14" variant="Linear" />
            </Link>
          </div>

          <div className="card flex flex-col">
            <div className="flex items-center justify-between mb-4">
              <h2 className="text-[17px] font-medium tracking-tight text-paper">Get Pro early</h2>
              <span className="badge">COMING SOON</span>
            </div>
            <p className="text-sm text-fog leading-relaxed mb-2">
              Pro lifts the limits to 500 prompts and 25 hours of device time. It isn&apos;t
              self-serve yet — there&apos;s no form to fill in.
            </p>
            <p className="text-sm text-fog leading-relaxed mb-6">
              We&apos;re building in public and announce openings on X and in Discord first.
              Follow along and you&apos;ll be first to know.
            </p>
            <div className="mt-auto space-y-2.5">
              {followChannels.map((c) => (
                <Link
                  key={c.label}
                  href={c.href}
                  target="_blank"
                  rel="noopener noreferrer"
                  className="btn h-10 px-4 rounded-md text-sm flex items-center justify-between"
                >
                  <span className="flex items-center gap-2.5">
                    <svg
                      className="w-[16px] h-[16px]"
                      fill="currentColor"
                      viewBox="0 0 24 24"
                      aria-hidden="true"
                    >
                      <path d={c.path} />
                    </svg>
                    {c.cta}
                  </span>
                  <ArrowRight size="14" variant="Linear" className="text-ash" />
                </Link>
              ))}
            </div>
          </div>
        </div>

        {/* What early builders are actually getting — the honest pillars. */}
        <div className="max-w-3xl mx-auto mt-24">
          <h2 className="t-heading-sm text-center mb-10">
            What you&apos;re building with
          </h2>
          <div className="grid grid-cols-1 md:grid-cols-2 gap-2">
            {pillars.map((p) => (
              <div key={p.title} className="card-subtle p-6!">
                <h3 className="text-[15px] font-medium text-paper mb-2">{p.title}</h3>
                <p className="text-sm text-fog leading-relaxed">{p.body}</p>
              </div>
            ))}
          </div>
        </div>
      </main>

      <MarketingFooter />
    </div>
  );
}
