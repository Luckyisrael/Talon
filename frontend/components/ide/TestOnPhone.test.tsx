import { describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import TestOnPhone from "./TestOnPhone";
import { testInfo } from "@/lib/api";

vi.mock("@/lib/api", () => ({
  testInfo: { get: vi.fn() },
}));

describe("DV2 TestOnPhone panel", () => {
  it("renders the QR + deep link when the phone path is live", async () => {
    vi.mocked(testInfo.get).mockResolvedValue({
      available: true,
      deep_link: "exp://talon.example.com:17812",
      metro_url: "http://talon.example.com:17812",
      dev_client_url: "http://localhost:8080/v1/projects/1/dev-client.apk",
    });
    render(<TestOnPhone projectId={1} />);
    // Wait for the availability verdict first — the header button is
    // disabled until info lands, and a click on a disabled button is a
    // no-op. B4: the drawer is collapsed by default; open it, then the
    // QR mounts.
    await waitFor(() =>
      expect(screen.getByRole("button", { name: /Test on your phone/i })).toBeTruthy(),
    );
    fireEvent.click(screen.getByRole("button", { name: /Test on your phone/i }));
    // The QR svg mounts with the aria-label carrying the link; the APK
    // link follows one render later (info → apkUrl effect chain).
    await waitFor(() =>
      expect(
        screen.getByRole("img", { name: /exp:\/\/talon\.example\.com/ }),
      ).toBeTruthy(),
    );
    expect(screen.getByText("exp://talon.example.com:17812")).toBeTruthy();
    await waitFor(() =>
      expect(screen.getByText(/Install the dev client/i)).toBeTruthy(),
    );
  });

  it("stays collapsed by default — the QR never crowds the rail", async () => {
    vi.mocked(testInfo.get).mockResolvedValue({
      available: true,
      deep_link: "exp://talon.example.com:17812",
    });
    const { container } = render(<TestOnPhone projectId={1} />);
    // The drawer waits one tick for info. Collapsed is a CSS collapse
    // (grid 0fr) — the QR stays mounted but clipped, the same law as the
    // thinking rows, so assert the style, not the DOM absence.
    await waitFor(() =>
      expect(screen.getByRole("button", { name: /Test on your phone/i })).toBeTruthy(),
    );
    const drawer = container.querySelector("div.grid");
    expect(drawer).not.toBeNull();
    expect((drawer as HTMLElement).style.gridTemplateRows).toBe("0fr");
    expect((drawer as HTMLElement).style.opacity).toBe("0");
    // And opening it reveals: one click, 1fr.
    fireEvent.click(screen.getByRole("button", { name: /Test on your phone/i }));
    expect((drawer as HTMLElement).style.gridTemplateRows).toBe("1fr");
  });

  it("shows the honest reason when no device session is live", async () => {
    vi.mocked(testInfo.get).mockResolvedValue({
      available: false,
      reason: "no device session running — start the device first",
    });
    render(<TestOnPhone projectId={1} />);
    await waitFor(() =>
      expect(
        screen.getByText(/no device session running/),
      ).toBeTruthy(),
    );
    // No dead QR renders.
    expect(screen.queryByRole("img")).toBeNull();
  });

  it("omits the APK button when the server has no build", async () => {
    vi.mocked(testInfo.get).mockResolvedValue({
      available: true,
      deep_link: "exp://10.0.0.5:17555",
    });
    render(<TestOnPhone projectId={1} />);
    // Availability first (the button is disabled-honest until then), then
    // open the collapsed drawer.
    await waitFor(() =>
      expect(screen.getByRole("button", { name: /Test on your phone/i })).toBeTruthy(),
    );
    fireEvent.click(screen.getByRole("button", { name: /Test on your phone/i }));
    await waitFor(() =>
      expect(screen.getByText(/not built on this server/i)).toBeTruthy(),
    );
    expect(screen.queryByText(/Install the dev client/i)).toBeNull();
  });
});
