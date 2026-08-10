"use client";

import { Mobile, Warning2 } from "iconsax-reactjs";
import { useRef, useEffect, useCallback } from "react";
import { createAuthWebSocket } from "@/lib/api";

const DEVICE_WIDTH = 1080;
const DEVICE_HEIGHT = 2400;

interface PhonePreviewProps {
  status?: "idle" | "connecting" | "streaming" | "error";
  sessionId?: number | null;
  onStartSession?: () => void;
}

export default function PhonePreview({ status = "idle", sessionId, onStartSession }: PhonePreviewProps) {
  const canvasRef = useRef<HTMLCanvasElement>(null);
  const videoWsRef = useRef<WebSocket | null>(null);
  const inputWsRef = useRef<WebSocket | null>(null);
  const touchStateRef = useRef<{ startX: number; startY: number; startTime: number } | null>(null);

  const sendInput = useCallback((msg: Record<string, unknown>) => {
    const ws = inputWsRef.current;
    if (!ws || ws.readyState !== WebSocket.OPEN) return;
    ws.send(JSON.stringify(msg));
  }, []);

  const handlePointerDown = useCallback((e: React.PointerEvent<HTMLCanvasElement>) => {
    const canvas = canvasRef.current;
    if (!canvas) return;

    const rect = canvas.getBoundingClientRect();
    const scaleX = DEVICE_WIDTH / rect.width;
    const scaleY = DEVICE_HEIGHT / rect.height;

    const x = Math.round((e.clientX - rect.left) * scaleX);
    const y = Math.round((e.clientY - rect.top) * scaleY);

    touchStateRef.current = { startX: x, startY: y, startTime: Date.now() };

    // Send immediate tap for responsive feel
    sendInput({ type: "tap", x, y });
  }, [sendInput]);

  const handlePointerUp = useCallback((e: React.PointerEvent<HTMLCanvasElement>) => {
    const canvas = canvasRef.current;
    if (!canvas || !touchStateRef.current) return;

    const rect = canvas.getBoundingClientRect();
    const scaleX = DEVICE_WIDTH / rect.width;
    const scaleY = DEVICE_HEIGHT / rect.height;

    const endX = Math.round((e.clientX - rect.left) * scaleX);
    const endY = Math.round((e.clientY - rect.top) * scaleY);
    const { startX, startY, startTime } = touchStateRef.current;
    const duration = Date.now() - startTime;

    // If dragged significantly and held for a bit, send swipe
    const dx = endX - startX;
    const dy = endY - startY;
    const dist = Math.sqrt(dx * dx + dy * dy);

    if (dist > 20 && duration > 100) {
      sendInput({
        type: "swipe",
        x1: startX,
        y1: startY,
        x2: endX,
        y2: endY,
        duration: Math.min(duration, 500),
      });
    }

    touchStateRef.current = null;
  }, [sendInput]);

  const connectStream = useCallback(() => {
    if (!sessionId) return;

    // Connect to video stream (separate from input)
    if (!videoWsRef.current) {
      const videoWs = createAuthWebSocket(`/v1/sessions/${sessionId}/stream`);
      if (!videoWs) return;
      videoWsRef.current = videoWs;

      videoWs.onmessage = (event) => {
        if (event.data instanceof Blob) {
          const canvas = canvasRef.current;
          if (!canvas) return;

          createImageBitmap(event.data).then((bmp) => {
            canvas.width = bmp.width;
            canvas.height = bmp.height;
            const ctx = canvas.getContext("2d");
            if (ctx) {
              ctx.drawImage(bmp, 0, 0);
            }
            bmp.close();
          }).catch(() => {});
        }
      };

      videoWs.onclose = () => { videoWsRef.current = null; };
      videoWs.onerror = () => { videoWsRef.current = null; };
    }

    // Connect to input stream (separate from video)
    if (!inputWsRef.current) {
      const inputWs = createAuthWebSocket(`/v1/sessions/${sessionId}/input`);
      if (!inputWs) return;
      inputWsRef.current = inputWs;

      inputWs.onclose = () => { inputWsRef.current = null; };
      inputWs.onerror = () => { inputWsRef.current = null; };
    }
  }, [sessionId]);

  useEffect(() => {
    if (status === "streaming" && sessionId) connectStream();
    return () => {
      videoWsRef.current?.close();
      videoWsRef.current = null;
      inputWsRef.current?.close();
      inputWsRef.current = null;
    };
  }, [status, sessionId, connectStream]);

  return (
    <div className="flex flex-col h-full min-h-0 bg-surface-1">
      <div className="h-12 flex items-center justify-between px-4 flex-shrink-0 bg-surface-1 border-b border-white/5">
        <span className="text-xs font-mono uppercase tracking-wider text-text-secondary">Preview</span>
        <div className="flex items-center gap-2">
          <div
            className={`w-2 h-2 rounded-full transition-colors duration-300 ${
              status === "streaming"
                ? "bg-success animate-pulse-subtle"
                : status === "connecting"
                  ? "bg-warning animate-pulse-subtle"
                  : status === "error"
                    ? "bg-error"
                    : "bg-text-secondary/40"
            }`}
          />
          <span className="text-xs text-text-secondary capitalize">{status}</span>
        </div>
      </div>

      <div className="flex-1 min-h-0 overflow-y-auto flex items-center justify-center p-4">
        <div className="relative w-full max-w-[280px]">
          <div className="relative bg-surface-4 rounded-[36px] p-2">
            <div className="relative bg-surface-2 rounded-[28px] overflow-hidden aspect-[9/19.5]">
              {/* Status bar */}
              <div className="h-10 flex items-center justify-between px-5 pt-1 z-10 relative">
                <span className="text-[9px] font-mono text-text-secondary">9:41</span>
                <div className="flex gap-0.5">
                  <div className="w-2.5 h-1.5 bg-text-secondary rounded-sm" />
                  <div className="w-2.5 h-1.5 bg-text-secondary rounded-sm" />
                  <div className="w-3 h-1.5 bg-text-secondary rounded-sm" />
                </div>
              </div>

              {/* Screen content */}
              <div className="absolute inset-0 top-10 bottom-12 flex items-center justify-center">
                {status === "idle" && (
                  <div className="text-center px-6">
                    <div className="w-12 h-12 rounded-xl bg-surface-3 flex items-center justify-center mx-auto mb-3">
                      <Mobile size="24" className="text-text-secondary" variant="Linear" />
                    </div>
                    <p className="text-[10px] text-text-secondary mb-3">Start a session to preview your app</p>
                    {onStartSession && (
                      <button
                        onClick={onStartSession}
                        className="px-4 py-1.5 rounded-full bg-accent text-background text-[10px] font-medium hover:bg-accent-hover transition-all duration-150 active:scale-[0.98]"
                      >
                        Start Session
                      </button>
                    )}
                  </div>
                )}

                {status === "connecting" && (
                  <div className="text-center">
                    <div className="w-8 h-8 border-2 border-accent border-t-transparent rounded-full animate-spin mx-auto mb-2" />
                    <p className="text-[10px] text-text-secondary">Connecting to device...</p>
                  </div>
                )}

                {status === "streaming" && (
                  <canvas
                    ref={canvasRef}
                    onPointerDown={handlePointerDown}
                    onPointerUp={handlePointerUp}
                    className="w-full h-full object-cover cursor-pointer touch-none"
                  />
                )}

                {status === "error" && (
                  <div className="text-center px-6">
                    <div className="w-12 h-12 rounded-xl bg-error/20 flex items-center justify-center mx-auto mb-3">
                      <Warning2 size="24" className="text-error" variant="Linear" />
                    </div>
                    <p className="text-[10px] text-error">Connection failed</p>
                    {onStartSession && (
                      <button
                        onClick={onStartSession}
                        className="mt-2 px-4 py-1.5 rounded-full bg-accent text-background text-[10px] font-medium hover:bg-accent-hover transition-all duration-150 active:scale-[0.98]"
                      >
                        Retry
                      </button>
                    )}
                  </div>
                )}
              </div>

              {/* Navigation bar */}
              <div className="absolute bottom-0 left-0 right-0 h-12 flex items-center justify-around px-5 bg-surface-2 z-10">
                <div className="w-5 h-5 rounded-full bg-accent/20" />
                <div className="w-5 h-5 rounded-full bg-text-primary/10" />
                <div className="w-5 h-5 rounded-full bg-text-primary/10" />
              </div>
            </div>
          </div>
        </div>
      </div>
    </div>
  );
}
