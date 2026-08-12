"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { createAuthWebSocket } from "@/lib/api";

/**
 * The device panel.
 *
 * There used to be a cartoon phone drawn around the stream: rounded plastic
 * bezel, a hard-coded "9:41" status bar, and three grey circles pretending to be
 * Android's navigation bar. All three were lies — the real device has its own
 * clock in the frames being streamed, and the fake nav dots did nothing when
 * clicked. What replaces them is the frame itself, sized to the resolution the
 * device is actually sending, above a readout of what that connection is doing,
 * and navigation keys that send real keyevents.
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
    <section className="flex h-full min-h-0 flex-col bg-ink">
      <header className="bay-head justify-between">
        <div className="flex min-w-0 items-center gap-2">
          <span className="lamp" data-state={lampState} />
          {/* The label names the thing it sits above — the adb target when there
              is one, rather than the word "PREVIEW". */}
          <span className="gauge truncate" title={adbTarget ?? undefined}>
            {adbTarget ?? (status === "streaming" ? "Device" : "No device")}
          </span>
        </div>
        {status === "streaming" && (
          <span className="gauge tnum flex-shrink-0">{fps} fps</span>
        )}
      </header>

      <div className="flex min-h-0 flex-1 flex-col items-center justify-center overflow-hidden p-3">
        {status === "streaming" ? (
          <>
            {/* No bezel. The frame is the object — a thin rail and a shadow are
                enough to seat it on the bench. */}
            <div
              className="relative min-h-0 overflow-hidden rounded-2 border border-edge bg-ink shadow-[0_16px_40px_-12px_rgba(0,0,0,0.8)]"
              style={{ aspectRatio: frame ? `${frame.w} / ${frame.h}` : "9 / 19.5", maxHeight: "100%" }}
            >
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
              {!frame && (
                <p className="gauge absolute inset-0 flex items-center justify-center">
                  Waiting for the first frame
                </p>
              )}
            </div>

            {/* Real navigation keys, sending real keyevents. */}
            <div className="mt-3 flex items-center gap-1">
              {NAV_KEYS.map((key) => (
                <button
                  key={key.keycode}
                  type="button"
                  disabled={!inputReady}
                  onClick={() => sendInput({ type: "key", keycode: key.keycode })}
                  title={key.label}
                  aria-label={key.label}
                  className="btn btn-ghost h-7 w-9 text-graphite"
                >
                  <span aria-hidden className="text-body leading-none">
                    {key.glyph}
                  </span>
                </button>
              ))}
            </div>

            {/* Instrument readout — the numbers the connection is really doing. */}
            <dl className="mt-3 grid w-full max-w-[240px] grid-cols-[auto_1fr] gap-x-3 gap-y-1 font-mono text-micro tabular-nums">
              <Readout label="RES" value={frame ? `${frame.w}×${frame.h}` : "—"} />
              <Readout label="SESSION" value={sessionId ? `#${sessionId}` : "—"} />
              <Readout
                label="INPUT"
                value={inputReady ? "attached" : "detached"}
                tone={inputReady ? "signal" : "filament"}
              />
            </dl>
          </>
        ) : (
          <div className="max-w-[240px] text-center">
            {status === "connecting" ? (
              <>
                <span
                  aria-hidden
                  className="mx-auto mb-3 block h-5 w-5 rounded-full border-2 border-probe border-t-transparent"
                  style={{ animation: "spin 0.7s linear infinite" }}
                />
                <p className="text-small text-graphite">
                  Booting a device and attaching to it. This takes a moment the
                  first time.
                </p>
              </>
            ) : status === "error" ? (
              <>
                <p className="text-small text-fault">Could not reach a device.</p>
                <p className="mt-1 text-tiny text-graphite">
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
                <p className="text-small text-paper">No device attached.</p>
                <p className="mt-1 text-tiny text-graphite">
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
    </section>
  );
}

function Readout({
  label,
  value,
  tone,
}: {
  label: string;
  value: string;
  tone?: "signal" | "filament";
}) {
  return (
    <>
      <dt className="text-dust tracking-[0.11em]">{label}</dt>
      <dd
        className={
          tone === "signal"
            ? "text-signal-dim"
            : tone === "filament"
              ? "text-filament"
              : "text-graphite"
        }
      >
        {value}
      </dd>
    </>
  );
}
