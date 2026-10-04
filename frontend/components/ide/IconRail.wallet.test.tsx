import { describe, expect, it, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import IconRail from "./IconRail";

// FM-WALLb — an unconnected wallet must be hard to forget. The web rail is
// the navigation surface the user named: while /v1/me reports
// wallet_linked=false it carries a BOLD accent reminder; the moment a
// wallet links the row is gone (never latched, never cached stale).
//
// Harness: the app router is not mounted under vitest (house pattern —
// see verify-evidence.test.tsx); IconRail's own fetches are mocked off,
// me.get drives the reminder.
//
// Red before L65 (observed): the rail renders no wallet reminder at all.

vi.mock("next/navigation", () => ({
  useRouter: () => ({
    push: vi.fn(),
    replace: vi.fn(),
    back: vi.fn(),
    prefetch: vi.fn(),
    refresh: vi.fn(),
  }),
  usePathname: () => "/project/1",
  useSearchParams: () => new URLSearchParams(),
}));

vi.mock("@/components/dashboard/CreateProject", () => ({
  default: () => null,
}));

const meGet = vi.fn();
vi.mock("@/lib/api", async (importOriginal) => {
  const orig = await importOriginal<typeof import("@/lib/api")>();
  return {
    ...orig,
    projects: { ...orig.projects, list: vi.fn(async () => []) },
    conversations: { ...orig.conversations, list: vi.fn(async () => []) },
    me: { ...orig.me, get: (...args: unknown[]) => meGet(...args) },
  };
});

function renderRail() {
  return render(
    <IconRail projectId={1} projectName="Demo" collapsed={false} onToggleCollapsed={() => {}} />,
  );
}

describe("the rail's wallet reminder (FM-WALLb)", () => {
  it("L65 — reminds, in bold, while no wallet is linked", async () => {
    meGet.mockResolvedValue({
      id: 7,
      email: "dev@talon.app",
      display_name: "Dev",
      tier: "free",
      created_at: "2026-10-04",
      history: [],
      wallet_linked: false,
    });
    renderRail();

    const row = await screen.findByText("Connect your wallet");
    // Bold is the instruction, not a mood: the class carries it.
    expect(row.className).toContain("font-semibold");
  });

  it("L65 — a linked wallet drops the reminder entirely", async () => {
    meGet.mockResolvedValue({
      id: 7,
      email: "dev@talon.app",
      display_name: "Dev",
      tier: "free",
      created_at: "2026-10-04",
      history: [],
      wallet_linked: true,
    });
    renderRail();

    // The rail settles on its project list; the reminder must not be there.
    await waitFor(() => expect(screen.getByText("New project")).toBeTruthy());
    expect(screen.queryByText("Connect your wallet")).toBeNull();
  });

  it("L65 — a guest (profile unreachable) is never nagged", async () => {
    meGet.mockRejectedValue(new Error("401"));
    renderRail();

    await waitFor(() => expect(screen.getByText("New project")).toBeTruthy());
    expect(screen.queryByText("Connect your wallet")).toBeNull();
  });
});
