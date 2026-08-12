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

const socials = [
  {
    href: "https://twitter.com/seekersstudio",
    label: "Twitter",
    path: "M18.244 2.25h3.308l-7.227 8.26 8.502 11.24H16.17l-5.214-6.817L4.99 21.75H1.68l7.73-8.835L1.254 2.25H8.08l4.713 6.231zm-1.161 17.52h1.833L7.084 4.126H5.117z",
  },
  {
    href: "https://youtube.com/@seekersstudio",
    label: "YouTube",
    path: "M23.498 6.186a3.016 3.016 0 0 0-2.122-2.136C19.505 3.545 12 3.545 12 3.545s-7.505 0-9.377.505A3.017 3.017 0 0 0 .502 6.186C0 8.07 0 12 0 12s0 3.93.502 5.814a3.016 3.016 0 0 0 2.122 2.136c1.871.505 9.376.505 9.376.505s7.505 0 9.377-.505a3.015 3.015 0 0 0 2.122-2.136C24 15.93 24 12 24 12s0-3.93-.502-5.814zM9.545 15.568V8.432L15.818 12l-6.273 3.568z",
  },
  {
    href: "https://discord.gg/seekersstudio",
    label: "Discord",
    path: "M20.317 4.3698a19.7913 19.7913 0 00-4.8851-1.5152.0741.0741 0 00-.0785.0371c-.211.3753-.4447.8648-.6083 1.2495-1.8447-.2762-3.68-.2762-5.4868 0-.1636-.3933-.4058-.8742-.6177-1.2495a.077.077 0 00-.0785-.037 19.7363 19.7363 0 00-4.8852 1.515.0699.0699 0 00-.0321.0277C.5334 9.0458-.319 13.5799.0992 18.0578a.0824.0824 0 00.0312.0561c2.0528 1.5076 4.0413 2.4228 5.9929 3.0294a.0777.0777 0 00.0842-.0276c.4616-.6304.8731-1.2952 1.226-1.9942a.076.076 0 00-.0416-.1057c-.6528-.2476-1.2743-.5495-1.8722-.8923a.077.077 0 01-.0076-.1277c.1258-.0943.2517-.1923.3718-.2914a.0743.0743 0 01.0776-.0105c3.9278 1.7933 8.18 1.7933 12.0614 0a.0739.0739 0 01.0785.0095c.1202.099.246.1981.3728.2924a.077.077 0 01-.0066.1276 12.2986 12.2986 0 01-1.873.8914.0766.0766 0 00-.0407.1067c.3604.698.7719 1.3628 1.225 1.9932a.076.076 0 00.0842.0286c1.961-.6067 3.9495-1.5219 6.0023-3.0294a.077.077 0 00.0313-.0552c.5004-5.177-.8382-9.6739-3.5485-13.6604a.061.061 0 00-.0312-.0286zM8.02 15.3312c-1.1825 0-2.1569-1.0857-2.1569-2.419 0-1.3332.9555-2.4189 2.157-2.4189 1.2108 0 2.1757 1.0952 2.1568 2.419 0 1.3332-.9555 2.4189-2.1569 2.4189zm7.9748 0c-1.1825 0-2.1569-1.0857-2.1569-2.419 0-1.3332.9554-2.4189 2.1569-2.4189 1.2108 0 2.1757 1.0952 2.1568 2.419 0 1.3332-.946 2.4189-2.1568 2.4189z",
  },
  {
    href: "https://github.com/seekersstudio",
    label: "GitHub",
    path: "M12 0c-6.626 0-12 5.373-12 12 0 5.302 3.438 9.8 8.207 11.387.599.111.793-.261.793-.577v-2.234c-3.338.726-4.033-1.416-4.033-1.416-.546-1.387-1.333-1.756-1.333-1.756-1.089-.745.083-.729.083-.729 1.205.084 1.839 1.237 1.839 1.237 1.07 1.834 2.807 1.304 3.492.997.107-.775.418-1.305.762-1.604-2.665-.305-5.467-1.334-5.467-5.931 0-1.311.469-2.381 1.236-3.221-.124-.303-.535-1.524.117-3.176 0 0 1.008-.322 3.301 1.23.957-.266 1.983-.399 3.003-.404 1.02.005 2.047.138 3.006.404 2.291-1.552 3.297-1.23 3.297-1.23.653 1.653.242 2.874.118 3.176.77.84 1.235 1.911 1.235 3.221 0 4.609-2.807 5.624-5.479 5.921.43.372.823 1.102.823 2.222v3.293c0 .319.192.694.801.576 4.765-1.589 8.199-6.086 8.199-11.386 0-6.627-5.373-12-12-12z",
  },
];

