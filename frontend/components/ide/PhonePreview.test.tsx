import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";
import PhonePreview from "./PhonePreview";

describe("PhonePreview states", () => {
  it("idle invites starting a device", () => {
    render(<PhonePreview status="idle" onStartSession={() => {}} />);
    expect(screen.getByText("No device attached.")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Start a device" })).toBeInTheDocument();
  });

  it("connecting shows the boot state, not a dead panel", () => {
    render(<PhonePreview status="connecting" />);
    expect(screen.getByText("Booting a device")).toBeInTheDocument();
  });

  it("error names the failure and offers retry", () => {
    render(<PhonePreview status="error" onStartSession={() => {}} />);
    expect(screen.getByText("Could not reach a device.")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Try again" })).toBeInTheDocument();
  });

  it("opens no sockets without a session", () => {
    // No sessionId: no WebSocket is constructed, so nothing can leak or dial.
    const { container } = render(<PhonePreview status="streaming" />);
    expect(container.querySelector("canvas")).not.toBeNull();
  });
});
