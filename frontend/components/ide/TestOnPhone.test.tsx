import { describe, expect, it, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import TestOnPhone from "./TestOnPhone";
import { testInfo } from "@/lib/api";

vi.mock("@/lib/api", () => ({
  testInfo: { get: vi.fn() },
}));

describe("Get-the-app pane", () => {
  it("renders the QR, the deep link, and the APK link when the phone path is live", async () => {
    vi.mocked(testInfo.get).mockResolvedValue({
      available: true,
      deep_link: "exp://talon.example.com:17812",
      metro_url: "http://talon.example.com:17812",
      dev_client_url: "http://localhost:8080/v1/projects/1/dev-client.apk",
    });
    render(<TestOnPhone projectId={1} />);
    // The pane is always open — no collapse to step through first. The QR
    // mounts with the aria-label carrying the link; the APK link follows one
    // render later (info → apkUrl effect chain).
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

  // FM-DEVa(ii): a first-time phone installs the client BEFORE there is a
  // session to pair with. The URL is on the wire (session or not) — the pane
  // must render it outside the available branch too, or the download stays
  // unreachable exactly when it is needed.
  it("offers the APK to a first-time phone even with no session", async () => {
    vi.mocked(testInfo.get).mockResolvedValue({
      available: false,
      reason: "no device session running — start the device first",
      dev_client_url: "http://localhost:8080/v1/projects/1/dev-client.apk",
    });
    render(<TestOnPhone projectId={1} />);
    await waitFor(() =>
      expect(screen.getByText(/no device session running/)).toBeTruthy(),
    );
    expect(screen.queryByRole("img")).toBeNull(); // still no dead QR
    expect(screen.getByText(/Install the dev client/i)).toBeTruthy();
  });

  // FM-DEVe(ii): the shipped dev client's merged manifest registers
  // `talonapp`, `exp+talon-app` and `expo-dev-launcher` — never `exp` — so a
  // phone camera's VIEW intent for the `exp://` QR has nothing to resolve
  // against. The instruction has to name the entry the installed client
  // actually serves (its launcher's own "Scan QR Code"), or the tap
  // dead-ends — or opens Expo Go, which lacks this project's native modules.
  it("tells the user to open the QR with the dev client's own scanner", async () => {
    vi.mocked(testInfo.get).mockResolvedValue({
      available: true,
      deep_link: "exp://talon.example.com:17812",
      dev_client_url: "http://localhost:8080/v1/projects/1/dev-client.apk",
    });
    render(<TestOnPhone projectId={1} />);
    await waitFor(() =>
      expect(screen.getByText(/Scan QR Code/i)).toBeTruthy(),
    );
    expect(screen.queryByText(/phone camera/i)).toBeNull();
  });
});
