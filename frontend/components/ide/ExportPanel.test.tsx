import { beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import { SubmissionAssist } from "./SubmissionAssist";
import { exportApi } from "@/lib/api";

// Row L38 — FM-SUBa(i)/(ii)/(iii): after a paid export the user is handed
// HOW to submit, and no part of that answer is authored here.
//
// N1 the panel must show what the SERVER sent: the package, the portal URL
//    and the costs note all arrive from GET /v1/exports/{id}/bundle. The
//    package is a value this component cannot know on its own, and the
//    portal address is one no client would be allowed to hardcode — FM-SUBa(ii)
//    is exactly a panel pointing somewhere the docs no longer recognise.
// N2 the disclosure is the server's to state. FM-SUBa(iii): the old panel
//    shipped a hand-typed "Publishing costs real SOL (~0.2 for fees + storage)"
//    sentence; two paraphrases of a money disclosure is a disclosure that
//    eventually says something the server would not.
// N3 the files are handed over, not re-typed: SUBMIT.md and dapp-store.json
//    are served as bytes and saved as bytes.
//
// Harness note: the panel's done branch sits behind a 10s poll and
// @testing-library's waitFor only auto-advances fake timers when a `jest`
// global exists (it does not under vitest), so this suite exercises the
// assist directly rather than driving the panel's timer. The full path —
// job reaches done and the assist appears — is covered on the mobile side
// in talonmobileapp/components/build/export-panel.test.tsx.

vi.mock("@/lib/api", () => ({
  exportApi: { bundle: vi.fn() },
}));

const PACKAGE = "com.talon.l38demo";
const PORTAL = "https://publish.example.test/portal";
const COSTS =
  "Publishing to the Solana dApp Store costs real SOL: keep ~0.2 SOL in your " +
  "publisher wallet for transaction fees and ArDrive storage.";
const README = [
  "# Submit this release to the Solana dApp Store",
  "",
  "Talon built and signed this APK; submission is yours, with your own wallet.",
  "",
  "## Option A — Publisher Portal (recommended for a first release)",
  "",
  "1. Sign up at https://publish.solanamobile.com and complete KYC/KYB.",
  `2. The portal matches it by package name \`${PACKAGE}\`.`,
  "3. Upload this APK as a new version and approve the signing requests.",
].join("\n");

const servedBundle = () => ({
  submit_md: README,
  manifest: {
    android_package: PACKAGE,
    portal_url: PORTAL,
    costs_note: COSTS,
    publish_command:
      "dapp-store --apk-file ./release.apk --keypair <publisher-keypair.json> --whats-new \"first\"",
  },
});

/** Files the assist asked the browser to save, in click order. */
let saved: { name: string; blob: Blob }[] = [];

beforeEach(() => {
  vi.clearAllMocks();
  saved = [];

  // jsdom has no object URLs and clicking an anchor means navigation; both
  // are stubbed so what is asserted is WHAT is handed over, not the browser.
  const blobs = new Map<string, Blob>();
  let seq = 0;
  URL.createObjectURL = vi.fn((blob: Blob) => {
    const url = `blob:test-${seq++}`;
    blobs.set(url, blob);
    return url;
  }) as unknown as typeof URL.createObjectURL;
  URL.revokeObjectURL = vi.fn() as unknown as typeof URL.revokeObjectURL;
  vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(function (
    this: HTMLAnchorElement,
  ) {
    saved.push({
      name: this.download,
      blob: blobs.get(this.getAttribute("href") ?? "") ?? new Blob(),
    });
  });
});

describe("submission assist after a paid export", () => {
  it("a finished export shows the served package, portal and costs note", async () => {
    vi.mocked(exportApi.bundle).mockResolvedValue(servedBundle());

    render(<SubmissionAssist jobId={41} />);
    expect(exportApi.bundle).toHaveBeenCalledWith(41);

    // The package: only the bundle carries it.
    expect(await screen.findByText(PACKAGE)).toBeTruthy();

    // The portal link: the URL the server sent, not one typed in here.
    const link = await screen.findByRole("link", { name: /Open Publisher Portal/i });
    expect(link).toHaveAttribute("href", PORTAL);

    // The disclosure: the server's wording, verbatim.
    expect(await screen.findByText(COSTS)).toBeTruthy();
  });

  it("the bundle download hands over SUBMIT.md and dapp-store.json", async () => {
    vi.mocked(exportApi.bundle).mockResolvedValue(servedBundle());

    render(<SubmissionAssist jobId={41} />);
    fireEvent.click(await screen.findByRole("button", { name: /Download SUBMIT\.md/ }));

    expect(saved).toHaveLength(1);
    expect(saved[0].name).toBe("SUBMIT.md");
    expect(await saved[0].blob.text()).toContain("Option A — Publisher Portal");

    fireEvent.click(screen.getByRole("button", { name: /Download dapp-store\.json/ }));
    expect(saved).toHaveLength(2);
    expect(saved[1].name).toBe("dapp-store.json");
    const manifest = JSON.parse(await saved[1].blob.text());
    expect(manifest.android_package).toBe(PACKAGE);
    expect(manifest.costs_note).toBe(COSTS);
  });
});
