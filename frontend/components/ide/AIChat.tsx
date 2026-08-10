"use client";

import { useState, useRef, useEffect, useImperativeHandle, forwardRef, useCallback } from "react";
import { Message, Send, Document, ArrowRight } from "iconsax-reactjs";
import { streamPrompt, type StreamEvent } from "@/lib/api";
import { useIDEStore } from "@/lib/store";
import AgentTrace from "./AgentTrace";
import { getToolLabel, isReadOnlyTool } from "./AgentTrace";
import type { ChatMessage, StreamMessage, TraceRow } from "@/lib/types";

interface AIChatProps {
  projectId: number;
}

export interface AIChatHandle {
  switchToBuild: () => void;
  sendMessage: (msg: string) => void;
}

const AIChat = forwardRef<AIChatHandle, AIChatProps>(({ projectId }, ref) => {
  const [messages, setMessages] = useState<(ChatMessage | StreamMessage)[]>([]);
  const [input, setInput] = useState("");
  const [mode, setMode] = useState<"plan" | "build">("plan");
  const [error, setError] = useState("");
  const messagesEndRef = useRef<HTMLDivElement>(null);
  const formRef = useRef<HTMLFormElement>(null);
  const abortRef = useRef<AbortController | null>(null);
  const filesReadRef = useRef<Set<string>>(new Set());

  const addFiles = useIDEStore((s) => s.addFiles);
  const setCurrentPlan = useIDEStore((s) => s.setCurrentPlan);

  useImperativeHandle(ref, () => ({
    switchToBuild: () => setMode("build"),
    sendMessage: (msg: string) => {
      setInput(msg);
      setTimeout(() => formRef.current?.requestSubmit(), 50);
    },
  }));

  useEffect(() => {
    messagesEndRef.current?.scrollIntoView({ behavior: "smooth" });
  }, [messages]);

  useEffect(() => () => abortRef.current?.abort(), []);

  const processStreamEvents = useCallback(
    (setStreamMsg: (updater: (prev: StreamMessage) => StreamMessage) => void) => {
      return (event: StreamEvent) => {
        switch (event.type) {
          // ── Narration (§11.1 thought events) ──
          case "thinking.start":
          case "text.start":
            setStreamMsg((prev) => {
              const lastRow = prev.traceRows[prev.traceRows.length - 1];
              if (lastRow?.kind === "narration" && lastRow.streaming) return prev;
              return {
                ...prev,
                traceRows: [
                  ...prev.traceRows,
                  { id: crypto.randomUUID(), kind: "narration", text: "", streaming: true },
                ],
              };
            });
            break;

          case "thinking.delta":
          case "text.delta":
            setStreamMsg((prev) => {
              const rows = [...prev.traceRows];
              const lastRow = rows[rows.length - 1];
              if (lastRow?.kind === "narration" && lastRow.streaming) {
                rows[rows.length - 1] = {
                  ...lastRow,
                  text: (lastRow.text || "") + (event.data?.text || ""),
                };
              }
              return { ...prev, traceRows: rows };
            });
            break;

          case "thinking.end":
          case "text.end":
            setStreamMsg((prev) => {
              const rows = [...prev.traceRows];
              const lastRow = rows[rows.length - 1];
              if (lastRow?.kind === "narration" && lastRow.streaming) {
                rows[rows.length - 1] = { ...lastRow, streaming: false };
              }
              return { ...prev, traceRows: rows };
            });
            break;

          // ── Tool start → running row ──
          case "tool.start": {
            const toolName = event.data?.name || "unknown";
            const input = event.data?.input;
            const { label, target } = getToolLabel(toolName, input, filesReadRef.current);

            // Track reads for read-before-write audit
            if (toolName === "read_file" && input?.path) {
              filesReadRef.current.add(input.path);
            }

            const row: TraceRow = {
              id: crypto.randomUUID(),
              kind: "tool",
              tool: toolName,
              label,
              target,
              status: "running",
            };

            setStreamMsg((prev) => ({
              ...prev,
              traceRows: [...prev.traceRows, row],
            }));
            break;
          }

          // ── Tool end → done/fail ──
          case "tool.end": {
            setStreamMsg((prev) => {
              const rows = [...prev.traceRows];
              // Find the last running row for this tool
              for (let i = rows.length - 1; i >= 0; i--) {
                if (rows[i].kind === "tool" && rows[i].tool === event.data?.name && rows[i].status === "running") {
                  rows[i] = {
                    ...rows[i],
                    status: event.data?.fixed === false ? "fail" : "done",
                    summary: event.data?.result,
                  };
                  break;
                }
              }
              return { ...prev, traceRows: rows };
            });
            break;
          }

          // ── Legacy timeline events (phase, tool_call, verify, guardrail_reject) ──
          // Map these to TraceRows for backward compatibility
          case "phase": {
            const row: TraceRow = {
              id: crypto.randomUUID(),
              kind: "tool",
              tool: "phase",
              label: event.data?.phase || "Phase",
              target: "",
              status: event.data?.status === "done" ? "done" : event.data?.status === "failed" ? "fail" : "running",
            };
            setStreamMsg((prev) => ({ ...prev, traceRows: [...prev.traceRows, row] }));
            break;
          }

          case "tool_call": {
            const toolName = event.data?.tool || "";
            const { label, target } = getToolLabel(toolName, { path: event.data?.path, name: event.data?.name }, filesReadRef.current);
            const row: TraceRow = {
              id: crypto.randomUUID(),
              kind: "tool",
              tool: toolName,
              label,
              target: target || event.data?.path || "",
              status: event.data?.status === "done" ? "done" : event.data?.status === "failed" ? "fail" : "running",
            };
            setStreamMsg((prev) => ({ ...prev, traceRows: [...prev.traceRows, row] }));
            break;
          }

          case "verify": {
            const row: TraceRow = {
              id: crypto.randomUUID(),
              kind: "tool",
              tool: "verify",
              label: event.data?.stage || "Verify",
              target: "",
              status: event.data?.status === "done" || event.data?.status === "pass" ? "done" : event.data?.status === "failed" || event.data?.status === "fail" ? "fail" : "running",
              summary: event.data?.error,
            };
            setStreamMsg((prev) => ({ ...prev, traceRows: [...prev.traceRows, row] }));
            break;
          }

          case "guardrail_reject": {
            const row: TraceRow = {
              id: crypto.randomUUID(),
              kind: "tool",
              tool: "guardrail",
              label: "Blocked",
              target: event.data?.error || "guardrail violation",
              status: "fail",
            };
            setStreamMsg((prev) => ({ ...prev, traceRows: [...prev.traceRows, row] }));
            break;
          }

          // ── Build result appended as narration ──
          case "build.result":
            setStreamMsg((prev) => ({
              ...prev,
              traceRows: [
                ...prev.traceRows,
                {
                  id: crypto.randomUUID(),
                  kind: "narration",
                  text: event.data?.output || "",
                  streaming: false,
                },
              ],
            }));
            break;

          // ── Error ──
          case "error":
            setStreamMsg((prev) => ({
              ...prev,
              error: event.data?.message || "Unknown error",
              isStreaming: false,
            }));
            break;

          // ── Done ──
          case "done":
            setStreamMsg((prev) => ({ ...prev, isStreaming: false }));
            break;
        }
      };
    },
    [],
  );

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    if (!input.trim()) return;

    const userMessage = input.trim();
    setInput("");
    setError("");
    filesReadRef.current = new Set();

    const userMsg: ChatMessage = {
      id: crypto.randomUUID(),
      role: "user",
      content: userMessage,
      timestamp: Date.now(),
    };
    setMessages((prev) => [...prev, userMsg]);

    const streamMsgId = crypto.randomUUID();
    const streamMsg: StreamMessage = {
      id: streamMsgId,
      role: "assistant",
      traceRows: [],
      isStreaming: true,
    };
    setMessages((prev) => [...prev, streamMsg]);

    const setStreamMsg = (fn: (prev: StreamMessage) => StreamMessage) => {
      setMessages((prev) =>
        prev.map((m) => (m.id === streamMsgId ? fn(m as StreamMessage) : m)),
      );
    };

    abortRef.current = streamPrompt(
      projectId,
      userMessage,
      mode,
      processStreamEvents(setStreamMsg),
      (err) => {
        setError(err.message);
        setStreamMsg((prev) => ({ ...prev, isStreaming: false, error: err.message }));
      },
    );

    // Poll for completion and extract files
    const checkComplete = setInterval(() => {
      setMessages((prev) => {
        const sm = prev.find((m) => m.id === streamMsgId) as StreamMessage | undefined;
        if (sm && !sm.isStreaming) {
          clearInterval(checkComplete);

          if (mode === "build" && sm.traceRows.length > 0) {
            const fileEntries = sm.traceRows
              .filter((r) => r.kind === "tool" && r.tool === "write_file" && r.status === "done" && r.target)
              .map((r) => ({ path: r.target!, content: "" }));
            if (fileEntries.length > 0) {
              addFiles(fileEntries);
            }
          }
        }
        return prev;
      });
    }, 200);

    setTimeout(() => clearInterval(checkComplete), 120000);
  }

  function handleCancel() {
    abortRef.current?.abort();
    setMessages((prev) =>
      prev.map((m) =>
        (m as StreamMessage).isStreaming
          ? { ...m, isStreaming: false, error: "Cancelled" }
          : m,
      ),
    );
  }

  const isStreaming = messages.some((m) => "isStreaming" in m && (m as StreamMessage).isStreaming);

  return (
    <div className="flex flex-col h-full min-h-0 bg-surface-1">
      {/* Header */}
      <div className="h-12 flex items-center justify-between px-4 flex-shrink-0 bg-surface-1 border-b border-white/5">
        <span className="text-xs font-mono uppercase tracking-wider text-text-secondary">AI Assistant</span>
        <div className="flex gap-1">
          <button
            onClick={() => setMode("plan")}
            className={`rounded-full h-7 px-3 text-xs font-medium transition-all duration-150 ${
              mode === "plan" ? "bg-accent text-background" : "bg-surface-3 text-text-secondary hover:bg-surface-4"
            }`}
          >
            Plan
          </button>
          <button
            onClick={() => setMode("build")}
            className={`rounded-full h-7 px-3 text-xs font-medium transition-all duration-150 ${
              mode === "build" ? "bg-accent text-background" : "bg-surface-3 text-text-secondary hover:bg-surface-4"
            }`}
          >
            Build
          </button>
        </div>
      </div>

      {/* Messages */}
      <div className="flex-1 min-h-0 overflow-y-auto p-4 space-y-4">
        {messages.length === 0 && (
          <div className="flex flex-col items-center justify-center h-full text-center">
            <div className="w-12 h-12 rounded-2xl bg-surface-3 flex items-center justify-center mb-4">
              <Message size="24" className="text-text-secondary" variant="Linear" />
            </div>
            <p className="text-sm text-text-secondary mb-1">
              {mode === "plan"
                ? "Describe your app idea and I'll plan the screens and features"
                : "Tell me what to build and I'll generate the code"}
            </p>
            <p className="text-xs text-text-muted">
              {mode === "plan"
                ? "Switch to Build mode when you're ready to generate code"
                : "Switch to Plan mode to design the app first"}
            </p>
          </div>
        )}

        {messages.map((msg) => {
          // Streaming message with trace
          if ("isStreaming" in msg) {
            const streamMsg = msg as StreamMessage;
            return (
              <div key={streamMsg.id} className="space-y-3 animate-fade-in">
                {/* Trace rows */}
                {streamMsg.traceRows.length > 0 && (
                  <AgentTrace rows={streamMsg.traceRows} />
                )}

                {/* Error */}
                {streamMsg.error && (
                  <div className="rounded-xl px-4 py-3 text-sm bg-error/10 text-error border border-error/20">
                    {streamMsg.error}
                  </div>
                )}

                {/* Loading dots (no content yet) */}
                {streamMsg.isStreaming && streamMsg.traceRows.length === 0 && !streamMsg.error && (
                  <div className="flex gap-1 py-2">
                    <div className="w-2 h-2 bg-text-secondary/40 rounded-full animate-bounce" style={{ animationDelay: "0ms" }} />
                    <div className="w-2 h-2 bg-text-secondary/40 rounded-full animate-bounce" style={{ animationDelay: "150ms" }} />
                    <div className="w-2 h-2 bg-text-secondary/40 rounded-full animate-bounce" style={{ animationDelay: "300ms" }} />
                  </div>
                )}
              </div>
            );
          }

          // Completed message
          const chatMsg = msg as ChatMessage;
          return (
            <div key={chatMsg.id} className="space-y-3 animate-fade-in">
              <div className={`flex ${chatMsg.role === "user" ? "justify-end" : "justify-start"}`}>
                <div
                  className={`max-w-[85%] rounded-2xl px-4 py-3 text-sm whitespace-pre-wrap ${
                    chatMsg.role === "user" ? "bg-accent text-background" : "bg-surface-3 text-text-primary"
                  }`}
                >
                  {chatMsg.content}
                </div>
              </div>

              {chatMsg.role === "assistant" && chatMsg.plan && (
                <div className="ml-4 space-y-3">
                  {chatMsg.plan.screens.length > 0 && (
                    <div className="bg-surface-2 rounded-xl p-3">
                      <p className="text-xs font-mono uppercase tracking-wider text-text-secondary mb-2">Screens</p>
                      <div className="space-y-2">
                        {chatMsg.plan.screens.map((screen, j) => (
                          <div key={j} className="flex items-start gap-2">
                            <ArrowRight size="14" className="text-accent mt-0.5 flex-shrink-0" variant="Linear" />
                            <div>
                              <span className="text-sm font-medium text-text-primary">{screen.name}</span>
                              <p className="text-xs text-text-secondary">{screen.description}</p>
                            </div>
                          </div>
                        ))}
                      </div>
                    </div>
                  )}

                  {chatMsg.plan.features.length > 0 && (
                    <div className="bg-surface-2 rounded-xl p-3">
                      <p className="text-xs font-mono uppercase tracking-wider text-text-secondary mb-2">Features</p>
                      <div className="flex flex-wrap gap-1.5">
                        {chatMsg.plan.features.map((feature, j) => (
                          <span key={j} className="h-6 px-2.5 rounded-full bg-surface-3 text-xs text-text-secondary">
                            {feature}
                          </span>
                        ))}
                      </div>
                    </div>
                  )}

                  {chatMsg.plan.navigation && (
                    <div className="bg-surface-2 rounded-xl p-3">
                      <p className="text-xs font-mono uppercase tracking-wider text-text-secondary mb-1">Navigation</p>
                      <p className="text-sm text-text-primary">{chatMsg.plan.navigation}</p>
                    </div>
                  )}

                  <button
                    onClick={() => {
                      setMode("build");
                      const screenNames = chatMsg.plan!.screens.map((s) => s.name).join(", ");
                      setInput(`Build the following screens: ${screenNames}. Generate all necessary components with full implementation.`);
                    }}
                    className="h-8 px-4 rounded-lg bg-accent text-background text-xs font-medium flex items-center gap-1.5 hover:bg-accent-hover transition-colors active:scale-[0.98]"
                  >
                    <Document size="12" variant="Linear" />
                    Approve &amp; Build
                  </button>
                </div>
              )}

              {chatMsg.role === "assistant" && chatMsg.files && chatMsg.files.length > 0 && (
                <div className="ml-4 bg-surface-2 rounded-xl p-3">
                  <p className="text-xs font-mono uppercase tracking-wider text-text-secondary mb-2">Generated Files</p>
                  <div className="space-y-1">
                    {chatMsg.files.map((file, j) => (
                      <div key={j} className="flex items-center gap-2 text-sm">
                        <Document size="14" className="text-accent flex-shrink-0" variant="Linear" />
                        <span className="text-text-primary font-mono text-xs">{file.path}</span>
                      </div>
                    ))}
                  </div>
                </div>
              )}
            </div>
          );
        })}

        <div ref={messagesEndRef} />
      </div>

      {/* Input */}
      <form ref={formRef} onSubmit={handleSubmit} className="p-4 flex-shrink-0 border-t border-white/5">
        <div className="flex gap-2">
          <input
            type="text"
            value={input}
            onChange={(e) => setInput(e.target.value)}
            placeholder={mode === "plan" ? "Describe your app idea..." : "What should I build?"}
            className="flex-1 h-10 px-4 rounded-xl bg-surface-2 text-text-primary placeholder:text-text-muted focus:outline-none focus:ring-2 focus:ring-accent/50 transition-all text-sm"
            disabled={isStreaming}
          />
          {isStreaming ? (
            <button
              type="button"
              onClick={handleCancel}
              className="rounded-full h-10 w-10 flex items-center justify-center bg-error text-white hover:bg-error/80 transition-colors active:scale-[0.98]"
            >
              <span className="text-xs font-bold">Stop</span>
            </button>
          ) : (
            <button
              type="submit"
              disabled={!input.trim()}
              className="rounded-full h-10 w-10 flex items-center justify-center bg-accent text-background hover:bg-accent-hover transition-colors disabled:opacity-50 active:scale-[0.98]"
            >
              <Send size="16" variant="Linear" />
            </button>
          )}
        </div>
      </form>
    </div>
  );
});

AIChat.displayName = "AIChat";
export default AIChat;
