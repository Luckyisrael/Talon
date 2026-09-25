"use client";

import { useMemo, useState } from "react";
import { filterPlugins, type PluginCard } from "@/lib/plugin-directory";

// The directory grid + search for /plugins. The data arrives from the
// server component (registry fetch, revalidated every few minutes, so
// new packs appear on their own); filtering is client-side so typing
// never round-trips.
export default function PluginDirectory({
  plugins,
  live,
}: {
  plugins: PluginCard[];
  live: boolean;
}) {
  const [query, setQuery] = useState("");
  const results = useMemo(() => filterPlugins(plugins, query), [plugins, query]);

  if (!live) {
    return (
      <div className="card-subtle p-6!">
        <h3 className="text-[15px] font-medium tracking-tight text-paper mb-1.5">
          Directory unreachable right now
        </h3>
        <p className="text-sm text-fog leading-relaxed">
          The registry could not be fetched. Plugins already pinned for a project
          keep serving from their verified cache — try again in a few minutes, or
          browse the packs in the source repo:{" "}
          <a
            href="https://github.com/Luckyisrael/talon-plugin-directory"
            target="_blank"
            rel="noopener noreferrer"
            className="text-mist hover:text-paper transition-colors"
          >
            github.com/Luckyisrael/talon-plugin-directory
          </a>
        </p>
      </div>
    );
  }

  return (
    <div>
      <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-4 mb-6">
        <label className="block w-full sm:max-w-xs">
          <span className="sr-only">Search plugins</span>
          <input
            type="search"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder="Search — payment, swap, wallet…"
            className="field"
          />
        </label>
        <p className="t-caption text-ash">
          {results.length} of {plugins.length} plugin{plugins.length === 1 ? "" : "s"}
        </p>
      </div>

      {results.length === 0 ? (
        <div className="card-subtle p-6!">
          <p className="text-sm text-fog leading-relaxed">
            {plugins.length === 0
              ? "The directory is empty — no packs published yet."
              : `No plugin matches “${query}”. Try a capability word — payment, wallet, price feed — or build your own: see Docs → Plugins.`}
          </p>
        </div>
      ) : (
        <div className="grid grid-cols-1 md:grid-cols-2 gap-2">
          {results.map((p) => (
            <PluginCardView key={`${p.name}@${p.version}`} plugin={p} />
          ))}
        </div>
      )}
    </div>
  );
}

function PluginCardView({ plugin: p }: { plugin: PluginCard }) {
  const source = p.repo ? `https://${p.repo}/tree/main/${p.path}` : null;
  return (
    <div className="card-subtle p-6! flex flex-col">
      <div className="flex items-start justify-between gap-3">
        <h3 className="text-[15px] font-mono font-medium tracking-tight text-paper break-all">
          {p.name}
        </h3>
        <span className="badge flex-shrink-0">v{p.version}</span>
      </div>

      <p className="text-sm text-fog leading-relaxed mt-2">{p.description}</p>

      {p.keywords && p.keywords.length > 0 && (
        <div className="flex flex-wrap gap-1.5 mt-3">
          {p.keywords.map((k) => (
            <span
              key={k}
              className="text-[11px] font-mono text-ash border border-graphite rounded px-1.5 py-0.5"
            >
              {k}
            </span>
          ))}
        </div>
      )}

      {p.skills.length > 0 && (
        <p className="text-xs text-ash mt-3 leading-relaxed">
          <span className="gauge mr-1.5">SKILLS</span>
          {p.skills.join(" · ")}
        </p>
      )}

      <div className="flex items-center gap-4 mt-auto pt-4 text-[13px]">
        {p.website && (
          <a
            href={p.website}
            target="_blank"
            rel="noopener noreferrer"
            className="text-mist hover:text-paper transition-colors"
          >
            Website →
          </a>
        )}
        {source && (
          <a
            href={source}
            target="_blank"
            rel="noopener noreferrer"
            className="text-mist hover:text-paper transition-colors"
          >
            Source →
          </a>
        )}
        {p.license && (
          <span className="ml-auto font-mono text-xs text-ash">{p.license}</span>
        )}
      </div>
    </div>
  );
}
