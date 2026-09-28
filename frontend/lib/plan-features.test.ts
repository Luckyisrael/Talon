/**
 * L15 slice contract, frontend side — FM-C1a, written before the
 * implementation (TEST_RULES §3):
 *
 *  1. Legacy string features pass through byte-identical (old plans
 *     already stored as strings must render exactly as before).
 *  2. A shaped {text, files, acceptance} feature reduces to its text —
 *     React throws on object children, so an un-normalized feature is a
 *     crashed plan card, not a cosmetic bug.
 *  3. A malformed shaped feature (no text) yields "" rather than
 *     "[object Object]" or undefined — never an object, never a crash.
 */
import { describe, expect, it } from "vitest";
import { featureText, type PlanFeatureWire } from "./types";

describe("featureText", () => {
  it("passes legacy string features through unchanged", () => {
    expect(featureText("connect wallet via MWA")).toBe("connect wallet via MWA");
  });

  it("extracts text from a shaped feature", () => {
    const shaped: PlanFeatureWire = {
      text: "send SOL",
      files: ["app/send.tsx", "src/wallet.ts"],
      acceptance: "confirm sheet shows",
    };
    expect(featureText(shaped)).toBe("send SOL");
  });

  it("never yields an object, undefined, or a crash", () => {
    expect(featureText({})).toBe("");
    expect(featureText({ files: ["a.ts"] })).toBe("");
  });
});
