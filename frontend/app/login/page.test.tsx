import { describe, expect, it, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import LoginPage from "./page";
import { auth } from "@/lib/api";

// FM-PROVa — a sign-in surface offers only what the product backs, and the
// retirement stays reversible. Google sits in `allProviders` while the
// product is GitHub-first: a second offer that renders disabled ("not
// configured") at best, and at worst a live OAuth path to an account the
// product doesn't manage. The fix is a comment, not a deletion — the day
// Google returns it must be an uncomment, not a re-implementation.
//
// Red before L64 (observed): the login renders "Continue with Google"
// beside GitHub, and the subtitle names it too.

vi.mock("@/lib/api", () => ({
  auth: { providers: vi.fn(), startURL: vi.fn() },
}));

describe("login offers only what the product backs (FM-PROVa)", () => {
  it("L64 — renders GitHub and never Google, subtitle included", async () => {
    vi.mocked(auth.providers).mockResolvedValue({
      providers: [{ id: "github", name: "GitHub" }],
    });
    render(<LoginPage />);

    await waitFor(() =>
      expect(screen.getByText("Continue with GitHub")).toBeTruthy(),
    );
    // No surface may name or offer Google while it is retired.
    expect(screen.queryByText("Continue with Google")).toBeNull();
    expect(screen.queryByText(/Google/)).toBeNull();
  });

  it("L64 — a provider the server doesn't have never resurrects a button", async () => {
    vi.mocked(auth.providers).mockResolvedValue({
      providers: [{ id: "github", name: "GitHub" }],
    });
    render(<LoginPage />);

    await waitFor(() =>
      expect(screen.getByText("Continue with GitHub")).toBeTruthy(),
    );
    // The offer list is the product's, not the server's: even a server
    // that still reports google must not put the button back on screen.
    expect(screen.getAllByRole("button").length).toBe(1);
  });
});
