"use client";

import { useState, useRef, useEffect, useCallback } from "react";
import { Terminal as XTerminal } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import "@xterm/xterm/css/xterm.css";
import { createAuthWebSocket } from "@/lib/api";
import { useIDEStore } from "@/lib/store";

interface TerminalProps {
  sessionId?: number | null;
}

export default function Terminal({ sessionId }: TerminalProps) {
  const [activeTab, setActiveTab] = useState<"terminal" | "output" | "logs">("terminal");
  const termRef = useRef<HTMLDivElement>(null);
  const xtermRef = useRef<XTerminal | null>(null);
  const fitAddonRef = useRef<FitAddon | null>(null);
  const wsRef = useRef<WebSocket | null>(null);
  const outputRef = useRef<HTMLDivElement>(null);

  const buildOutput = useIDEStore((s) => s.buildOutput);

  useEffect(() => {
    if (!termRef.current || xtermRef.current) return;

    const term = new XTerminal({
      theme: {
        background: "#111111",
        foreground: "#e0e0e0",
        cursor: "#F5A623",
        cursorAccent: "#111111",
        selectionBackground: "#F5A62333",
        black: "#111111",
        red: "#ff6b6b",
        green: "#51cf66",
        yellow: "#F5A623",
        blue: "#339af0",
        magenta: "#cc5de8",
        cyan: "#22b8cf",
        white: "#e0e0e0",
        brightBlack: "#666666",
        brightRed: "#ff8787",
        brightGreen: "#69db7c",
        brightYellow: "#ffd43b",
        brightBlue: "#4dabf7",
        brightMagenta: "#da77f2",
        brightCyan: "#3bc9db",
        brightWhite: "#f8f9fa",
      },
      fontFamily: '"Geist Mono", "Cascadia Code", "Fira Code", monospace',
      fontSize: 13,
      lineHeight: 1.4,
      cursorBlink: true,
      cursorStyle: "bar",
      allowProposedApi: true,
    });

    const fitAddon = new FitAddon();
    term.loadAddon(fitAddon);

    term.open(termRef.current);
    fitAddon.fit();

    xtermRef.current = term;
    fitAddonRef.current = fitAddon;

    const observer = new ResizeObserver(() => fitAddon.fit());
    observer.observe(termRef.current);

    return () => {
      observer.disconnect();
      term.dispose();
      xtermRef.current = null;
    };
  }, []);

  const connectWs = useCallback(() => {
    if (!sessionId || wsRef.current) return;

    const ws = createAuthWebSocket(`/v1/sessions/${sessionId}/terminal`);
    if (!ws) return;
    wsRef.current = ws;

    ws.onopen = () => {
      xtermRef.current?.writeln("\x1b[32mConnected to device shell\x1b[0m\r\n");
    };

    ws.onmessage = (event) => {
      if (event.data instanceof Blob) {
        event.data.arrayBuffer().then((buf) => {
          xtermRef.current?.write(new TextDecoder().decode(buf));
        });
      } else {
        xtermRef.current?.write(event.data);
      }
    };

    ws.onclose = () => {
      xtermRef.current?.writeln("\r\n\x1b[31mDisconnected\x1b[0m");
      wsRef.current = null;
    };

    ws.onerror = () => {
      xtermRef.current?.writeln("\r\n\x1b[31mConnection error\x1b[0m");
      wsRef.current = null;
    };

    xtermRef.current?.onData((data) => {
      if (ws.readyState === WebSocket.OPEN) {
        ws.send(new TextEncoder().encode(data));
      }
    });
  }, [sessionId]);

  useEffect(() => {
    if (activeTab === "terminal" && sessionId) connectWs();
  }, [activeTab, sessionId, connectWs]);

  useEffect(() => () => wsRef.current?.close(), []);

  useEffect(() => {
    if (outputRef.current) outputRef.current.scrollTop = outputRef.current.scrollHeight;
  }, [buildOutput]);

  const tabs = [
    { id: "terminal" as const, label: "Terminal" },
    { id: "output" as const, label: "Build Output" },
    { id: "logs" as const, label: "Logs" },
  ];

  return (
    <div className="flex flex-col h-full min-h-0 bg-surface-1">
      <div className="flex items-center h-8 px-2 flex-shrink-0 bg-surface-1">
        {tabs.map((tab) => (
          <button
            key={tab.id}
            onClick={() => setActiveTab(tab.id)}
            className={`px-3 h-7 text-xs font-mono transition-colors ${
              activeTab === tab.id
                ? "text-accent border-b-2 border-accent"
                : "text-text-secondary hover:text-text-primary"
            }`}
          >
            {tab.label}
          </button>
        ))}
      </div>

      <div className="flex-1 min-h-0 overflow-hidden">
        {activeTab === "terminal" && <div ref={termRef} className="h-full w-full" />}

        {activeTab === "output" && (
          <div ref={outputRef} className="h-full overflow-y-auto p-3 font-mono text-xs text-text-secondary whitespace-pre-wrap">
            {buildOutput || "No build output yet."}
          </div>
        )}

        {activeTab === "logs" && (
          <div className="h-full overflow-y-auto p-3 font-mono text-xs text-text-secondary whitespace-pre-wrap">
            {buildOutput || "No logs yet."}
          </div>
        )}
      </div>
    </div>
  );
}
