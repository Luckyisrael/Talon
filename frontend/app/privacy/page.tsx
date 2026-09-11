import type { Metadata } from "next";
import Link from "next/link";
import { MarketingNav, MarketingFooter } from "@/components/marketing";

export const metadata: Metadata = {
  title: "Privacy",
  description:
    "What Talon collects, how it is used, and what it never touches — including your keys.",
};

const LAST_UPDATED = "August 2026";

export default function PrivacyPage() {
  return (
    <div className="min-h-screen flex flex-col bg-void">
      <MarketingNav />

      <main className="flex-1 w-full max-w-[720px] mx-auto px-6 pt-16 pb-20">
        <header className="mb-12">
          <span className="gauge">privacy policy</span>
          <h1 className="text-4xl font-medium text-paper mt-3 mb-3 tracking-tight">
            Privacy
          </h1>
          <p className="font-mono text-xs text-ash">Last updated: {LAST_UPDATED}</p>
        </header>

        <div className="space-y-10">
          <section>
            <p className="text-sm text-fog leading-relaxed">
              This policy explains what Talon collects when you build with it, why, and
              what it deliberately never touches. It is written plainly on purpose — if anything
              here is unclear, ask us before you rely on it.
            </p>
          </section>

          <section>
            <h2 className="text-[15px] font-medium text-paper mb-3">What we collect</h2>
            <ul className="space-y-3 text-sm text-fog leading-relaxed">
              <li>
                <span className="text-paper font-medium">Account.</span> Your email address and a
                salted, hashed password. We store the hash, never the password itself.
              </li>
              <li>
                <span className="text-paper font-medium">Your projects.</span> The prompts you
                send, the files the agent generates, and build output — everything needed to keep
                your workspace working between sessions.
              </li>
              <li>
                <span className="text-paper font-medium">Usage.</span> Counts of AI prompts and
                minutes of device time, so we can show your quota and enforce it.
              </li>
              <li>
                <span className="text-paper font-medium">Basic technical data.</span> The minimum
                needed to operate and secure the service.
              </li>
            </ul>
          </section>

          <section>
            <h2 className="text-[15px] font-medium text-paper mb-3">How we use it</h2>
            <p className="text-sm text-fog leading-relaxed">
              To run the builder, generate and preview your app, keep your account secure, and
              measure usage against your plan. We do not sell your data.
            </p>
          </section>

          <section>
            <h2 className="text-[15px] font-medium text-paper mb-3">AI processing</h2>
            <p className="text-sm text-fog leading-relaxed">
              To generate code, your prompts and the relevant project context are sent to a
              third-party AI model provider. That content is processed to return a result to your
              workspace. Don&apos;t paste secrets you wouldn&apos;t want processed this way.
            </p>
          </section>

          <section>
            <h2 className="text-[15px] font-medium text-paper mb-3">What we never collect</h2>
            <p className="text-sm text-fog leading-relaxed">
              Talon never asks for, stores, or generates a seed phrase or private key.
              Wallet connections run through the Mobile Wallet Adapter, and the device&apos;s Seed
              Vault or wallet app holds all key material. Key custody never reaches our servers.
            </p>
          </section>

          <section>
            <h2 className="text-[15px] font-medium text-paper mb-3">Keeping and deleting data</h2>
            <p className="text-sm text-fog leading-relaxed">
              Your projects stay until you delete them. Deleting a project removes its files and
              associated data. Deleting your account removes your projects and profile. Some
              records may persist briefly in backups before rotating out.
            </p>
          </section>

          <section>
            <h2 className="text-[15px] font-medium text-paper mb-3">Contact</h2>
            <p className="text-sm text-fog leading-relaxed">
              Questions about privacy? Reach us through our{" "}
              <Link
                href="https://discord.gg/talon"
                target="_blank"
                rel="noopener noreferrer"
                className="text-paper underline underline-offset-2 hover:text-white transition-colors"
              >
                community channels
              </Link>
              . See also our{" "}
              <Link href="/terms" className="text-paper underline underline-offset-2 hover:text-white transition-colors">
                Terms
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
