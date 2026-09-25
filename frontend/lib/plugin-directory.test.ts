import { describe, expect, it } from "vitest";
import { filterPlugins, skillShortName, type PluginCard } from "./plugin-directory";

const pack = (over: Partial<PluginCard>): PluginCard => ({
  name: "demo-pack",
  version: "1.0.0",
  path: "plugins/demo-pack",
  description: "A demo pack.",
  skills: [],
  ...over,
});

const directory: PluginCard[] = [
  pack({
    name: "moove-payment-api",
    description: "Moove payment links (checkout API), send, receive, and swap flows for moove.xyz.",
    keywords: ["payment", "checkout", "swap"],
    skills: ["moove-overview", "moove-receive", "moove-swap"],
  }),
  pack({
    name: "talon-mobile-ui",
    description: "Wallet connect button, SOL balance view, transaction signing for Solana Mobile.",
    keywords: ["wallet", "solana", "connect"],
    skills: ["wallet-connect", "sol-balance", "sol-send"],
  }),
  pack({
    name: "pyth-api",
    description: "Pyth price feeds, indices, and a data marketplace for real-time market data.",
    keywords: ["pyth", "price", "feeds", "oracle"],
    skills: ["pyth-overview"],
  }),
];

describe("skillShortName", () => {
  it("reads the skill directory out of a manifest path", () => {
    expect(skillShortName("skills/moove-receive/SKILL.md")).toBe("moove-receive");
  });

  it("falls back to the last segment on unexpected shapes", () => {
    expect(skillShortName("SKILL.md")).toBe("SKILL.md");
    expect(skillShortName("")).toBe("");
  });
});

describe("filterPlugins", () => {
  it("lists the whole directory on an empty query", () => {
    expect(filterPlugins(directory, "")).toHaveLength(3);
    expect(filterPlugins(directory, "   ")).toHaveLength(3);
  });

  it("matches on name, description, keywords, and skills alike", () => {
    expect(filterPlugins(directory, "pyth").map((p) => p.name)).toEqual(["pyth-api"]);
    expect(filterPlugins(directory, "solana mobile").map((p) => p.name)).toEqual([
      "talon-mobile-ui",
    ]);
    expect(filterPlugins(directory, "checkout").map((p) => p.name)).toEqual([
      "moove-payment-api",
    ]);
    expect(filterPlugins(directory, "moove-swap").map((p) => p.name)).toEqual([
      "moove-payment-api",
    ]);
  });

  it("requires every token to match (AND, not OR)", () => {
    expect(filterPlugins(directory, "payment swap")).toHaveLength(1);
    expect(filterPlugins(directory, "payment wallet")).toHaveLength(0);
  });

  it("is case-insensitive and returns an honest empty list", () => {
    expect(filterPlugins(directory, "PYTH")).toHaveLength(1);
    expect(filterPlugins(directory, "insurance underwriting")).toHaveLength(0);
  });

  it("tolerates entries without keywords or skills", () => {
    const bare = [pack({ name: "bare", description: "plain text here", keywords: undefined })];
    expect(filterPlugins(bare, "plain")).toHaveLength(1);
    expect(filterPlugins(bare, "missing")).toHaveLength(0);
  });
});
