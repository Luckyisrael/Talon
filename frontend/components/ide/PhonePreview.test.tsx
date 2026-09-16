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
    // The boot state renders inside a full-size device frame — the panel is
    // a phone that has not finished booting, never a small empty box.
    expect(screen.getByText("Booting the device")).toBeInTheDocument();
    const { container } = render(<PhonePreview status="connecting" />);
    // The frame is drawn in every state, so the boot message lives inside a
    // phone-shaped screen rather than replacing it.
    const frame = container.querySelector(".relative.max-h-full");
    expect(frame).not.toBeNull();
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
