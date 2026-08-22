import type { Metadata } from "next";
import Link from "next/link";
import { MarketingNav, MarketingFooter } from "@/components/marketing";

export const metadata: Metadata = {
  title: "Docs",
  description:
    "How SeekerStudio turns a prompt into a running Solana Mobile app — getting started, the Solana Mobile stack, and your quotas.",
};

// Section ids are the anchor targets the footer links to (#getting-started,
// #solana-mobile, #quotas). Keep them in sync with components/marketing.
const toc = [
  { id: "getting-started", label: "Getting started" },
  { id: "how-it-works", label: "How it works" },
  { id: "solana-mobile", label: "Solana Mobile" },
  { id: "quotas", label: "Quotas & limits" },
];

export default function DocsPage() {
  return (
    <div className="min-h-screen flex flex-col bg-ink">
      <MarketingNav />

      <main className="flex-1 w-full max-w-[1000px] mx-auto px-6 pt-16 pb-20">
        <header className="mb-14">
          <span className="gauge">documentation</span>
          <h1 className="text-4xl font-semibold text-paper mt-3 mb-3 tracking-tight">
            Build a Solana Mobile app from a prompt
          </h1>
          <p className="text-body text-graphite max-w-2xl leading-relaxed">
            SeekerStudio is a browser IDE with an agent that writes React Native, runs it on a
            live virtual Seeker device, and installs the build — no local Android toolchain.
          </p>
        </header>

        <div className="flex flex-col md:flex-row gap-12">
          {/* Section nav */}
          <nav className="md:w-48 md:flex-shrink-0">
            <div className="md:sticky md:top-8">
              <p className="gauge mb-3">On this page</p>
              <ul className="space-y-2">
                {toc.map((item) => (
                  <li key={item.id}>
                    <a
                      href={`#${item.id}`}
                      className="text-small text-graphite hover:text-paper transition-colors"
                    >
                      {item.label}
                    </a>
                  </li>
                ))}
              </ul>
            </div>
          </nav>

          {/* Content */}
          <div className="min-w-0 flex-1 space-y-16">
            <section id="getting-started" className="scroll-mt-8">
              <h2 className="text-title font-semibold text-paper mb-4">Getting started</h2>
              <ol className="space-y-4">
                {[
                  ["Create an account", "Sign up with an email and password. The free tier is open — no card, no waiting."],
                  ["Start a project", "From your dashboard, create a project. Each project is its own app, workspace, and device."],
                  ["Describe the app", "Tell the agent what you want in plain English. It proposes a plan — screens, features, packages — before it writes anything."],
                  ["Watch it build", "The agent writes files, installs dependencies, and runs the build. You see every decision, edit, and command as it happens."],
                  ["Preview on a device", "Your app runs on a virtual Seeker device in the browser. Interact with it live; every change streams straight to the screen."],
                ].map(([title, body], i) => (
                  <li key={title} className="flex gap-4">
                    <span className="font-mono text-small text-dust flex-shrink-0 tabular-nums pt-0.5">
                      {String(i + 1).padStart(2, "0")}
                    </span>
                    <div>
                      <h3 className="text-body font-semibold text-paper mb-1">{title}</h3>
                      <p className="text-small text-graphite leading-relaxed">{body}</p>
                    </div>
                  </li>
                ))}
              </ol>
            </section>

            <section id="how-it-works" className="scroll-mt-8">
              <h2 className="text-title font-semibold text-paper mb-4">How it works</h2>
              <p className="text-small text-graphite leading-relaxed mb-4">
                The workspace has three bays: the chat where you talk to the agent, the editor and
                file tree, and the live device. The agent works the way you would — reading the
                project, planning, editing files, running commands, and checking the result — and
                it narrates each step so you are never guessing what it did.
              </p>
              <p className="text-small text-graphite leading-relaxed">
                Everything runs server-side. There is no VS Code to install, no Android Studio, no
                emulator to configure on your machine. Open the tab and build.
              </p>
            </section>

            <section id="solana-mobile" className="scroll-mt-8">
              <h2 className="text-title font-semibold text-paper mb-4">Solana Mobile</h2>
              <p className="text-small text-graphite leading-relaxed mb-4">
                New projects are scaffolded for the Solana Mobile stack: the Mobile Wallet Adapter
                (MWA) for wallet connections, and Solana RPC wired and ready. Ask for a wallet
                screen or an on-chain action and the agent builds against these directly.
              </p>
              <div className="bay p-5">
                <h3 className="text-small font-semibold text-paper mb-2">On keys and security</h3>
                <p className="text-small text-graphite leading-relaxed">
                  SeekerStudio never asks for, stores, or generates a seed phrase or private key.
                  Wallet connections go through the Mobile Wallet Adapter, and the device&apos;s
                  Seed Vault or wallet app holds all key material. Your app requests a signature;
                  the wallet decides. Key custody never touches the builder.
                </p>
              </div>
            </section>

            <section id="quotas" className="scroll-mt-8">
              <h2 className="text-title font-semibold text-paper mb-4">Quotas &amp; limits</h2>
              <p className="text-small text-graphite leading-relaxed mb-6">
                Two things are metered: AI prompts and live device time. You can see both in your
                workspace at any moment.
              </p>
              <div className="overflow-x-auto">
                <table className="w-full text-small border-collapse">
                  <thead>
                    <tr className="border-b border-rail text-left">
                      <th className="py-2.5 pr-4 gauge font-normal">Allowance</th>
                      <th className="py-2.5 px-4 gauge font-normal">Free</th>
                      <th className="py-2.5 pl-4 gauge font-normal">Pro</th>
                    </tr>
                  </thead>
                  <tbody className="font-mono tabular-nums">
                    <tr className="border-b border-rail/60">
                      <td className="py-3 pr-4 text-graphite font-sans">AI prompts per cycle</td>
                      <td className="py-3 px-4 text-paper">20</td>
                      <td className="py-3 pl-4 text-paper">500</td>
                    </tr>
                    <tr>
                      <td className="py-3 pr-4 text-graphite font-sans">Live device time</td>
                      <td className="py-3 px-4 text-paper">60 min</td>
                      <td className="py-3 pl-4 text-paper">25 hrs</td>
                    </tr>
                  </tbody>
                </table>
              </div>
              <p className="text-small text-graphite leading-relaxed mt-6">
                Reading files, running builds, and streaming the preview don&apos;t count against
                your prompt quota — only messages the agent acts on do. See{" "}
                <Link href="/pricing" className="text-paper underline underline-offset-2 hover:text-white transition-colors">
                  pricing
                </Link>{" "}
                for the full breakdown.
              </p>
            </section>
          </div>
        </div>
      </main>

      <MarketingFooter />
    </div>
  );
}
