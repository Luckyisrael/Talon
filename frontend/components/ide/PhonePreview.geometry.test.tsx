import { describe, expect, it, vi } from "vitest";
import { render } from "@testing-library/react";
import PhonePreview from "./PhonePreview";

// The module graph: PhonePreview calls openMetroStream() and expects an
// unsubscribe handle back, plus createAuthWebSocket() instances that are
// closeable. jsdom has no WebCodecs, so decoder teardown is guarded in the
// component itself; the mocks here only stand in for the network.
vi.mock("@/lib/api", () => ({
  createAuthWebSocket: vi.fn(() => ({
    close: vi.fn(),
    send: vi.fn(),
    readyState: 0,
  })),
  sessions: { subscribeVideo: vi.fn() },
  exportApi: { uploadAsset: vi.fn() },
  // openMetroStream returns an unsubscribe FUNCTION (not an object handle) —
  // it is `() => es.close()`. The effect calls it directly on teardown.
  openMetroStream: vi.fn(() => () => {}),
}));
vi.mock("@/lib/auth", () => ({ getToken: () => "test-token" }));
vi.mock("@/components/ui/LoadingState", () => ({
  default: ({ label }: { label: string }) => <div>{label}</div>,
}));

describe("PhonePreview frame geometry", () => {
  it("holds a phone-shaped fallback box until the stream reports real dims", () => {
    const { container } = render(
      <div style={{ width: 348, height: 800 }}>
        <PhonePreview status="streaming" sessionId={1} projectId={1} />
      </div>,
    );
    // The streaming branch renders immediately; frame dims arrive from the
    // stream later. Before the first frame the sizer is the fallback box —
    // a placeholder phone aspect, so the rail never looks empty while the
    // device warms up, and the layout does not jump when the real dims land.
    const frame = container.querySelector(".relative.max-h-full");
    expect(frame).not.toBeNull();
    const style = (frame as HTMLElement).getAttribute("style") || "";
    // Before the first frame the device still has a shape: the Seeker's own
    // 1080×2400 ratio, drawn at full size. The boot state is a device that
    // has not finished booting, not a small placeholder box.
    expect(style).toContain("aspect-ratio: 1080 / 2400");
    expect(style).toContain("width: 100%");
    // The fallback keeps phone proportions so the transition to the live
    // frame is a resize, not a reshaping.
    expect(1080 / 2400).toBeCloseTo(9 / 20, 2);
  });
});
