import { beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import ExportPanel from "./ExportPanel";
import { exportApi, projects, skrApi, usage } from "@/lib/api";

// FM-SKRd(i)/(c) — the export surface teaches its own paywall. The old
// panel told free users "Release export needs the Pro plan": a lie of
// omission that hid the credits path entirely (free accounts export with
// SKR credits — one per release). The replacement states the actual rule
// from the server's quote: balance, pack price, pack size, and where
// payment happens (the app — the wallet lives there, web never takes
// payments). A quote failure must state the reason the wire carried,
// never a bare "try again".
//
// Red before L66 (observed): the free branch renders the Pro-plan line
// and no credit rule exists anywhere in the panel; a quote failure shows
// no reason.

vi.mock("@/lib/api", () => ({
  usage: { get: vi.fn() },
  projects: { get: vi.fn() },
  exportApi: { listAssets: vi.fn(), start: vi.fn(), get: vi.fn(), bundle: vi.fn() },
  skrApi: { quote: vi.fn() },
}));

const quoteOf = {
  skr_amount: 37,
  credits: 5,
  mint: "mint",
  treasury: "treasury",
  spare_credits: 2,
};

beforeEach(() => {
  vi.clearAllMocks();
  vi.mocked(usage.get).mockResolvedValue({
    tier: "free",
  } as unknown as Awaited<ReturnType<typeof usage.get>>);
  vi.mocked(projects.get).mockRejectedValue(new Error("no project read needed here"));
  vi.mocked(exportApi.listAssets).mockResolvedValue([]);
  vi.mocked(skrApi.quote).mockResolvedValue(quoteOf);
});

describe("the export panel teaches its paywall (FM-SKRd)", () => {
  it("L66 — a free export states the actual rule from the quote, not 'Pro plan'", async () => {
    render(<ExportPanel projectId={7} />);
    fireEvent.click(screen.getByText("Export"));

    // (i) the lie of omission is gone — no bare plan refusal.
    await waitFor(() =>
      expect(screen.queryByText(/needs the Pro plan/)).toBeNull(),
    );
    // The real rule, all of it quoted from the server: the pack line is
    // async (it lands with the quote), so wait for it first — the rest of
    // the block renders with it.
    await waitFor(() =>
      expect(screen.getByText("37 SKR unlocks 5 exports")).toBeTruthy(),
    );
    expect(screen.getByText(/one export credit/)).toBeTruthy();
    expect(screen.getByText(/2 of 5 export credits/)).toBeTruthy();
    expect(screen.getByText(/Pay in the Talon app/)).toBeTruthy();
  });

  it("L66 — a quote failure states the reason the wire carried", async () => {
    vi.mocked(skrApi.quote).mockRejectedValue(
      new Error("the credits ledger is offline"),
    );
    render(<ExportPanel projectId={7} />);
    fireEvent.click(screen.getByText("Export"));

    await waitFor(() =>
      expect(screen.getByText(/the credits ledger is offline/)).toBeTruthy(),
    );
  });

  it("L66 — a linked (paid) export is not lectured about credits", async () => {
    vi.mocked(usage.get).mockResolvedValue({
      tier: "pro",
    } as unknown as Awaited<ReturnType<typeof usage.get>>);
    render(<ExportPanel projectId={7} />);
    fireEvent.click(screen.getByText("Export"));

    // The pro branch is the store checklist — the paywall teaching stays
    // out of it, and so does any leftover Pro-plan nag.
    await waitFor(() =>
      expect(screen.getByText("Store checklist")).toBeTruthy(),
    );
    expect(screen.queryByText(/needs the Pro plan/)).toBeNull();
    expect(screen.queryByText(/one export credit/)).toBeNull();
  });
});
