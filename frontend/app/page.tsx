import Link from "next/link";
import {
  ArrowRight,
  Message,
  Mobile,
  DocumentUpload,
  ShieldSecurity,
  Flash,
  Code,
} from "iconsax-reactjs";
import { MarketingNav, MarketingFooter } from "@/components/marketing";

const features = [
  {
    icon: Message,
    title: "AI-powered generation",
    body: "Describe your app in plain English. The agent writes production-ready React Native with Solana Mobile SDK integration.",
  },
  {
    icon: Mobile,
    title: "Live device preview",
    body: "Watch your app run on a real Android target in real time. Every change streams straight to the virtual Seeker device.",
  },
  {
    icon: DocumentUpload,
    title: "One-click deploy",
    body: "Build and install your APK to the device. Test wallet connections, the MWA protocol, and native features instantly.",
  },
  {
    icon: ShieldSecurity,
    title: "Solana native",
    body: "MWA wallet integration, keypair management, and Solana RPC — everything a Seeker app needs, pre-configured.",
  },
  {
    icon: Flash,
    title: "Instant iteration",
    body: "Talk to the agent to refine the app. Fix bugs, add features, change designs — all through the conversation.",
  },
  {
    icon: Code,
    title: "Full IDE",
    body: "File tree, editor, terminal, build output — all in the browser. No VS Code, no Android Studio, no local toolchain.",
  },
];

const ecosystem = ["Solana Mobile", "Expo SDK 55", "React Native", "Mobile Wallet Adapter"];

export default function Home() {
  return (
    <div className="min-h-screen flex flex-col bg-void">
      <MarketingNav />

      <main className="flex-1 flex flex-col items-center">
        {/* ── Hero: left-aligned oversized headline + link CTA ── */}
        <section className="w-full max-w-[1200px] mx-auto px-6 pt-24 pb-16">
          <div className="pill mb-6">Seeker-native AI coding platform</div>
          <h1 className="t-heading-lg max-w-[880px]">
            Build Solana Mobile apps at the speed of thought
          </h1>
          <p className="t-body max-w-2xl mt-6 text-fog">
            Describe your app, preview and test it live on a real device, then
            deploy to Solana Seeker — all without leaving your browser.
          </p>
          <div className="flex items-center gap-6 mt-8">
            <Link href="/register" className="btn-go h-10 px-4 inline-flex items-center gap-2 rounded-md text-sm">
              Start building
              <ArrowRight size="15" variant="Linear" />
            </Link>
            <Link href="/docs" className="text-sm text-mist hover:text-paper transition-colors inline-flex items-center gap-1.5">
              How it works <ArrowRight size="14" variant="Linear" />
            </Link>
          </div>
        </section>

        {/* ── Product screenshot frame on the gradient floor ──
            IMAGE NEEDED: full-fidelity IDE screenshot (approx 1440×900) —
            agent chat left, Monaco code center, live phone preview right,
            on the void canvas. Real product UI, no illustration. */}
        <section className="w-full hero-floor pb-24">
          <div className="max-w-[1200px] mx-auto px-6">
            <div className="card">
              <div className="rounded-md bg-void border border-graphite aspect-[16/10] flex items-center justify-center">
                <div className="text-center">
                  <p className="t-label">IDE SCREENSHOT · 1440 × 900</p>
                  <p className="t-caption mt-2">chat · code · live device preview</p>
                </div>
              </div>
            </div>
          </div>
        </section>

        {/* ── Ecosystem strip ── */}
        <section className="w-full max-w-[1200px] mx-auto px-6 py-12">
          <div className="flex flex-wrap items-center justify-center gap-x-12 gap-y-4">
            {ecosystem.map((name) => (
              <span key={name} className="text-sm font-medium tracking-tight text-ash">
                {name}
              </span>
            ))}
          </div>
        </section>

        {/* ── Features: 2-column subtle cards ── */}
        <section className="w-full max-w-[1200px] mx-auto px-6 py-24">
          <h2 className="t-heading-sm max-w-xl">Everything you need to ship</h2>
          <p className="t-body-sm text-fog max-w-xl mt-4">
            From idea to installed app on a real Android device — no local
            setup required.
          </p>

          <div className="grid grid-cols-1 md:grid-cols-2 gap-2 mt-12">
            {features.map(({ icon: Icon, title, body }) => (
              <div key={title} className="card-subtle p-6!">
                <div className="w-9 h-9 rounded-md bg-white/5 border border-graphite flex items-center justify-center mb-4">
                  <Icon size="18" className="text-fog" variant="Linear" />
                </div>
                <h3 className="text-[15px] font-medium tracking-tight text-paper mb-1.5">{title}</h3>
                <p className="text-sm text-fog leading-relaxed">{body}</p>
              </div>
            ))}
          </div>
        </section>

        {/* ── CTA band ── */}
        <section className="w-full max-w-[1200px] mx-auto px-6 py-24 text-center">
          <h2 className="t-heading-sm">Start building today</h2>
          <p className="t-body-sm text-fog mt-4 max-w-lg mx-auto">
            The free tier includes 20 AI prompts and 60 minutes of device time.
            No credit card required.
          </p>
          <Link
            href="/register"
            className="btn-go h-10 px-4 inline-flex items-center gap-2 rounded-md text-sm mt-8"
          >
            Get started free
            <ArrowRight size="15" variant="Linear" />
          </Link>
        </section>
      </main>

      <MarketingFooter />
    </div>
  );
}
