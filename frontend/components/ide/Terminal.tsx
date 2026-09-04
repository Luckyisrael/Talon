"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Terminal as XTerminal } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import "@xterm/xterm/css/xterm.css";
import { createAuthWebSocket } from "@/lib/api";
import { getToken } from "@/lib/auth";
import { useIDEStore } from "@/lib/store";

/**
 * The shell on the device, and the build output.
 *
 * There used to be three tabs; two of them ("Build Output" and "Logs") rendered
 * the same string, and nothing in the app ever wrote to it. The shell also lost
 * its contents whenever you looked at another tab, because the container it was
 * attached to was unmounted while the xterm instance held on to the detached
 * node — reopening the tab showed a blank pane with no way back.
 */

interface TerminalProps {
  sessionId?: number | null;
}

/**
 * xterm renders through a canvas, and `ctx.font` does not resolve `var()` — so
 * the CSS custom property has to be read off the document and passed as a plain
 * family list. next/font generates the real family names at build time.
 */
function monoStack(): string {
  const fallback = 'ui-monospace, "SF Mono", "Cascadia Code", monospace';
  if (typeof window === "undefined") return fallback;
  const v = getComputedStyle(document.documentElement)
    .getPropertyValue("--font-berkeley-mono")
    .trim();
  return v ? `${v}, ${fallback}` : fallback;
}

// Linear surfaces in concrete hex — xterm renders through canvas, and
// `ctx.font`/`ctx.fillStyle` do not resolve `var()`.
const THEME = {
  background: "#0f1011",
  foreground: "#d0d6e0",
  cursor: "#e4f222",
  cursorAccent: "#08090a",
  selectionBackground: "#23252a",
  black: "#0f1011",
  red: "#eb5757",
  green: "#27a644",
  yellow: "#ffb020",
  blue: "#4c9bef",
  magenta: "#df6faf",
  cyan: "#02b8cc",
  white: "#d0d6e0",
  brightBlack: "#62666d",
  brightRed: "#f08080",
  brightGreen: "#4cc069",
  brightYellow: "#ffc95c",
  brightBlue: "#8fbcff",
  brightMagenta: "#ef9fca",
  brightCyan: "#6fe4d8",
  brightWhite: "#ffffff",
} as const;

