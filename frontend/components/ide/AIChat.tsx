"use client";

import { useState, useRef, useEffect, useImperativeHandle, forwardRef, useCallback } from "react";
import { Message, Send, Document, ArrowRight } from "iconsax-reactjs";
import { streamPrompt, type StreamEvent } from "@/lib/api";
import { useIDEStore } from "@/lib/store";
import StreamRenderer, { type StreamMessage, type ToolCall } from "./StreamRenderer";
import type { TimelineEvent } from "./Timeline";
import type { ChatMessage } from "@/lib/types";

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

  // Cleanup abort controller on unmount
  useEffect(() => () => abortRef.current?.abort(), []);

  const processStreamEvents = useCallback(
    (streamMsg: StreamMessage, setStreamMsg: (updater: (prev: StreamMessage) => StreamMessage) => void) => {
      return (event: StreamEvent) => {
        switch (event.type) {
          case "thinking.start":
            setStreamMsg((prev) => ({ ...prev, thinking: "" }));
            break;

          case "thinking.delta":
            setStreamMsg((prev) => ({
              ...prev,
              thinking: (prev.thinking || "") + (event.data?.text || ""),
            }));
            break;

          case "thinking.end":
            break;

          case "text.start":
            setStreamMsg((prev) => ({ ...prev, text: "" }));
            break;

          case "text.delta":
            setStreamMsg((prev) => ({
              ...prev,
              text: (prev.text || "") + (event.data?.text || ""),
            }));
            break;

          case "text.end":
            break;

          case "tool.start": {
            const tool: ToolCall = {
              name: event.data?.name || "unknown",
              input: event.data?.input,
              status: "running",
            };
            setStreamMsg((prev) => ({
              ...prev,
              toolCalls: [...(prev.toolCalls || []), tool],
            }));
            break;
          }

          case "tool.end": {
            setStreamMsg((prev) => ({
              ...prev,
              toolCalls: (prev.toolCalls || []).map((t) =>
                t.name === event.data?.name && t.status === "running"
                  ? { ...t, status: "completed" as const, result: event.data?.result }
                  : t,
              ),
            }));
            break;
          }

          case "phase": {
            const timelineEvt: TimelineEvent = {
              type: "phase",
              phase: event.data?.phase || "",
              status: event.data?.status || "started",
            };
            setStreamMsg((prev) => ({
              ...prev,
              timelineEvents: [...(prev.timelineEvents || []), timelineEvt],
            }));
            break;
          }

          case "tool_call": {
            const timelineEvt: TimelineEvent = {
              type: "tool_call",
              tool: event.data?.tool || "",
              path: event.data?.path || "",
              name: event.data?.name || "",
              status: event.data?.status || "running",
            };
            setStreamMsg((prev) => ({
              ...prev,
              timelineEvents: [...(prev.timelineEvents || []), timelineEvt],
            }));
            break;
          }

          case "verify": {
            const timelineEvt: TimelineEvent = {
              type: "verify",
              tool: event.data?.stage || "",
              status: event.data?.status || "running",
              error: event.data?.error || "",
            };
            setStreamMsg((prev) => ({
              ...prev,
              timelineEvents: [...(prev.timelineEvents || []), timelineEvt],
            }));
            break;
          }

          case "guardrail_reject": {
            const timelineEvt: TimelineEvent = {
              type: "guardrail_reject",
              tool: event.data?.tool || "",
              status: "rejected",
              error: event.data?.error || "",
            };
            setStreamMsg((prev) => ({
              ...prev,
              timelineEvents: [...(prev.timelineEvents || []), timelineEvt],
            }));
            break;
          }

          case "build.result":
            setStreamMsg((prev) => ({
              ...prev,
              text: (prev.text || "") + "\n\n" + (event.data?.output || ""),
            }));
            break;

          case "error":
            setStreamMsg((prev) => ({
              ...prev,
              error: event.data?.message || "Unknown error",
              isStreaming: false,
            }));
            break;

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

    // Add user message
    const userMsg: ChatMessage = {
      id: crypto.randomUUID(),
      role: "user",
      content: userMessage,
      timestamp: Date.now(),
    };
    setMessages((prev) => [...prev, userMsg]);

    // Create streaming assistant message
    const streamMsgId = crypto.randomUUID();
    const streamMsg: StreamMessage = {
      id: streamMsgId,
      role: "assistant",
      thinking: undefined,
      text: undefined,
      toolCalls: [],
      timelineEvents: [],
      isStreaming: true,
    };
    setMessages((prev) => [...prev, streamMsg]);

    // Start streaming
    const updater = (prev: StreamMessage): StreamMessage => prev;
    const setStreamMsg = (fn: (prev: StreamMessage) => StreamMessage) => {
      setMessages((prev) =>
        prev.map((m) => (m.id === streamMsgId ? fn(m as StreamMessage) : m)),
      );
    };

    abortRef.current = streamPrompt(
      projectId,
      userMessage,
      mode,
      processStreamEvents(streamMsg, setStreamMsg),
      (err) => {
        setError(err.message);
        setStreamMsg((prev) => ({ ...prev, isStreaming: false, error: err.message }));
      },
    );

    // Capture the stream message for file/plan extraction
    // We'll poll for completion and extract files
    const checkComplete = setInterval(() => {
      setMessages((prev) => {
        const sm = prev.find((m) => m.id === streamMsgId) as StreamMessage | undefined;
        if (sm && !sm.isStreaming) {
          clearInterval(checkComplete);

          // Extract files from tool calls if build mode
          if (mode === "build" && sm.toolCalls && sm.toolCalls.length > 0) {
            const fileEntries = sm.toolCalls
              .filter((t) => t.name === "write_file" && t.status === "completed")
              .map((t) => ({ path: t.input?.path || "", content: "" }));
            if (fileEntries.length > 0) {
              addFiles(fileEntries);
            }
          }
        }
        return prev;
      });
    }, 200);

    // Safety: stop checking after 120s
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

  return (
    <div className="flex flex-col h-full min-h-0 bg-surface-1">
      {/* Header */}
      <div className="h-12 flex items-center justify-between px-4 flex-shrink-0 bg-surface-1">
        <span className="text-xs font-mono uppercase tracking-wider text-text-secondary">AI Assistant</span>
        <div className="flex gap-1">
          <button
            onClick={() => setMode("plan")}
            className={`rounded-full h-7 px-3 text-xs font-medium transition-colors ${
              mode === "plan" ? "bg-accent text-background" : "bg-surface-3 text-text-secondary hover:bg-surface-4"
            }`}
          >
            Plan
          </button>
          <button
            onClick={() => setMode("build")}
            className={`rounded-full h-7 px-3 text-xs font-medium transition-colors ${
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
          // Streaming message
          if ("isStreaming" in msg) {
            return <StreamRenderer key={msg.id} message={msg as StreamMessage} />;
          }

          // Completed message
          const chatMsg = msg as ChatMessage;
          return (
            <div key={chatMsg.id} className="space-y-3">
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
                    className="h-8 px-4 rounded-lg bg-accent text-background text-xs font-medium flex items-center gap-1.5 hover:bg-accent-hover transition-colors"
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
      <form ref={formRef} onSubmit={handleSubmit} className="p-4 flex-shrink-0">
        <div className="flex gap-2">
          <input
            type="text"
            value={input}
            onChange={(e) => setInput(e.target.value)}
            placeholder={mode === "plan" ? "Describe your app idea..." : "What should I build?"}
            className="flex-1 h-10 px-4 rounded-xl bg-surface-2 text-text-primary placeholder:text-text-muted focus:outline-none focus:ring-2 focus:ring-accent/50 transition-all text-sm"
            disabled={messages.some((m) => "isStreaming" in m && (m as StreamMessage).isStreaming)}
          />
          {messages.some((m) => "isStreaming" in m && (m as StreamMessage).isStreaming) ? (
            <button
              type="button"
              onClick={handleCancel}
              className="rounded-full h-10 w-10 flex items-center justify-center bg-red-500 text-white hover:bg-red-600 transition-colors"
            >
              <span className="text-xs font-bold">Stop</span>
            </button>
          ) : (
            <button
              type="submit"
              disabled={!input.trim()}
              className="rounded-full h-10 w-10 flex items-center justify-center bg-accent text-background hover:bg-accent-hover transition-colors disabled:opacity-50"
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
