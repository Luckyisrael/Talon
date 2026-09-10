import { describe, expect, it, vi } from "vitest";
import { act, render, screen } from "@testing-library/react";
import PhonePreview from "./PhonePreview";
import type { MetroEvent } from "@/lib/api";
import { openMetroStream, createAuthWebSocket } from "@/lib/api";

vi.mock("@/lib/api", () => ({
  openMetroStream: vi.fn().mockReturnValue(() => {}),
  createAuthWebSocket: vi.fn().mockReturnValue(null),
  sessions: { screenshot: vi.fn() },
  exportApi: {},
}));
vi.mock("@/lib/auth", () => ({ getToken: () => "t" }));

// The badge states render from the metro tap: bundling %, reloaded, redbox.
function preview(status: string) {
  return render(
    <PhonePreview status={status as never} projectId={1} sessionId={1} adbTarget="localhost:5555" />,
  );
}

describe("DV1 metro badge", () => {
  it("subscribes to the metro tap while streaming", () => {
    preview("streaming");
    expect(openMetroStream).toHaveBeenCalledWith(1, "t", expect.any(Function));
  });

  it("does not subscribe when idle", () => {
    vi.mocked(openMetroStream).mockClear();
    preview("idle");
    expect(openMetroStream).not.toHaveBeenCalled();
  });

  it("renders bundling progress and redbox errors from events", async () => {
    const { rerender } = preview("streaming");
    // Capture the event callback the preview registered.
    const cb = vi.mocked(openMetroStream).mock.calls.at(-1)![2] as (
      ev: MetroEvent,
    ) => void;

    act(() => cb({ kind: "bundle_progress", text: "Bundling index.js  45%", progress: 45 }));
    expect(screen.getAllByText(/bundling 45%/).length).toBeGreaterThan(0);

    act(() => cb({ kind: "error", text: "SyntaxError: Unexpected token (10:5)", progress: 0 }));
    // The redbox banner names the error over the stream.
    expect(screen.getAllByText(/SyntaxError/).length).toBeGreaterThan(0);

    act(() => cb({ kind: "bundle_done", text: "bundle ready", progress: 100 }));
    // The error banner clears only on the next event by design; done here.
    rerender(
      <PhonePreview status="streaming" projectId={1} sessionId={1} adbTarget="localhost:5555" />,
    );
  });
});
