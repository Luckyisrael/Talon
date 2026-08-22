import Link from "next/link";
import {
  ArrowRight,
  Message,
  Mobile,
  DocumentUpload,
  ShieldSecurity,
  Flash,
  Code,
  Box,
  Game,
  Solana,
} from "iconsax-reactjs";
import { MarketingNav, MarketingFooter } from "@/components/marketing";

const suggestions = [
  { icon: Game, label: "Create a multiplayer game" },
  { icon: Box, label: "Create a 3D game" },
  { icon: Solana, label: "Solana wallet app" },
  { icon: Flash, label: "GitHub to mobile app" },
];

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

export default function Home() {
  return (
    <div className="min-h-screen flex flex-col bg-ink">
      <MarketingNav />

      <main className="flex-1 flex flex-col items-center px-6 pt-20 pb-16">
        <h1 className="text-5xl md:text-7xl font-semibold text-paper text-center mb-5 tracking-tight">
          Build Solana Mobile apps
        </h1>

        <p className="text-body md:text-lead text-graphite text-center mb-10 max-w-2xl leading-relaxed">
          Describe your app, preview and test it live on a real device, then deploy to Solana
          Seeker — all without leaving your browser.
        </p>

        {/* The console. It isn't a working input on this page — so instead of
            miming one with dead controls, it says what it is and takes you to
            where you can actually type. */}
        <Link
          href="/register"
          className="group block w-full max-w-[900px] bay rounded-3 p-5 mb-8 hover:border-edge transition-colors"
        >
          <div className="flex items-center gap-2 mb-4">
            <span className="gauge">new build</span>
          </div>
          <p className="text-lead text-graphite">Describe the app you want to build…</p>
          <div className="flex items-center justify-between mt-6">
            <span className="text-tiny text-dust font-mono">react native · solana mobile</span>
            <span className="h-9 px-4 rounded-1 bg-paper text-ink text-small font-medium inline-flex items-center gap-2 group-hover:bg-white transition-colors">
              Start building
              <ArrowRight size="14" variant="Linear" />
            </span>
          </div>
        </Link>

        {/* Suggestion chips — each starts a build, so each is a real link. */}
        <div className="flex flex-wrap justify-center gap-2.5 mb-24">
          {suggestions.map(({ icon: Icon, label }) => (
            <Link
              key={label}
              href="/register"
              className="h-10 px-4 rounded-1 bg-riser border border-rail flex items-center gap-2.5 text-small text-graphite hover:text-paper hover:border-edge transition-colors"
            >
              <Icon size="15" variant="Linear" className="text-dust" />
              {label}
            </Link>
          ))}
        </div>

        {/* Features */}
        <div className="w-full max-w-[1120px] mx-auto">
          <div className="text-center mb-14">
            <h2 className="text-title md:text-4xl font-semibold text-paper mb-3">
              Everything you need to ship
            </h2>
            <p className="text-body text-graphite max-w-xl mx-auto">
              From idea to installed app on a real Android device — no local setup required.
            </p>
          </div>

          <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
            {features.map(({ icon: Icon, title, body }) => (
              <div key={title} className="bay p-6">
                <div className="w-10 h-10 rounded-1 bg-riser border border-rail flex items-center justify-center mb-4">
                  <Icon size="20" className="text-graphite" variant="Linear" />
                </div>
                <h3 className="text-body font-semibold text-paper mb-2">{title}</h3>
                <p className="text-small text-graphite leading-relaxed">{body}</p>
              </div>
            ))}
          </div>
        </div>

        {/* CTA */}
        <div className="w-full max-w-[800px] mx-auto mt-24 text-center">
          <h2 className="text-title md:text-4xl font-semibold text-paper mb-3">
            Start building today
          </h2>
          <p className="text-body text-graphite mb-8 max-w-lg mx-auto">
            The free tier includes 20 AI prompts and 60 minutes of device time. No credit card
            required.
          </p>
          <Link
            href="/register"
            className="h-11 px-6 rounded-1 bg-paper text-ink text-body font-medium inline-flex items-center gap-2 hover:bg-white transition-colors"
          >
            Get started free
            <ArrowRight size="16" variant="Linear" />
          </Link>
        </div>
      </main>

      <MarketingFooter />
    </div>
  );
}
