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
    cta: { label: "Start building", href: "/login" },
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
    <div className="min-h-screen flex flex-col bg-void">
      <MarketingNav />

      <main className="flex-1 px-6 pt-20 pb-16">
        <div className="text-center mb-14">
          <h1 className="t-heading-sm mb-4">
            Simple, honest pricing
          </h1>
          <p className="t-body-sm text-fog max-w-xl mx-auto">
            Start free with a real allowance — not a trial. Upgrade when your builds outgrow it.
          </p>
        </div>

        <div className="grid grid-cols-1 md:grid-cols-2 gap-2 max-w-3xl mx-auto">
          {tiers.map((tier) => (
            <div
              key={tier.name}
              className="card flex flex-col"
            >
              <div className="flex items-center justify-between mb-1">
                <h2 className="text-[17px] font-medium tracking-tight text-paper">{tier.name}</h2>
                <span className="badge">{tier.available ? "available now" : "coming soon"}</span>
              </div>

              <div className="mt-4 mb-1">
                <span className="text-[32px] font-normal leading-none tracking-tight text-paper">{tier.price}</span>
              </div>
              <p className="font-mono text-xs text-ash mb-5">{tier.priceNote}</p>

              <p className="text-sm text-fog mb-6">{tier.tagline}</p>

              <ul className="space-y-3 mb-8">
                {tier.features.map((f) => (
                  <li key={f} className="flex items-start gap-2.5 text-sm text-mist">
                    <TickCircle
                      size="16"
                      variant="Linear"
                      className="text-fog mt-0.5 flex-shrink-0"
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
                  className="btn-go mt-auto h-10 px-5 rounded-md text-sm inline-flex items-center justify-center"
                >
                  {tier.cta.label}
                </Link>
              )}
            </div>
          ))}
        </div>

        {/* FAQ */}
        <div className="max-w-2xl mx-auto mt-24">
          <h2 className="t-heading-sm text-center mb-10">
            Questions
          </h2>
          <div className="space-y-2">
            {faqs.map((faq) => (
              <div key={faq.q} className="card-subtle p-5!">
                <h3 className="text-sm font-medium text-paper mb-1.5">{faq.q}</h3>
                <p className="text-sm text-fog leading-relaxed">{faq.a}</p>
              </div>
            ))}
          </div>
        </div>
      </main>

      <MarketingFooter />
    </div>
  );
}
