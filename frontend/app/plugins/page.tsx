import type { Metadata } from "next";
import Link from "next/link";
import { MarketingNav, MarketingFooter } from "@/components/marketing";
import PluginDirectory from "@/components/marketing/PluginDirectory";
import { skillShortName, type PluginCard, type RegistryEntry } from "@/lib/plugin-directory";

export const metadata: Metadata = {
  title: "Plugins",
  description:
    "The Talon plugin directory — public packs the agent discovers mid-build: web3 components, payment and market-data APIs, design packs.",
};

// The registry IS the source of truth: this page fetches it from the
// directory repo on GitHub, so a pack pushed to main appears here within
// the revalidate window with no deploy. Failure renders an honest
// unreachable state instead of breaking the route.
const REGISTRY_URL =
  "https://raw.githubusercontent.com/Luckyisrael/talon-plugin-directory/main/registry.json";

type RegistryIndex = { base?: string; plugins?: RegistryEntry[] };

async function loadDirectory(): Promise<{ plugins: PluginCard[]; live: boolean }> {
  try {
    const res = await fetch(REGISTRY_URL, { next: { revalidate: 300 } });
    if (!res.ok) throw new Error(`registry responded ${res.status}`);
    const index = (await res.json()) as RegistryIndex;
    const entries = Array.isArray(index.plugins) ? index.plugins : [];
    const base =
      index.base ?? REGISTRY_URL.replace(/\/registry\.json$/, "");

    const plugins = await Promise.all(
      entries.map(async (entry): Promise<PluginCard> => {
        // The manifest adds the skill list; it is enrichment only — a
        // failed fetch degrades the card, never the page.
        let skills: string[] = [];
        try {
          const manRes = await fetch(`${base}/${entry.path}/talon-plugin.json`, {
            next: { revalidate: 300 },
          });
          if (manRes.ok) {
            const man = (await manRes.json()) as { skills?: unknown } | null;
            if (man && Array.isArray(man.skills)) {
              skills = man.skills.filter((s): s is string => typeof s === "string").map(skillShortName);
            }
          }
        } catch {
          // Manifest optional — card renders without skills.
        }
        return { ...entry, skills };
      }),
    );
    return { plugins, live: true };
  } catch {
    return { plugins: [], live: false };
  }
}

export default async function PluginsPage() {
  const { plugins, live } = await loadDirectory();

  return (
    <div className="min-h-screen flex flex-col bg-void">
      <MarketingNav />

      <main className="flex-1 w-full max-w-[1200px] mx-auto px-6 pt-16 pb-20">
        <header className="mb-10">
          <span className="badge">PLUGIN DIRECTORY</span>
          <h1 className="t-heading-sm mt-4 mb-3">
            Plugins your agent can discover mid-build
          </h1>
          <p className="t-body-sm text-fog max-w-2xl">
            Public packs that teach the agent a specialty — web3 components, payment
            APIs, market data, design systems. When a build needs a capability the
            project doesn&apos;t have, the agent searches this directory, shows you the
            match, and pins it in{" "}
            <span className="font-mono text-[13px] text-mist">.talon/plugins.json</span>{" "}
            — nothing loads without that file.
          </p>
        </header>

        <PluginDirectory plugins={plugins} live={live} />

        <section className="mt-14 max-w-2xl">
          <h2 className="t-heading-sm mb-3">Build your own</h2>
          <p className="t-body-sm text-fog mb-4">
            A plugin is a folder with a manifest and skill files, plus one row in the
            registry — the same row that puts it on this page. Publishing is always
            safe: pinned builds keep serving the old bytes until their pin moves.
          </p>
          <div className="flex items-center gap-6">
            <Link
              href="/docs#plugins"
              className="text-sm text-mist hover:text-paper transition-colors"
            >
              How plugins load →
            </Link>
            <a
              href="https://github.com/Luckyisrael/talon-plugin-directory"
              target="_blank"
              rel="noopener noreferrer"
              className="text-sm text-mist hover:text-paper transition-colors"
            >
              Directory repo →
            </a>
          </div>
        </section>
      </main>

      <MarketingFooter />
    </div>
  );
}
