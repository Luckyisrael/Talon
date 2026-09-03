"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { createAuthWebSocket } from "@/lib/api";
import LoadingState from "@/components/ui/LoadingState";

/**
 * The device panel.
 *
 * There used to be a cartoon phone drawn around the stream: rounded plastic
 * bezel, a hard-coded "9:41" status bar, and three grey circles pretending to be
 * Android's navigation bar. All three were lies — the real device has its own
 * clock in the frames being streamed, and the fake nav dots did nothing when
 * clicked. What replaces them is the frame itself, sized to the resolution the
 * device is actually sending and filling the panel edge to edge, with the adb
 * target, frame rate, navigation keys, and connection readout floated over the
 * letterbox as instruments. The keys send real keyevents; nothing here is drawn
 * that the device is not really doing.
 */

interface PhonePreviewProps {
  status?: "idle" | "connecting" | "streaming" | "error";
  sessionId?: number | null;
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
  adbTarget,
  onStartSession,
}: PhonePreviewProps) {
  const canvasRef = useRef<HTMLCanvasElement>(null);
  const videoWsRef = useRef<WebSocket | null>(null);
  const inputWsRef = useRef<WebSocket | null>(null);
  const touchRef = useRef<{ x: number; y: number; at: number } | null>(null);

  // Frame size comes from the stream. It used to be hard-coded to 1080×2400,
  // which silently mis-scaled every tap on any device that was not exactly that.
  const [frame, setFrame] = useState<Frame | null>(null);
  const [sampledFps, setSampledFps] = useState(0);
  const [inputReady, setInputReady] = useState(false);
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

  const sendInput = useCallback((msg: Record<string, unknown>) => {
    const ws = inputWsRef.current;
    if (!ws || ws.readyState !== WebSocket.OPEN) return;
    ws.send(JSON.stringify(msg));
  }, []);

  // The canvas is sized to the frame, so its intrinsic pixels *are* device
  // pixels. Scaling off the rendered box is therefore exact at any panel width.
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
    };
  }, [status, sessionId]);

  const lampState =
    status === "streaming"
      ? "live"
      : status === "connecting"
        ? "working"
        : status === "error"
          ? "fault"
          : "idle";

  return (
    <section className="relative h-full min-h-0 overflow-hidden bg-void">
      {status === "streaming" ? (
        <>
          {/* The frame is the panel. It scales to fill while keeping the
              device's real aspect ratio; the instruments float over the
              letterbox rather than stealing rows from the frame. */}
          <div className="absolute inset-0 flex items-center justify-center">
            <canvas
              ref={canvasRef}
              onPointerDown={onPointerDown}
              onPointerUp={onPointerUp}
              onPointerCancel={() => (touchRef.current = null)}
              aria-label="The device screen. Click to tap, drag to swipe."
              className={`max-h-full max-w-full touch-none select-none ${
                inputReady ? "cursor-pointer" : "cursor-default"
              }`}
            />
          </div>

          {!frame && (
            <p className="gauge absolute inset-0 flex items-center justify-center">
              Waiting for the first frame
            </p>
          )}

          {/* Top strip — the target and frame rate, over a scrim so they stay
              legible against whatever the app is drawing. */}
          <div className="pointer-events-none absolute inset-x-0 top-0 flex items-center justify-between gap-2 bg-gradient-to-b from-void/90 via-void/45 to-transparent px-3 pb-6 pt-2.5">
            <div className="flex min-w-0 items-center gap-2">
              <span className="lamp" data-state={lampState} />
              <span className="gauge truncate" title={adbTarget ?? undefined}>
                {adbTarget ?? "Device"}
              </span>
            </div>
            <span className="gauge tnum flex-shrink-0">{fps} fps</span>
          </div>

          {/* Bottom strip — real navigation keys and the connection readout.
              Only the keys take pointer events; taps anywhere else fall through
              to the frame. */}
          <div className="pointer-events-none absolute inset-x-0 bottom-0 bg-gradient-to-t from-void/90 via-void/50 to-transparent px-3 pb-2.5 pt-8">
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
        </>
      ) : (
        <div className="absolute inset-0 flex items-center justify-center p-6">
          <div className="max-w-[240px] text-center">
            {status === "connecting" ? (
              <div className="flex flex-col items-center">
                <LoadingState label="Booting a device" />
                <p className="text-sm text-fog mt-3">
                  Attaching takes a moment the first time.
                </p>
              </div>
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
        </div>
      )}
    </section>
  );
}
