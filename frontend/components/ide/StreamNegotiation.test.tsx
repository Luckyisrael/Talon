/**
 * DV3: the stream negotiation and the two decode paths.
 *
 * The server's first frame is text — {"mode":"h264","w":..,"h":..} or
 * {"mode":"jpeg"} — and the client's behavior diverges from there: h264
 * configures a WebCodecs VideoDecoder (stubbed here) and feeds it
 * start-code-classified chunks; jpeg keeps the bitmap path.
 */
import { act, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import PhonePreview from "./PhonePreview";
import { createAuthWebSocket } from "@/lib/api";

vi.mock("@/lib/api", () => ({
  createAuthWebSocket: vi.fn(),
  sessions: { screenshot: vi.fn() },
  exportApi: {},
  openMetroStream: vi.fn().mockReturnValue(() => {}),
}));
vi.mock("@/lib/auth", () => ({ getToken: () => "t" }));

type Chunk = { type: string; timestamp: number; data: Uint8Array };

let decoded: Chunk[] = [];
let configured: unknown = null;

// The WebCodecs stub: jsdom has none; a fake decoder observes the exact
// calls the component makes.
class FakeDecoder {
  state = "unconfigured";
  decodeQueueSize = 0;
  constructor(
    public handlers: { output: (f: unknown) => void; error: (e: unknown) => void },
  ) {}
  configure(cfg: unknown) {
    configured = cfg;
    this.state = "configured";
  }
  decode(chunk: Chunk) {
    decoded.push(chunk);
  }
  close() {
    this.state = "closed";
  }
}

const wsCases: Array<{
  name: string;
  frames: () => void;
}> = [];

function makeWs() {
  const ws = {
    onmessage: null as ((e: { data: unknown }) => void) | null,
    onopen: null,
    onclose: null,
    close: vi.fn(),
    send: vi.fn(),
    readyState: 0,
  } as unknown as WebSocket & { onmessage: (e: { data: unknown }) => void };
  return ws;
}

beforeEach(() => {
  decoded = [];
  configured = null;
  vi.mocked(createAuthWebSocket).mockReset();
  // @ts-expect-error stubbing the browser API
  window.VideoDecoder = FakeDecoder;
  // @ts-expect-error stubbing
  window.EncodedVideoChunk = class {
    constructor(public init: Chunk) {
      Object.assign(this, init);
    }
  };
});

describe("DV3 stream negotiation", () => {
  it("configures the h264 decoder on the mode frame and classifies chunks", async () => {
    const videoWs = makeWs();
    const inputWs = makeWs();
    vi.mocked(createAuthWebSocket).mockImplementation((path: string) =>
      path.includes("/stream") ? (videoWs as WebSocket) : (inputWs as WebSocket),
    );

    render(
      <PhonePreview status="streaming" sessionId={7} projectId={1} adbTarget="t" />,
    );

    // The negotiation frame: h264 at 720×1600.
    act(() => {
      videoWs.onmessage({ data: JSON.stringify({ mode: "h264", w: 720, h: 1600 }) });
    });
    await waitFor(() =>
      expect((configured as { codedWidth?: number }).codedWidth).toBe(720),
    );

    // An Annex B blob: one SPS-prefixed chunk (key) and one delta.
    // 00 00 00 01 67 (SPS, nal 7) ... then 00 00 01 41 (delta slice).
    const nal = new Uint8Array([
      0, 0, 0, 1, 0x67, 0x64, 0x00,
      0, 0, 1, 0x41, 0x9a, 0x02,
    ]);
    const blob = new Blob([nal]);
    act(() => {
      videoWs.onmessage({ data: blob });
    });

    await waitFor(() => expect(decoded.length).toBe(2));
    // The SPS chunk is a key chunk; the trailing slice is a delta.
    expect(decoded[0].type).toBe("key");
    expect(decoded[1].type).toBe("delta");
    void wsCases;
  });

  it("keeps the jpeg bitmap path when the server says jpeg", async () => {
    const videoWs = makeWs();
    const inputWs = makeWs();
    vi.mocked(createAuthWebSocket).mockImplementation((path: string) =>
      path.includes("/stream") ? (videoWs as WebSocket) : (inputWs as WebSocket),
    );

    render(
      <PhonePreview status="streaming" sessionId={7} projectId={1} adbTarget="t" />,
    );

    act(() => {
      videoWs.onmessage({ data: JSON.stringify({ mode: "jpeg" }) });
    });

    // No decoder is configured on the jpeg path.
    await new Promise((r) => setTimeout(r, 10));
    expect(configured).toBeNull();

    // A jpeg frame goes through createImageBitmap — an opaque 1×1.
    const bmpStub = vi.fn();
    // jsdom's createImageBitmap may be missing; stub it.
    // @ts-expect-error stubbing
    window.createImageBitmap = async () => {
      bmpStub();
      return {
        width: 1,
        height: 1,
        close() {},
      };
    };
    await act(async () => {
      await videoWs.onmessage({ data: new Blob([new Uint8Array([0xff, 0xd8])]) });
    });
    expect(bmpStub).toHaveBeenCalled();
  });
});
