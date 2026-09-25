import type { Metadata } from "next";
import Link from "next/link";
import { MarketingNav, MarketingFooter } from "@/components/marketing";

export const metadata: Metadata = {
  title: "Docs",
  description:
    "How Talon turns a prompt into a running Solana Mobile app — getting started, the Solana Mobile stack, and your quotas.",
};

// Section ids are the anchor targets the footer links to (#getting-started,
// #solana-mobile, #quotas). Keep them in sync with components/marketing.
const toc = [
  { id: "getting-started", label: "Getting started" },
  { id: "how-it-works", label: "How it works" },
  { id: "solana-mobile", label: "Solana Mobile" },
  { id: "plugins", label: "Plugins" },
  { id: "quotas", label: "Quotas & limits" },
];

export default function DocsPage() {
  return (
    <div className="min-h-screen flex flex-col bg-void">
      <MarketingNav />

      <main className="flex-1 w-full max-w-[1000px] mx-auto px-6 pt-16 pb-20">
        <header className="mb-14">
          <span className="badge">DOCUMENTATION</span>
          <h1 className="t-heading-sm mt-4 mb-3">
            Build a Solana Mobile app from a prompt
          </h1>
          <p className="t-body-sm text-fog max-w-2xl">
            Talon is a browser IDE with an agent that writes React Native, runs it on a
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
                      className="text-[13px] text-fog hover:text-paper transition-colors"
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
              <h2 className="text-[22px] font-medium tracking-tight text-paper mb-4">Getting started</h2>
              <ol className="space-y-4">
                {[
                  ["Create an account", "Sign up with an email and password. The free tier is open — no card, no waiting."],
                  ["Start a project", "From your dashboard, create a project. Each project is its own app, workspace, and device."],
                  ["Describe the app", "Tell the agent what you want in plain English. It proposes a plan — screens, features, packages — before it writes anything."],
                  ["Watch it build", "The agent writes files, installs dependencies, and runs the build. You see every decision, edit, and command as it happens."],
                  ["Preview on a device", "Your app runs on a virtual Seeker device in the browser. Interact with it live; every change streams straight to the screen."],
                ].map(([title, body], i) => (
                  <li key={title} className="flex gap-4">
                    <span className="font-mono text-sm text-ash flex-shrink-0 tabular-nums pt-0.5">
                      {String(i + 1).padStart(2, "0")}
                    </span>
                    <div>
                      <h3 className="text-[15px] font-medium text-paper mb-1">{title}</h3>
                      <p className="text-sm text-fog leading-relaxed">{body}</p>
                    </div>
                  </li>
                ))}
              </ol>
            </section>

            <section id="how-it-works" className="scroll-mt-8">
              <h2 className="text-[22px] font-medium tracking-tight text-paper mb-4">How it works</h2>
              <p className="text-sm text-fog leading-relaxed mb-4">
                The workspace has three bays: the chat where you talk to the agent, the editor and
                file tree, and the live device. The agent works the way you would — reading the
                project, planning, editing files, running commands, and checking the result — and
                it narrates each step so you are never guessing what it did.
              </p>
              <p className="text-sm text-fog leading-relaxed">
                Everything runs server-side. There is no VS Code to install, no Android Studio, no
                emulator to configure on your machine. Open the tab and build.
              </p>
            </section>

            <section id="solana-mobile" className="scroll-mt-8">
              <h2 className="text-[22px] font-medium tracking-tight text-paper mb-4">Solana Mobile</h2>
              <p className="text-sm text-fog leading-relaxed mb-4">
                New projects are scaffolded for the Solana Mobile stack: the Mobile Wallet Adapter
                (MWA) for wallet connections, and Solana RPC wired and ready. Ask for a wallet
                screen or an on-chain action and the agent builds against these directly.
              </p>
              <div className="card-subtle p-5!">
                <h3 className="text-sm font-medium text-paper mb-2">On keys and security</h3>
                <p className="text-sm text-fog leading-relaxed">
                  Talon never asks for, stores, or generates a seed phrase or private key.
                  Wallet connections go through the Mobile Wallet Adapter, and the device&apos;s
                  Seed Vault or wallet app holds all key material. Your app requests a signature;
                  the wallet decides. Key custody never touches the builder.
                </p>
              </div>
            </section>

            <section id="plugins" className="scroll-mt-8">
              <h2 className="text-[22px] font-medium tracking-tight text-paper mb-4">Plugins</h2>
              <p className="text-sm text-fog leading-relaxed mb-4">
                Plugins are public repos that teach the agent a specialty: premade
                web3 components, a new Solana API, a design pack. The agent fetches
                a plugin when your task touches its area and adapts its patterns to
                your screens. Nothing installs itself and nothing runs unseen.
              </p>
              <p className="mb-4">
                <Link
                  href="/plugins"
                  className="text-sm text-mist underline underline-offset-2 hover:text-paper transition-colors"
                >
                  Browse the plugin directory →
                </Link>
              </p>
              <h3 className="text-[15px] font-medium text-paper mb-2 mt-6">Using plugins</h3>
              <p className="text-sm text-fog leading-relaxed mb-4">
                Pin the plugin for your project in{" "}
                <span className="font-mono text-[13px] text-mist">.talon/plugins.json</span>
                {" "}— that file is what you review, and nothing loads without it:{" "}
                <span className="font-mono text-[13px] text-mist">talon-mobile-ui@1.0.0</span>.
                When a build needs a capability the project doesn&apos;t have — a payment
                provider, a new API — the agent searches the directory with{" "}
                <span className="font-mono text-[13px] text-mist">search_plugins</span>,
                shows you the matching plugins with their version and license, and enables
                only the one the request chose. Then just describe what you want: the agent
                loads the one matching skill itself — connect, balance, or send —
                one at a time, never several just in case. State-changing
                mainnet calls always ask you in the UI before anything is sent.
                Updates never land silently: your build keeps serving the pinned
                version and tells you the exact version to adopt when the directory
                publishes something newer.
              </p>
              <h3 className="text-[15px] font-medium text-paper mb-2 mt-6">Building a plugin</h3>
              <p className="text-sm text-fog leading-relaxed mb-4">
                A plugin is a folder in the{" "}
                <a
                  href="https://github.com/Luckyisrael/talon-plugin-directory"
                  target="_blank"
                  rel="noopener noreferrer"
                  className="text-mist underline underline-offset-2 hover:text-paper transition-colors"
                >
                  public plugin directory
                </a>{" "}
                with a <span className="font-mono text-[13px] text-mist">talon-plugin.json</span> manifest
                plus <span className="font-mono text-[13px] text-mist">skills/*/SKILL.md</span> files
                carrying <span className="font-mono text-[13px] text-mist">name</span>, <span className="font-mono text-[13px] text-mist">description</span>,{" "}
                <span className="font-mono text-[13px] text-mist">version</span>, and <span className="font-mono text-[13px] text-mist">triggers</span>.
                Write guidance plus code templates, never executables. Pin exact
                dependency versions, keep an MIT license at the root, and put
                working starting points under <span className="font-mono text-[13px] text-mist">references/</span>.
                The reference implementation is{" "}
                <a
                  href="https://github.com/Luckyisrael/talon-plugin-directory/tree/main/plugins/talon-mobile-ui"
                  target="_blank"
                  rel="noopener noreferrer"
                  className="text-mist underline underline-offset-2 hover:text-paper transition-colors"
                >
                  talon-mobile-ui in the plugin directory
                </a>
                : connect, balance, and send skills with version 1.0.0 pinned.
              </p>
              <div className="card-subtle p-5!">
                <h3 className="text-sm font-medium text-paper mb-2">Trust rules</h3>
                <p className="text-sm text-fog leading-relaxed">
                  Plugin bodies load on demand and never auto-apply rules. File
                  hashes pin every version, and updates need your approval.
                  Dependencies resolve through the normal install flow, and
                  everything the agent borrows lands in your workspace where
                  you can read it.
                </p>
              </div>
            </section>

            <section id="quotas" className="scroll-mt-8">
              <h2 className="text-[22px] font-medium tracking-tight text-paper mb-4">Quotas &amp; limits</h2>
              <p className="text-sm text-fog leading-relaxed mb-6">
                Two things are metered: AI prompts and live device time. You can see both in your
                workspace at any moment.
              </p>
              <div className="overflow-x-auto">
                <table className="w-full text-sm border-collapse">
                  <thead>
                    <tr className="border-b border-graphite text-left">
                      <th className="py-2.5 pr-4 gauge font-normal">Allowance</th>
                      <th className="py-2.5 px-4 gauge font-normal">Free</th>
                      <th className="py-2.5 pl-4 gauge font-normal">Pro</th>
                    </tr>
                  </thead>
                  <tbody className="font-mono tabular-nums">
                    <tr className="border-b border-graphite/60">
                      <td className="py-3 pr-4 text-fog font-sans">AI prompts per cycle</td>
                      <td className="py-3 px-4 text-paper">20</td>
                      <td className="py-3 pl-4 text-paper">500</td>
                    </tr>
                    <tr>
                      <td className="py-3 pr-4 text-fog font-sans">Live device time</td>
                      <td className="py-3 px-4 text-paper">60 min</td>
                      <td className="py-3 pl-4 text-paper">25 hrs</td>
                    </tr>
                  </tbody>
                </table>
              </div>
              <p className="text-sm text-fog leading-relaxed mt-6">
                Reading files, running builds, and streaming the preview don&apos;t count against
                your prompt quota — only messages the agent acts on do. See{" "}
                <Link href="/pricing" className="text-mist underline underline-offset-2 hover:text-paper transition-colors">
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