export default function Home() {
  return (
    <div className="min-h-screen flex flex-col bg-ink">
      {/* Navbar */}
      <nav className="h-16 flex items-center justify-between px-6 md:px-12 max-w-[1500px] mx-auto w-full border-b border-rail">
        <Link href="/" className="flex items-center gap-2.5">
          <span className="inline-block w-2.5 h-2.5 rounded-[1px] bg-paper" />
          <span className="text-lead font-semibold text-paper">SeekerStudio</span>
        </Link>

        <div className="hidden md:flex items-center gap-8">
          <Link href="/docs" className="text-small text-graphite hover:text-paper transition-colors">
            Resources
          </Link>
          <Link href="/pricing" className="text-small text-graphite hover:text-paper transition-colors">
            Pricing
          </Link>
          <Link href="/dashboard" className="text-small text-paper transition-colors">
            App
          </Link>
        </div>

        <div className="flex items-center gap-5">
          {socials.map((s) => (
            <Link
              key={s.label}
              href={s.href}
              aria-label={s.label}
              className="text-dust hover:text-paper transition-colors"
              target="_blank"
              rel="noopener noreferrer"
            >
              <svg className="w-[18px] h-[18px]" fill="currentColor" viewBox="0 0 24 24">
                <path d={s.path} />
              </svg>
            </Link>
          ))}

          <Link
            href="/login"
            className="h-9 px-4 rounded-1 bg-paper text-ink text-small font-medium inline-flex items-center hover:bg-white transition-colors"
          >
            Sign in
          </Link>
        </div>
      </nav>

      {/* Hero */}
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

      {/* Footer */}
      <footer className="py-12 px-6 md:px-12 border-t border-rail">
        <div className="max-w-[1120px] mx-auto">
          <div className="grid grid-cols-2 md:grid-cols-4 gap-8 mb-12">
            <div>
              <div className="flex items-center gap-2.5 mb-4">
                <span className="inline-block w-2 h-2 rounded-[1px] bg-paper" />
                <span className="text-small font-semibold text-paper">SeekerStudio</span>
              </div>
              <p className="text-small text-graphite leading-relaxed">
                Build Solana Mobile apps with AI. From idea to device in minutes.
              </p>
            </div>

            <div>
              <h4 className="gauge mb-4">Product</h4>
              <ul className="space-y-2">
                <li><Link href="/pricing" className="text-small text-graphite hover:text-paper transition-colors">Pricing</Link></li>
                <li><Link href="/dashboard" className="text-small text-graphite hover:text-paper transition-colors">App</Link></li>
                <li><Link href="/docs" className="text-small text-graphite hover:text-paper transition-colors">Documentation</Link></li>
              </ul>
            </div>

            <div>
              <h4 className="gauge mb-4">Resources</h4>
              <ul className="space-y-2">
                <li><Link href="/docs/getting-started" className="text-small text-graphite hover:text-paper transition-colors">Getting started</Link></li>
                <li><Link href="/docs/solana-mobile" className="text-small text-graphite hover:text-paper transition-colors">Solana Mobile guide</Link></li>
                <li><Link href="/docs/api" className="text-small text-graphite hover:text-paper transition-colors">API reference</Link></li>
              </ul>
            </div>

            <div>
              <h4 className="gauge mb-4">Community</h4>
              <ul className="space-y-2">
                <li><Link href="https://discord.gg/seekersstudio" className="text-small text-graphite hover:text-paper transition-colors" target="_blank" rel="noopener noreferrer">Discord</Link></li>
                <li><Link href="https://twitter.com/seekersstudio" className="text-small text-graphite hover:text-paper transition-colors" target="_blank" rel="noopener noreferrer">Twitter</Link></li>
                <li><Link href="https://github.com/seekersstudio" className="text-small text-graphite hover:text-paper transition-colors" target="_blank" rel="noopener noreferrer">GitHub</Link></li>
              </ul>
            </div>
          </div>

          <div className="pt-8 border-t border-rail flex flex-col md:flex-row items-center justify-between gap-4">
            <p className="text-tiny text-dust font-mono">© 2026 SeekerStudio. All rights reserved.</p>
            <div className="flex items-center gap-6">
              <Link href="/privacy" className="text-tiny text-dust hover:text-graphite transition-colors">Privacy</Link>
              <Link href="/terms" className="text-tiny text-dust hover:text-graphite transition-colors">Terms</Link>
            </div>
          </div>
        </div>
      </footer>
    </div>
  );
}