export default function Terminal({ sessionId }: TerminalProps) {
  const [tab, setTab] = useState<"shell" | "output">("shell");
  // Tagged with the session it describes. A bare status would keep reading
  // "attached" for one render after the session changed, because the previous
  // socket's "open" outlives the socket itself.
  const [socket, setSocket] = useState<{
    id: number;
    state: "open" | "closed" | "error" | "reconnecting";
  } | null>(null);
  // Whether we hold credentials at all. Read once on mount because a socket
  // cannot be opened without them, and that is knowable here rather than
  // something to discover inside the effect and write back.
  const [authed] = useState(() => Boolean(getToken()));
  const hostRef = useRef<HTMLDivElement>(null);
  const termRef = useRef<XTerminal | null>(null);
  const fitRef = useRef<FitAddon | null>(null);
  const wsRef = useRef<WebSocket | null>(null);
  const outputRef = useRef<HTMLDivElement>(null);
  // Reconnect bookkeeping lives in refs: retry count and timer are not render
  // state, and resetting them needs no extra render pass.
  const retryRef = useRef(0);
  const retryTimer = useRef(0);
  const [epoch, setEpoch] = useState(0);

  const buildOutput = useIDEStore((s) => s.buildOutput);
  const clearBuildOutput = useIDEStore((s) => s.clearBuildOutput);

  const fit = useCallback(() => {
    const host = hostRef.current;
    if (!host || host.clientWidth === 0 || host.clientHeight === 0) return;
    try {
      fitRef.current?.fit();
    } catch {
      // fit() throws while the pane is mid-transition. The next resize wins.
    }
  }, []);

  useEffect(() => {
    if (!hostRef.current || termRef.current) return;

    const term = new XTerminal({
      theme: { ...THEME },
      fontFamily: monoStack(),
      fontSize: 12,
      lineHeight: 1.45,
      cursorBlink: true,
      cursorStyle: "bar",
      scrollback: 5000,
      allowProposedApi: true,
    });

    const fitAddon = new FitAddon();
    term.loadAddon(fitAddon);
    term.open(hostRef.current);

    termRef.current = term;
    fitRef.current = fitAddon;
    fit();

    // Registered once, for the lifetime of the terminal. The old code attached a
    // fresh onData handler on every connect, so a reconnect sent each keystroke
    // as many times as the socket had been opened.
    const typed = term.onData((data) => {
      const ws = wsRef.current;
      if (ws?.readyState === WebSocket.OPEN) ws.send(new TextEncoder().encode(data));
    });

    const observer = new ResizeObserver(fit);
    observer.observe(hostRef.current);

    return () => {
      observer.disconnect();
      typed.dispose();
      term.dispose();
      termRef.current = null;
      fitRef.current = null;
    };
  }, [fit]);

  // One socket per session, torn down when the session changes or goes away.
  // An unexpected close schedules a reconnect with backoff (1s, 2s, 4s, 8s,
  // 15s cap, five attempts) and reports "reconnecting" meanwhile — the socket
  // never silently freezes while looking attached. Cleanup clears the timer so
  // an unmount never resurrects a dead socket.
  useEffect(() => {
    // No `setSocket` on this branch: with no session the status reads "no
    // device" from `sessionId` alone, so syncing a state here would only cost a
    // second render pass to reach the same screen.
    if (!sessionId) return;
    retryRef.current = 0;

    const ws = createAuthWebSocket(`/v1/sessions/${sessionId}/terminal`);
    // A null socket means there is no token to open one with. That is read off
    // `authed` during render instead of written back as state here, so the
    // failure costs no extra render pass.
    if (!ws) return;
    wsRef.current = ws;

    const scheduleRetry = () => {
      retryRef.current += 1;
      if (retryRef.current > 5) return;
      setSocket({ id: sessionId, state: "reconnecting" });
      const delay = Math.min(1000 * 2 ** (retryRef.current - 1), 15000);
      retryTimer.current = window.setTimeout(() => setEpoch((e) => e + 1), delay);
    };

    ws.onopen = () => {
      retryRef.current = 0;
      setSocket({ id: sessionId, state: "open" });
    };
    ws.onmessage = (event) => {
      const data: unknown = event.data;
      if (data instanceof Blob) {
        void data.arrayBuffer().then((buf) => {
          termRef.current?.write(new TextDecoder().decode(buf));
        });
      } else if (data instanceof ArrayBuffer) {
        termRef.current?.write(new TextDecoder().decode(data));
      } else if (typeof data === "string") {
        termRef.current?.write(data);
      }
    };
    ws.onclose = () => {
      if (wsRef.current === ws) wsRef.current = null;
      setSocket({ id: sessionId, state: "closed" });
      scheduleRetry();
    };
    ws.onerror = () => setSocket({ id: sessionId, state: "error" });

    return () => {
      window.clearTimeout(retryTimer.current);
      ws.onclose = null;
      ws.close();
      if (wsRef.current === ws) wsRef.current = null;
    };
  }, [sessionId, epoch]);

  useEffect(() => {
    if (tab === "shell") fit();
  }, [tab, fit]);

  useEffect(() => {
    if (outputRef.current) outputRef.current.scrollTop = outputRef.current.scrollHeight;
  }, [buildOutput]);

  const status = useMemo(() => {
    if (!sessionId) return "no device";
    if (!authed) return "could not attach";
    // A record for a different session is last session's socket, not this one's.
    if (socket?.id !== sessionId) return "attaching";
    if (socket.state === "open") return "attached";
    if (socket.state === "error") return "could not attach";
    if (socket.state === "reconnecting") return "reconnecting";
    return "detached";
  }, [socket, sessionId, authed]);

  return (
    <div className="flex h-full min-h-0 flex-col bg-carbon">
      <div className="flex h-7 flex-shrink-0 items-center gap-1 border-b border-graphite px-1.5">
        {(["shell", "output"] as const).map((id) => (
          <button
            key={id}
            type="button"
            onClick={() => setTab(id)}
            aria-pressed={tab === id}
            className={`h-full px-2 font-mono text-xs uppercase tracking-[0.12em] transition-colors ${
              tab === id
                ? "text-paper shadow-[inset_0_-2px_0_0_var(--color-acid-lime)]"
                : "text-ash hover:text-fog"
            }`}
          >
            {id}
          </button>
        ))}

        <span className="gauge ml-auto pr-1">
          {tab === "shell" ? status : buildOutput ? `${buildOutput.split("\n").length} lines` : "empty"}
        </span>
        {tab === "output" && buildOutput && (
          <button type="button" onClick={clearBuildOutput} className="btn btn-ghost font-mono text-xs">
            Clear
          </button>
        )}
      </div>

      <div className="relative min-h-0 flex-1 overflow-hidden">
        {/* The shell stays mounted whichever tab is showing, so its scrollback
            and its socket survive a look at the output. */}
        <div className={tab === "shell" ? "h-full w-full" : "hidden"}>
          <div ref={hostRef} className="h-full w-full px-2 py-1" />
          {!sessionId && (
            <div className="absolute inset-0 flex flex-col items-center justify-center gap-1 bg-carbon/90 px-6 text-center">
              <p className="text-sm text-mist">No device running.</p>
              <p className="font-mono text-xs text-fog">
                Start one from the top rail and this becomes a shell on it.
              </p>
            </div>
          )}
        </div>

        <div className={tab === "output" ? "h-full" : "hidden"}>
          <div
            ref={outputRef}
            className="h-full overflow-y-auto whitespace-pre-wrap px-3 py-2 font-mono text-xs leading-relaxed text-fog"
          >
            {buildOutput || (
              <span className="text-ash">
                Type checks and dev-client rebuilds print here as they run.
              </span>
            )}
          </div>
        </div>
      </div>
    </div>
  );
}
