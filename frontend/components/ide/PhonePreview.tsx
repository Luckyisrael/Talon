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

  // ── Connections ──
  useEffect(() => {
    if (status !== "streaming" || !sessionId) return;

    let closed = false;

    const videoWs = createAuthWebSocket(`/v1/sessions/${sessionId}/stream`);
    if (videoWs) {
      videoWsRef.current = videoWs;
      videoWs.onmessage = (event) => {
        if (!(event.data instanceof Blob)) return;
        createImageBitmap(event.data)
          .then((bmp) => {
            const canvas = canvasRef.current;
            if (!canvas || closed) {
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
          })
          .catch(() => {
            // A single corrupt frame is not worth tearing the stream down.
          });
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
        a.download = `seeker-${sessionId}-${Date.now()}.png`;
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

  return (
    <section className="relative flex h-full min-h-0 flex-col overflow-hidden bg-void">
      <div className="relative flex min-h-0 flex-1 items-center justify-center p-4">
        {status === "streaming" ? (
          <div
            className="relative max-h-full"
            style={
              frame
                ? landscape
                  ? { aspectRatio: `${frame.w} / ${frame.h}`, width: "100%", maxHeight: "100%" }
                  : { aspectRatio: `${frame.w} / ${frame.h}`, height: "100%", maxWidth: "100%" }
                : { width: 180, height: 390 }
            }
          >
            {/* ── Device frame ──
                Uniform bezel, generous uniform radius, grounding shadow, edge
                highlight. Physical keys ride the frame edge as graphics. The
                punch-hole sits over the screen top — the stream already carries
                the device's own status bar, which is never redrawn. */}
            <div
              className="absolute inset-0 rounded-[30px] border border-smoke bg-gradient-to-b from-obsidian via-carbon to-obsidian"
              style={{ boxShadow: "0 24px 64px -16px rgba(0,0,0,0.7), inset 0 1px 0 rgba(255,255,255,0.06)" }}
              aria-hidden
            >
              {/* side keys — frame graphics, not controls */}
              <span className="absolute -right-[2.5px] top-28 h-12 w-[3px] rounded-full bg-smoke" />
              <span className="absolute -left-[2.5px] top-24 h-10 w-[3px] rounded-full bg-smoke" />
              <span className="absolute -left-[2.5px] top-36 h-10 w-[3px] rounded-full bg-smoke" />
            </div>

            {/* screen */}
            <div className="absolute inset-[10px] overflow-hidden rounded-[20px] bg-black">
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
              {/* punch-hole camera over the screen top */}
              <span
                aria-hidden
                className="pointer-events-none absolute left-1/2 top-[10px] size-2.5 -translate-x-1/2 rounded-full bg-black"
                style={{ boxShadow: "inset 0 0 0 1px rgba(255,255,255,0.08), 0 0 0 3px rgba(0,0,0,0.55)" }}
              />
              {/* top strip — target and frame rate over a scrim */}
              <div className="pointer-events-none absolute inset-x-0 top-0 flex items-center justify-between gap-2 bg-gradient-to-b from-black/70 via-black/30 to-transparent px-3 pb-6 pt-2.5">
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
              <div className="pointer-events-none absolute inset-x-0 bottom-0 bg-gradient-to-t from-black/70 via-black/30 to-transparent px-3 pb-2.5 pt-8">
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
            </div>
          </div>
        ) : (
          <div className="flex max-w-[240px] flex-col items-center text-center">
            {status === "connecting" ? (
              <>
                <LoadingState label="Booting a device" />
                <p className="text-sm text-fog mt-3">
                  Attaching takes a moment the first time.
                </p>
              </>
            ) : status === "error" ? (
              <>
                <p className="text-sm text-coral-red">Could not reach a device.</p>
                <p className="mt-1 font-mono text-xs text-fog">
                  The container may still be starting, or adb lost the target.
                </p>
                {onStartSession && (
                  <button type="button" onClick={onStartSession} className="btn mt-3">
                    Try again
                  </button>
                )}
              </>
            ) : (
              <>
                <p className="text-sm text-mist">No device attached.</p>
                <p className="mt-1 font-mono text-xs text-fog">
                  Start one to run the app, tap through it, and let the agent see
                  what it looks like.
                </p>
                {onStartSession && (
                  <button type="button" onClick={onStartSession} className="btn btn-go mt-3">
                    Start a device
                  </button>
                )}
              </>
            )}
          </div>
        )}
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
