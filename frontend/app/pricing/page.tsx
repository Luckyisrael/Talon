import type { Metadata } from "next";
import Link from "next/link";
import { TickCircle } from "iconsax-reactjs";
import { MarketingNav, MarketingFooter } from "@/components/marketing";
import ProCta from "./ProCta";

export const metadata: Metadata = {
  title: "Pricing",
  description:
    "Start free with 20 AI prompts and 60 minutes of live device time. No credit card required.",
};

// Every number here is the limit the backend actually enforces
// (backend/internal/api/usage.go). Pro has no checkout yet, so it does not
// claim a price or a working purchase button — it points at the waitlist.
const tiers = [
  {
    name: "Free",
    price: "$0",
    priceNote: "no credit card required",
    tagline: "Everything you need to ship your first build.",
    available: true,
    features: [
      "20 AI prompts per cycle",
      "60 minutes of live device time",
      "Full browser IDE — editor, terminal, files",
      "Live Seeker device preview",
      "Solana Mobile (MWA) scaffolding built in",
    ],
    cta: { label: "Start building", href: "/register" },
    primary: true,
  },
  {
    name: "Pro",
    price: "Early access",
    priceNote: "no charge in test mode",
    tagline: "Higher limits for heavier builds.",
    available: false,
    features: [
      "Everything in Free",
      "500 AI prompts per cycle",
      "25 hours of live device time",
    ],
    cta: { label: "Join the waitlist", href: "/waitlist" },
    primary: false,
  },
];

const faqs = [
  {
    q: "Do I need a credit card to start?",
    a: "No. The Free tier is open — create an account and start building. We never ask for a card up front.",
  },
  {
    q: "What counts as an AI prompt?",
    a: "Each message you send the agent that it acts on. Reading files, running builds, and streaming the preview do not count against your prompt quota.",
  },
  {
    q: "How is device time measured?",
    a: "Wall-clock minutes a virtual Seeker device is running for your project. Stopping the device stops the meter.",
  },
  {
    q: "When do quotas reset?",
    a: "Usage tracks against your current billing cycle and refreshes when a new cycle begins.",
  },
];

export default function PricingPage() {
  return (
    <div className="min-h-screen flex flex-col bg-ink">
      <MarketingNav />

      <main className="flex-1 px-6 pt-20 pb-16">
        <div className="text-center mb-14">
          <h1 className="text-4xl md:text-5xl font-semibold text-paper mb-4 tracking-tight">
            Simple, honest pricing
          </h1>
          <p className="text-body md:text-lead text-graphite max-w-xl mx-auto leading-relaxed">
            Start free with a real allowance — not a trial. Upgrade when your builds outgrow it.
          </p>
        </div>

        <div className="grid grid-cols-1 md:grid-cols-2 gap-5 max-w-3xl mx-auto">
          {tiers.map((tier) => (
            <div
              key={tier.name}
              className={`bay rounded-3 p-7 flex flex-col ${
                tier.primary ? "border-edge" : ""
              }`}
            >
              <div className="flex items-center justify-between mb-1">
                <h2 className="text-lead font-semibold text-paper">{tier.name}</h2>
                <span className="gauge">{tier.available ? "available now" : "coming soon"}</span>
              </div>

              <div className="mt-4 mb-1">
                <span className="text-title font-semibold text-paper">{tier.price}</span>
              </div>
              <p className="text-tiny text-dust font-mono mb-5">{tier.priceNote}</p>

              <p className="text-small text-graphite mb-6">{tier.tagline}</p>

              <ul className="space-y-3 mb-8">
                {tier.features.map((f) => (
                  <li key={f} className="flex items-start gap-2.5 text-small text-paper">
                    {/* Neutral, not signal-green: a green tick would read as
                        "device live" in this palette, which it isn't. */}
                    <TickCircle
                      size="16"
                      variant="Linear"
                      className="text-graphite mt-0.5 flex-shrink-0"
                    />
                    <span>{f}</span>
                  </li>
                ))}
              </ul>

              {/* Free is a static link; Pro's CTA depends on who's looking, so
                  it (and its footnote) live in a small client island. */}
              {tier.name === "Pro" ? (
                <ProCta />
              ) : (
                <Link
                  href={tier.cta.href}
                  className={`mt-auto h-10 px-5 rounded-1 text-small font-medium inline-flex items-center justify-center transition-colors ${
                    tier.primary
                      ? "bg-paper text-ink hover:bg-white"
                      : "border border-rail text-paper hover:border-edge hover:bg-riser"
                  }`}
                >
                  {tier.cta.label}
                </Link>
              )}
            </div>
          ))}
        </div>

        {/* FAQ */}
        <div className="max-w-2xl mx-auto mt-24">
          <h2 className="text-title font-semibold text-paper text-center mb-10">
            Questions
          </h2>
          <div className="space-y-4">
            {faqs.map((faq) => (
              <div key={faq.q} className="bay p-5">
                <h3 className="text-small font-semibold text-paper mb-1.5">{faq.q}</h3>
                <p className="text-small text-graphite leading-relaxed">{faq.a}</p>
              </div>
            ))}
          </div>
        </div>
      </main>

      <MarketingFooter />
    </div>
  );
}
