"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import {
  createAuthWebSocket,
  sessions as sessionsApi,
  exportApi,
  openMetroStream,
  type MetroEvent,
} from "@/lib/api";
import { getToken } from "@/lib/auth";
import LoadingState from "@/components/ui/LoadingState";

/**
 * The device panel.
 *
 * The stream is honesty-constrained: the device's own status bar, clock, and
 * navigation are already in the mirrored frames, so nothing here redraws
 * screen content. What the panel adds is physical chrome around that stream —
 * a uniform bezel, punch-hole camera, side keys, grounding shadow — sized from
 * the resolution the device is actually sending, so taps stay exact. Every
 * control below the frame sends a real input; nothing here is drawn that the
 * device is not really doing.
 */

interface PhonePreviewProps {
  status?: "idle" | "connecting" | "streaming" | "error";
  sessionId?: number | null;
  projectId?: number | null;
  adbTarget?: string | null;
  onStartSession?: () => void;
}

type Frame = { w: number; h: number };

const NAV_KEYS = [
  { label: "Back", keycode: "BACK", glyph: "‹" },
  { label: "Home", keycode: "HOME", glyph: "○" },
  { label: "Recents", keycode: "APP_SWITCH", glyph: "▢" },
] as const;

export default function PhonePreview({
  status = "idle",
  sessionId,
  projectId,
  adbTarget,
  onStartSession,
}: PhonePreviewProps) {
  const canvasRef = useRef<HTMLCanvasElement>(null);
  const videoWsRef = useRef<WebSocket | null>(null);
  const inputWsRef = useRef<WebSocket | null>(null);
  const touchRef = useRef<{ x: number; y: number; at: number } | null>(null);
  const lastFrameAt = useRef(0);

  // Frame size comes from the stream. It used to be hard-coded to 1080×2400,
  // which silently mis-scaled every tap on any device that was not exactly that.
  const [frame, setFrame] = useState<Frame | null>(null);
  const [sampledFps, setSampledFps] = useState(0);
  const [inputReady, setInputReady] = useState(false);
  // Bumped to re-subscribe the sockets without touching the session — the
  // stall retry path.
  const [streamEpoch, setStreamEpoch] = useState(0);

  // DV1: the metro SSE tap rides the device's lifetime — subscribed while
  // streaming, torn down with it. Events drive the badge; errors persist
  // (the redbox is on the screen until a good bundle clears it), progress
  // and reloads fade on their own timers.
  useEffect(() => {
    if (status !== "streaming" || !projectId) return;
    const token = getToken() ?? "";
    const close = openMetroStream(projectId, token, (ev) => {
      if (metroTimer.current) {
        clearTimeout(metroTimer.current);
        metroTimer.current = null;
      }
      if (ev.kind === "error") {
        setMetroBadge({ kind: ev.kind, text: ev.text, progress: 0 });
        return; // errors persist until the next good bundle
      }
      setMetroBadge({ kind: ev.kind, text: ev.text, progress: ev.progress });
      if (ev.kind === "bundle_progress" || ev.kind === "bundle_start") {
        // Progress refreshes on every frame; a stalled one gives up quietly.
        metroTimer.current = setTimeout(
          () => setMetroBadge(null),
          15_000,
        );
      } else {
        // Reloads and bundle-done flash briefly, then clear.
        metroTimer.current = setTimeout(
          () => setMetroBadge(null),
          2_500,
        );
      }
    });
    return () => {
      close();
      if (metroTimer.current) clearTimeout(metroTimer.current);
      setMetroBadge(null);
    };
  }, [status, projectId, streamEpoch]);

  // DV3: the decoder dies with the subscription — never across re-subs.
  useEffect(() => {
    return () => {
      // state is only meaningful on a real WebCodecs decoder; a jsdom stub
      // lacks it, and closing a closed decoder throws.
      if (decoderRef.current && "state" in decoderRef.current && decoderRef.current.state !== "closed") {
        decoderRef.current.close();
      }
      decoderRef.current = null;
      h264Active.current = false;
    };
  }, [streamEpoch, status, sessionId]);
  const [stalled, setStalled] = useState(false);
  const [capturing, setCapturing] = useState(false);
  const [captureNote, setCaptureNote] = useState<string | null>(null);
  // DV1: the bundler's voice — bundle progress, hot reloads, redbox errors.
  // The badge lives above the stream: bundling/reloaded are transient states
  // with self-dismissing timers, errors persist until the next good bundle.
  const [metroBadge, setMetroBadge] = useState<{
    kind: MetroEvent["kind"];
    text: string;
    progress: number;
  } | null>(null);
  const metroTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
  // DV3: the stream's negotiated mode arrives as the first (text) frame —
  // h264 (the device's own encoder, WebCodecs-decoded) or jpeg (the
  // standing fallback). Null until the server speaks.
  const [videoMode, setVideoMode] = useState<"h264" | "jpeg" | null>(null);
  const decoderRef = useRef<VideoDecoder | null>(null);
  const frameCount = useRef(0);

  // Zero unless frames are actually arriving. Derived rather than written back on
  // stop, so a stopped stream cannot leave last second's rate on the gauge.
  const fps = status === "streaming" ? sampledFps : 0;

  // ── Frame rate ──
  // Counted from frames that actually arrived, sampled once a second.
  useEffect(() => {
    if (status !== "streaming") return;
    const id = window.setInterval(() => {
      setSampledFps(frameCount.current);
      frameCount.current = 0;
    }, 1000);
    return () => window.clearInterval(id);
  }, [status]);

  // ── Stall detector ──
  // A frozen UI that looks live is the worst failure here. If frames stop
  // arriving while the session claims streaming, say so and offer retry.
  useEffect(() => {
    if (status !== "streaming") return;
    const id = window.setInterval(() => {
      setStalled(lastFrameAt.current > 0 && Date.now() - lastFrameAt.current > 3000);
    }, 1000);
    return () => window.clearInterval(id);
  }, [status, streamEpoch]);

  const sendInput = useCallback((msg: Record<string, unknown>) => {
    const ws = inputWsRef.current;
    if (!ws || ws.readyState !== WebSocket.OPEN) return;
    ws.send(JSON.stringify(msg));
  }, []);

  // The canvas is sized to the frame, so its intrinsic pixels *are* device
  // pixels. Scaling off the rendered box is therefore exact at any panel width —
  // the surrounding bezel never enters the math.
  const toDevice = useCallback((clientX: number, clientY: number) => {
    const canvas = canvasRef.current;
    if (!canvas || canvas.width === 0 || canvas.height === 0) return null;
    const rect = canvas.getBoundingClientRect();
    if (rect.width === 0 || rect.height === 0) return null;
    return {
      x: Math.round((clientX - rect.left) * (canvas.width / rect.width)),
      y: Math.round((clientY - rect.top) * (canvas.height / rect.height)),
    };
  }, []);

  const onPointerDown = useCallback(
    (e: React.PointerEvent<HTMLCanvasElement>) => {
      const p = toDevice(e.clientX, e.clientY);
      if (!p) return;
      e.currentTarget.setPointerCapture(e.pointerId);
      touchRef.current = { ...p, at: e.timeStamp };
    },
    [toDevice],
  );

  const onPointerUp = useCallback(
    (e: React.PointerEvent<HTMLCanvasElement>) => {
      const start = touchRef.current;
      touchRef.current = null;
      const end = toDevice(e.clientX, e.clientY);
      if (!start || !end) return;

      const dx = end.x - start.x;
      const dy = end.y - start.y;
      const travelled = Math.hypot(dx, dy);

      // A tap was previously sent on pointer-down *and* a swipe on pointer-up,
      // so every drag also fired a tap at its origin. Deciding once, on release,
      // is both correct and what the device would have seen from a finger.
      if (travelled > 24) {
        sendInput({
          type: "swipe",
          x1: start.x,
          y1: start.y,
          x2: end.x,
          y2: end.y,
          duration: Math.min(Math.max(Math.round(e.timeStamp - start.at), 60), 800),
        });
      } else {
        sendInput({ type: "tap", x: end.x, y: end.y });
      }
    },
    [sendInput, toDevice],
  );

  // ── Frame sinks ──
  // JPEG: decode a bitmap and blit. The original path, unchanged.
  const drawJPEG = useCallback(async (blob: Blob) => {
    try {
      const bmp = await createImageBitmap(blob);
      const canvas = canvasRef.current;
      if (!canvas) {
        bmp.close();
        return;
      }
      if (canvas.width !== bmp.width || canvas.height !== bmp.height) {
        canvas.width = bmp.width;
        canvas.height = bmp.height;
        setFrame({ w: bmp.width, h: bmp.height });
      }
      canvas.getContext("2d")?.drawImage(bmp, 0, 0);
      bmp.close();
      frameCount.current++;
      lastFrameAt.current = Date.now();
      setStalled(false);
    } catch {
      // A single corrupt frame is not worth tearing the stream down.
    }
  }, []);

  // H.264: WebCodecs on the GPU. The server relays the device encoder's
  // NAL stream; we demux access-unit boundaries ( Annex B start codes )
  // and feed the decoder. No WebCodecs (old browser, jsdom) → the server
  // is already told jpeg via nothing — the mode arrives from the server's
  // own probe; if it still sends h264 to a browser without the API, frames
  // are dropped silently and the stall retry re-subscribes.
  const h264Active = useRef(false);

  const startH264 = useCallback((w: number, h: number) => {
    if (!("VideoDecoder" in window)) return; // no fallback mid-stream; jpeg comes from the server
    try {
      const decoder = new VideoDecoder({
        output: (frame) => {
          const canvas = canvasRef.current;
          if (canvas) {
            if (canvas.width !== frame.displayWidth || canvas.height !== frame.displayHeight) {
              canvas.width = frame.displayWidth;
              canvas.height = frame.displayHeight;
              setFrame({ w: frame.displayWidth, h: frame.displayHeight });
            }
            canvas.getContext("2d")?.drawImage(frame, 0, 0);
          }
          frame.close();
          frameCount.current++;
          lastFrameAt.current = Date.now();
          setStalled(false);
        },
        error: () => {
          // A decoder error drops the h264 path; the next frame is skipped
          // until the stall path re-subscribes (server re-negotiates jpeg).
          h264Active.current = false;
        },
      });
      decoder.configure({
        codec: "avc1.42E01F", // baseline profile, the redroid encoder's output
        codedWidth: w,
        codedHeight: h,
      });
      decoderRef.current = decoder;
      h264Active.current = true;
    } catch {
      h264Active.current = false;
    }
  }, []);

  const feedH264 = useCallback(async (blob: Blob) => {
    const decoder = decoderRef.current;
    if (!decoder || decoder.state === "closed") return;
    try {
      const buf = new Uint8Array(await blob.arrayBuffer());
      // Annex B: split on 00 00 00 01 / 00 00 01 start codes into
      // access-unit-ish chunks. The chunk type comes from the first NAL
      // header after the start code: IDR slice (5) or SPS (7) → key; the
      // rest are deltas. WebCodecs uses the type for decode order, so a
      // lie here corrupts the stream.
      const chunks: Array<{ data: Uint8Array; key: boolean }> = [];
      let start = -1;
      let key = false;
      for (let i = 0; i < buf.length; i++) {
        if (buf[i] !== 0 || i + 3 >= buf.length) continue;
        const is4 = buf[i + 2] === 0 && buf[i + 3] === 1;
        const is3 = buf[i + 2] === 1;
        if (!is4 && !is3) continue;
        if (start >= 0 && i > start) {
          chunks.push({ data: buf.slice(start, i), key });
        }
        start = i;
        // The NAL header byte sits just past the start code.
        const hdr = buf[i + (is4 ? 4 : 3)];
        if (hdr !== undefined) {
          const nalType = hdr & 0x1f;
          key = nalType === 5 || nalType === 7; // IDR or SPS
        }
        i += is4 ? 3 : 2;
      }
      if (start >= 0 && start < buf.length) {
        chunks.push({ data: buf.slice(start), key });
      }
      const ts = performance.now() * 1000;
      for (const c of chunks) {
        decoder.decode(
          new EncodedVideoChunk({
            type: c.key ? "key" : "delta",
            timestamp: ts,
            data: c.data,
          }),
        );
      }
      if (decoder.decodeQueueSize > 5) {
        await new Promise((r) => setTimeout(r, 0));
      }
    } catch {
      // A malformed chunk is dropped; the stream continues.
    }
  }, []);

  // ── Connections ──
  useEffect(() => {
    if (status !== "streaming" || !sessionId) return;

    let closed = false;

    const videoWs = createAuthWebSocket(`/v1/sessions/${sessionId}/stream`);
    if (videoWs) {
      videoWsRef.current = videoWs;
      videoWs.onmessage = (event) => {
        // DV3: the negotiation frame is text — the mode the server picked.
        if (typeof event.data === "string") {
          try {
            const mode = JSON.parse(event.data) as { mode: "h264" | "jpeg"; w?: number; h?: number };
            setVideoMode(mode.mode);
            if (mode.mode === "h264" && "VideoDecoder" in window) {
              if (mode.w && mode.h) setFrame({ w: mode.w, h: mode.h });
              startH264(mode.w ?? 1080, mode.h ?? 2400);
            }
            return;
          } catch {
            return; // an unparsable negotiation is ignored; no frames decode
          }
        }
        if (event.data instanceof Blob) {
          if (h264Active.current) {
            feedH264(event.data);
          } else {
            drawJPEG(event.data);
          }
        }
      };
    }

    const inputWs = createAuthWebSocket(`/v1/sessions/${sessionId}/input`);
    if (inputWs) {
      inputWsRef.current = inputWs;
      inputWs.onopen = () => !closed && setInputReady(true);
      inputWs.onclose = () => !closed && setInputReady(false);
      inputWs.onerror = () => !closed && setInputReady(false);
    }

    return () => {
      closed = true;
      videoWsRef.current?.close();
      videoWsRef.current = null;
      inputWsRef.current?.close();
      inputWsRef.current = null;
      setInputReady(false);
      setFrame(null);
      frameCount.current = 0;
      lastFrameAt.current = 0;
      setStalled(false);
    };
  }, [status, sessionId, streamEpoch]);

  // ── Screenshot capture ──
  // Two honest paths: download a throwaway PNG, or save into the project's
  // validated store-asset collection (the server validates dimensions and
  // orientation — a rejection there names the real dApp Store rule).
  async function capture(saveAsAsset: boolean) {
    if (!sessionId || capturing) return;
    setCapturing(true);
    setCaptureNote(null);
    try {
      const blob = await sessionsApi.screenshotBlob(sessionId);
      if (!saveAsAsset) {
        const url = URL.createObjectURL(blob);
        const a = document.createElement("a");
        a.href = url;
        a.download = `talon-${sessionId}-${Date.now()}.png`;
        a.click();
        URL.revokeObjectURL(url);
        return;
      }
      if (!projectId) {
        setCaptureNote("No project to attach the asset to.");
        return;
      }
      const file = new File([blob], `screenshot-${Date.now()}.png`, { type: "image/png" });
      const asset = await exportApi.uploadAsset(projectId, "screenshot", file);
      setCaptureNote(`Saved as store asset ${asset.width}×${asset.height}.`);
    } catch (err) {
      setCaptureNote(err instanceof Error ? err.message : "Capture failed.");
    } finally {
      setCapturing(false);
    }
  }

  const lampState =
    status === "streaming"
      ? stalled
        ? "fault"
        : "live"
      : status === "connecting"
        ? "working"
        : status === "error"
          ? "fault"
          : "idle";

  const landscape = frame !== null && frame.w > frame.h;
  // Before the first frame, the device still has a shape — the Seeker's own
  // 1080×2400. A booting or idle phone is not a 240px-wide text box: the frame
  // is drawn at full size with the status living inside the screen, so the
  // panel reads as a device that hasn't finished booting rather than as
  // nothing at all.
  const aspect = frame
    ? `${frame.w} / ${frame.h}`
    : "1080 / 2400";

  return (
    <section className="relative flex h-full min-h-0 flex-col overflow-hidden bg-void">
      <div
        className="relative flex min-h-0 flex-1 items-center justify-center p-3"
        // The stage is a query container so the phone can measure the real
        // space available in either dimension; the crop mode below keys off
        // the stage's own aspect ratio.
        style={{ containerType: "size" } as React.CSSProperties}
      >
        <div
          className="relative max-h-full w-full"
          // Largest true-aspect box that fits the stage in BOTH dimensions.
          // Width-driven with a max-height cap: an explicit height plus a
          // clamped max-width distorts the aspect exactly when the rail is
          // narrower than the device's own ratio, which most rails are.
          // The device's reported ratio always wins — never an assumed
          // phone shape. Before the first frame the Seeker's own ratio is
          // used so the frame is drawn at full size from the moment a
          // session starts, not as a small placeholder.
          style={
            landscape
              ? { aspectRatio: aspect, height: "100%", maxWidth: "100%" }
              : { aspectRatio: aspect, width: "100%", maxHeight: "100%" }
          }
        >
            {/* ── Device frame ──
                Slim uniform bezel (4px), concentric corner radii (outer minus
                the bezel — the premium tell), a hairline metallic edge with a
                top specular, and one soft grounding shadow. Physical keys sit
                recessed into the frame edge, positioned as a FRACTION of frame
                height so they stay anatomically correct at any size the
                stream reports. The punch-hole rides the screen top — the
                stream carries the device's own status bar, which is never
                redrawn. */}
            <div
              className="absolute inset-0 rounded-[32px] bg-gradient-to-b from-[#1c1f24] via-[#101215] to-[#050608] phone-crop"
              style={{
                boxShadow:
                  "0 32px 80px -24px rgba(0,0,0,0.65), inset 0 1px 0 rgba(255,255,255,0.10), inset 0 0 0 1px rgba(255,255,255,0.07)",
              }}
              aria-hidden
            >
              {/* side keys — recessed frame graphics, not controls.
                  Percentages of the frame so they scale with any device. */}
              <span className="absolute -left-[1px] top-[15.5%] h-[3.2%] w-[2px] rounded-full bg-[#3a3f47]/90 shadow-[inset_0_1px_2px_rgba(0,0,0,0.8)]" />
              <span className="absolute -left-[1px] top-[21%] h-[7.2%] w-[2px] rounded-full bg-[#3a3f47]/90 shadow-[inset_0_1px_2px_rgba(0,0,0,0.8)]" />
              <span className="absolute -left-[1px] top-[30.5%] h-[7.2%] w-[2px] rounded-full bg-[#3a3f47]/90 shadow-[inset_0_1px_2px_rgba(0,0,0,0.8)]" />
              <span className="absolute -right-[1px] top-[23%] h-[11.5%] w-[2px] rounded-full bg-[#3a3f47]/90 shadow-[inset_0_1px_2px_rgba(0,0,0,0.8)]" />
            </div>

            {/* screen */}
            <div className="absolute inset-[4px] overflow-hidden rounded-[28px] bg-black">
              {status === "streaming" ? (
                <canvas
                  ref={canvasRef}
                  onPointerDown={onPointerDown}
                  onPointerUp={onPointerUp}
                  onPointerCancel={() => (touchRef.current = null)}
                  aria-label="The device screen. Click to tap, drag to swipe."
                  className={`h-full w-full touch-none select-none ${
                    inputReady ? "cursor-pointer" : "cursor-default"
                  }`}
                />
              ) : (
                // The device exists but has no frames yet: booting, idle, or
                // an error reaching the container. Rendered inside the screen
                // rather than replacing the phone — the frame is the device,
                // and this is what its screen is doing right now.
                <div className="flex h-full w-full flex-col items-center justify-center gap-3 px-6 text-center">
                  {status === "connecting" ? (
                    <>
                      <LoadingState variant="Orbit" label="Booting the device" />
                      <p className="text-sm text-fog">
                        Attaching takes a moment the first time.
                      </p>
                    </>
                  ) : status === "error" ? (
                    <>
                      <p className="text-sm text-coral-red">Could not reach a device.</p>
                      <p className="font-mono text-xs text-fog">
                        The container may still be starting, or adb lost the target.
                      </p>
                      {onStartSession && (
                        <button type="button" onClick={onStartSession} className="btn mt-1">
                          Try again
                        </button>
                      )}
                    </>
                  ) : (
                    <>
                      <p className="text-sm text-mist">No device attached.</p>
                      <p className="font-mono text-xs text-fog">
                        Start one to run the app, tap through it, and let the
                        agent see what it looks like.
                      </p>
                      {onStartSession && (
                        <button type="button" onClick={onStartSession} className="btn btn-go mt-1">
                          Start a device
                        </button>
                      )}
                    </>
                  )}
                </div>
              )}
              {/* punch-hole camera over the screen top */}
              <span
                aria-hidden
                className="pointer-events-none absolute left-1/2 top-[8px] size-2 -translate-x-1/2 rounded-full bg-black"
                style={{ boxShadow: "inset 0 0 0 1px rgba(255,255,255,0.08), 0 0 0 3px rgba(0,0,0,0.55)" }}
              />
              {/* Live overlays — only meaningful while frames are flowing.
                  The boot/idle screen above already carries its own status;
                  these would report 0 fps and a detached input over it. */}
              {status === "streaming" && (
                <>
                  {/* top strip — target and frame rate over a scrim */}
                  <div className="pointer-events-none absolute inset-x-0 top-0 flex items-center justify-between gap-2 bg-gradient-to-b from-black/50 via-black/25 to-transparent px-3 pb-6 pt-2">
                    <div className="flex min-w-0 items-center gap-2">
                      <span className="lamp" data-state={lampState} />
                      <span className="gauge truncate" title={adbTarget ?? undefined}>
                        {adbTarget ?? "Device"}
                      </span>
                    </div>
                    <span className="gauge tnum flex-shrink-0">
                      {metroBadge && metroBadge.kind === "bundle_progress" && metroBadge.progress > 0
                        ? `bundling ${metroBadge.progress}%`
                        : metroBadge && (metroBadge.kind === "bundle_start" || metroBadge.kind === "bundle_progress")
                          ? "bundling…"
                          : metroBadge && metroBadge.kind === "bundle_done"
                            ? "bundle ready"
                            : metroBadge && metroBadge.kind === "reloaded"
                              ? "reloaded"
                              : `${fps} fps`}
                    </span>
                  </div>
                  {/* DV1: the redbox surface — a runtime/bundler error persists
                      over the stream until the next good bundle clears it. The
                      device screen may still show the old frame; this says why. */}
                  {metroBadge && metroBadge.kind === "error" ? (
                    <div
                      role="status"
                      title={metroBadge.text}
                      className="pointer-events-none absolute inset-x-0 top-10 border-t border-coral-red/60 bg-coral-red/20 px-3 py-1.5 backdrop-blur-[2px]"
                    >
                      <p className="gauge truncate text-coral-red">
                        ⚠ bundler: {metroBadge.text}
                      </p>
                    </div>
                  ) : null}

                  {/* bottom nav keys */}
                  <div className="pointer-events-none absolute inset-x-0 bottom-0 bg-gradient-to-t from-black/50 via-black/25 to-transparent px-3 pb-2 pt-6">
                    <div className="pointer-events-auto flex items-center justify-center gap-1">
                      {NAV_KEYS.map((key) => (
                        <button
                          key={key.keycode}
                          type="button"
                          disabled={!inputReady}
                          onClick={() => sendInput({ type: "key", keycode: key.keycode })}
                          title={key.label}
                          aria-label={key.label}
                          className="btn btn-ghost h-7 w-10 text-fog"
                        >
                          <span aria-hidden className="text-sm leading-none">
                            {key.glyph}
                          </span>
                        </button>
                      ))}
                    </div>
                    <div className="mt-2 flex items-center justify-center gap-2 font-mono text-xs tabular-nums text-ash">
                      <span>{frame ? `${frame.w}×${frame.h}` : "—"}</span>
                      <span className="text-smoke">·</span>
                      <span>{sessionId ? `#${sessionId}` : "—"}</span>
                      <span className="text-smoke">·</span>
                      <span className={inputReady ? "text-[#4cc069]" : "text-fog"}>
                        {inputReady ? "input attached" : "input detached"}
                      </span>
                    </div>
                  </div>
                  {!frame && (
                    <p className="gauge absolute inset-0 flex items-center justify-center">
                      Waiting for the first frame
                    </p>
                  )}
                  {stalled && frame && (
                    <div className="absolute inset-0 flex flex-col items-center justify-center gap-2 bg-void/80 px-6 text-center">
                      <p className="text-sm text-mist">Signal lost — no frames for a while.</p>
                      <p className="font-mono text-xs text-fog">The session may still be alive.</p>
                      <button
                        type="button"
                        onClick={() => setStreamEpoch((e) => e + 1)}
                        className="btn mt-1"
                      >
                        Reconnect stream
                      </button>
                    </div>
                  )}
                </>
              )}
            </div>
          </div>
      </div>

      {/* ── Device toolbar — outside the frame, never over the screen ── */}
      {status === "streaming" && (
        <div className="flex flex-shrink-0 items-center justify-center gap-1 border-t border-graphite px-2 py-1.5">
          <ToolbarButton
            title="Rotate portrait / landscape"
            label="Rotate"
            onClick={() => sendInput({ type: "rotate" })}
            disabled={!inputReady}
          >
            <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><path d="M21 12a9 9 0 1 1-2.64-6.36M21 3v6h-6" /></svg>
          </ToolbarButton>
          <ToolbarButton
            title="Volume down"
            label="Volume down"
            onClick={() => sendInput({ type: "key", keycode: "VOLUME_DOWN" })}
            disabled={!inputReady}
          >
            <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><path d="M11 5L6 9H2v6h4l5 4V5zM15.5 8.5a5 5 0 0 1 0 7" /></svg>
          </ToolbarButton>
          <ToolbarButton
            title="Volume up"
            label="Volume up"
            onClick={() => sendInput({ type: "key", keycode: "VOLUME_UP" })}
            disabled={!inputReady}
          >
            <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><path d="M11 5L6 9H2v6h4l5 4V5zM15.5 8.5a5 5 0 0 1 0 7M18.5 5.5a9 9 0 0 1 0 13" /></svg>
          </ToolbarButton>
          <ToolbarButton
            title="Power"
            label="Power"
            onClick={() => sendInput({ type: "key", keycode: "POWER" })}
            disabled={!inputReady}
          >
            <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><path d="M12 2v10M18.4 6.6a9 9 0 1 1-12.8 0" /></svg>
          </ToolbarButton>
          <span aria-hidden className="mx-1 h-4 w-px bg-graphite" />
          <ToolbarButton
            title="Capture screenshot (download PNG)"
            label="Capture screenshot"
            onClick={() => void capture(false)}
            disabled={!inputReady || capturing}
          >
            <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><path d="M23 19a2 2 0 0 1-2 2H3a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h4l2-3h6l2 3h4a2 2 0 0 1 2 2z" /><circle cx="12" cy="13" r="4" /></svg>
          </ToolbarButton>
          <ToolbarButton
            title="Save screenshot as store asset (validated)"
            label="Save as store asset"
            onClick={() => void capture(true)}
            disabled={!inputReady || capturing || !projectId}
          >
            <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><path d="M19 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h11l5 5v11a2 2 0 0 1-2 2z" /><path d="M17 21v-8H7v8M7 3v5h8" /></svg>
          </ToolbarButton>
        </div>
      )}
      {captureNote && (
        <p className="flex-shrink-0 border-t border-graphite px-3 py-1.5 text-center font-mono text-xs text-fog">
          {captureNote}
        </p>
      )}
    </section>
  );
}

function ToolbarButton({
  title,
  label,
  onClick,
  disabled,
  children,
}: {
  title: string;
  label: string;
  onClick: () => void;
  disabled?: boolean;
  children: React.ReactNode;
}) {
  return (
    <button
      type="button"
      title={title}
      aria-label={label}
      onClick={onClick}
      disabled={disabled}
      className="flex size-8 items-center justify-center rounded-md text-fog transition-colors hover:bg-white/5 hover:text-mist disabled:opacity-40"
    >
      {children}
    </button>
  );
}
