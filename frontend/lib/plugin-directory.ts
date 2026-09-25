// Registry shapes and pure helpers for the public plugin directory page
// (/plugins). The registry JSON is fetched server-side in app/plugins;
// everything here is deterministic so it stays unit-testable.

export type RegistryEntry = {
  name: string;
  version: string;
  path: string;
  description: string;
  license?: string;
  /** GitHub repo slug, e.g. "github.com/Luckyisrael/talon-plugin-directory". */
  repo?: string;
  /** Search terms the directory ranks on. */
  keywords?: string[];
  /** Optional project website, surfaced as the card's Website link. */
  website?: string;
};

export type PluginCard = RegistryEntry & {
  /** Short skill names (directory names) from the pack manifest. Empty
   * when the manifest could not be fetched — the card still renders. */
  skills: string[];
};

/** skillShortName turns "skills/moove-receive/SKILL.md" into "moove-receive". */
export function skillShortName(manifestPath: string): string {
  const parts = manifestPath.split("/");
  const i = parts.indexOf("skills");
  if (i >= 0 && i + 1 < parts.length) return parts[i + 1];
  return parts[parts.length - 1] || manifestPath;
}

/** filterPlugins keeps entries where EVERY whitespace-separated token of
 * the query appears in the name, description, keywords, or skill names.
 * An empty query lists the whole directory. */
export function filterPlugins(plugins: PluginCard[], query: string): PluginCard[] {
  const tokens = query.toLowerCase().split(/\s+/).filter(Boolean);
  if (tokens.length === 0) return plugins;
  return plugins.filter((p) => {
    const haystack = [p.name, p.description, ...(p.keywords ?? []), ...(p.skills ?? [])]
      .join(" ")
      .toLowerCase();
    return tokens.every((t) => haystack.includes(t));
  });
}
