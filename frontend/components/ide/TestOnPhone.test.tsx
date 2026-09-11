import { describe, expect, it, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
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
    await waitFor(() =>
      expect(screen.getByText(/not built on this server/i)).toBeTruthy(),
    );
    expect(screen.queryByText(/Install the dev client/i)).toBeNull();
  });
});
