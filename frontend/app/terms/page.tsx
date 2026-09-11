import type { Metadata } from "next";
import Link from "next/link";
import { MarketingNav, MarketingFooter } from "@/components/marketing";

export const metadata: Metadata = {
  title: "Terms",
  description:
    "The terms for using Talon — accounts, acceptable use, AI-generated code, and on-chain responsibility.",
};

const LAST_UPDATED = "August 2026";

export default function TermsPage() {
  return (
    <div className="min-h-screen flex flex-col bg-void">
      <MarketingNav />

      <main className="flex-1 w-full max-w-[720px] mx-auto px-6 pt-16 pb-20">
        <header className="mb-12">
          <span className="gauge">terms of service</span>
          <h1 className="text-4xl font-medium text-paper mt-3 mb-3 tracking-tight">
            Terms
          </h1>
          <p className="font-mono text-xs text-ash">Last updated: {LAST_UPDATED}</p>
        </header>

        <div className="space-y-10">
          <section>
            <p className="text-sm text-fog leading-relaxed">
              By using Talon you agree to these terms. They are meant to be readable — the
              short version is: build responsibly, review what the agent writes, and you own your
              keys.
            </p>
          </section>

          <section>
            <h2 className="text-[15px] font-medium text-paper mb-3">The service</h2>
            <p className="text-sm text-fog leading-relaxed">
              Talon is a browser IDE that uses an AI agent to generate React Native apps,
              run them on a virtual Solana Seeker device, and build installable APKs. Features and
              quotas may change as the product evolves.
            </p>
          </section>

          <section>
            <h2 className="text-[15px] font-medium text-paper mb-3">Your account</h2>
            <p className="text-sm text-fog leading-relaxed">
              Keep your credentials secure and give an accurate email. You are responsible for
              activity under your account. One account is for one person or team you represent.
            </p>
          </section>

          <section>
            <h2 className="text-[15px] font-medium text-paper mb-3">Acceptable use</h2>
            <ul className="space-y-2.5 text-sm text-fog leading-relaxed">
              <li>Don&apos;t use the service for anything illegal or harmful.</li>
              <li>Don&apos;t abuse the compute — no crypto mining, load generation, or working around quotas.</li>
              <li>Don&apos;t try to extract other users&apos; data or the platform&apos;s secrets.</li>
              <li>Don&apos;t resell device time or access as your own product.</li>
            </ul>
          </section>

          <section>
            <h2 className="text-[15px] font-medium text-paper mb-3">AI-generated code</h2>
            <p className="text-sm text-fog leading-relaxed">
              The agent writes code to your instructions, but you are responsible for what you
              ship. Review it before release. We don&apos;t warrant that generated code is
              correct, secure, or fit for a particular purpose — treat it as a strong starting
              point, not a finished audit.
            </p>
          </section>

          <section>
            <h2 className="text-[15px] font-medium text-paper mb-3">Wallets and on-chain actions</h2>
            <p className="text-sm text-fog leading-relaxed">
              Talon never holds your keys. Wallet connections go through the Mobile Wallet
              Adapter, and your wallet or Seed Vault signs. On-chain transactions are irreversible
              and can move real funds — test on devnet, and you are responsible for anything your
              app signs in production.
            </p>
          </section>

          <section>
            <h2 className="text-[15px] font-medium text-paper mb-3">Ownership</h2>
            <p className="text-sm text-fog leading-relaxed">
              You own the app code you generate. Talon owns the platform — the IDE, the
              agent, and the surrounding software. Using the service doesn&apos;t transfer either
              way beyond that.
            </p>
          </section>

          <section>
            <h2 className="text-[15px] font-medium text-paper mb-3">Availability and changes</h2>
            <p className="text-sm text-fog leading-relaxed">
              The service is provided as-is, without guarantees of uptime. We may change, suspend,
              or discontinue features, and may suspend accounts that violate these terms. We&apos;ll
              be reasonable about it.
            </p>
          </section>

          <section>
            <h2 className="text-[15px] font-medium text-paper mb-3">Contact</h2>
            <p className="text-sm text-fog leading-relaxed">
              Questions about these terms? Reach us through our{" "}
              <Link
                href="https://discord.gg/talon"
                target="_blank"
                rel="noopener noreferrer"
                className="text-paper underline underline-offset-2 hover:text-white transition-colors"
              >
                community channels
              </Link>
              . See also our{" "}
              <Link href="/privacy" className="text-paper underline underline-offset-2 hover:text-white transition-colors">
                Privacy policy
              </Link>
              .
            </p>
          </section>
        </div>
      </main>

      <MarketingFooter />
    </div>
  );
}
