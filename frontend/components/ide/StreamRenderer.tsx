"use client";

import { useEffect, useRef } from "react";
import type { StreamEvent } from "@/lib/api";
import Timeline, { type TimelineEvent } from "./Timeline";

export interface StreamMessage {
  id: string;
  role: "user" | "assistant";
  thinking?: string;
  text?: string;
  toolCalls?: ToolCall[];
  timelineEvents?: TimelineEvent[];
  isStreaming: boolean;
  error?: string;
}

export interface ToolCall {
  name: string;
  input?: { path?: string; name?: string; description?: string };
  status: "pending" | "running" | "completed" | "error";
  result?: string;
}

interface StreamRendererProps {
  message: StreamMessage;
}

export default function StreamRenderer({ message }: StreamRendererProps) {
  const textRef = useRef<HTMLDivElement>(null);

  // Auto-scroll text as it streams
  useEffect(() => {
    if (textRef.current && message.isStreaming) {
      textRef.current.scrollIntoView({ behavior: "smooth", block: "end" });
    }
  }, [message.text, message.isStreaming]);

  return (
    <div className="space-y-3">
      {/* Thinking section */}
      {message.thinking != null && (
        <ThinkingBlock content={message.thinking} isStreaming={message.isStreaming} />
      )}

      {/* Text response */}
      {message.text && (
        <div ref={textRef} className="max-w-[85%] rounded-2xl px-4 py-3 text-sm whitespace-pre-wrap bg-surface-3 text-text-primary">
          {message.text}
          {message.isStreaming && <span className="inline-block w-1.5 h-4 bg-accent ml-0.5 animate-pulse" />}
        </div>
      )}

      {/* Timeline events */}
      {message.timelineEvents && message.timelineEvents.length > 0 && (
        <Timeline events={message.timelineEvents} />
      )}

      {/* Tool calls (legacy) */}
      {message.toolCalls && message.toolCalls.length > 0 && (
        <div className="ml-4 space-y-1.5">
          {message.toolCalls.map((tool, i) => (
            <ToolCallItem key={i} tool={tool} />
          ))}
        </div>
      )}

      {/* Loading indicator (no text yet) */}
      {message.isStreaming && !message.text && !message.thinking && (!message.timelineEvents || message.timelineEvents.length === 0) && (
        <div className="flex justify-start">
          <div className="bg-surface-3 rounded-2xl px-4 py-3">
            <div className="flex gap-1">
              <div className="w-2 h-2 bg-text-secondary/40 rounded-full animate-bounce" style={{ animationDelay: "0ms" }} />
              <div className="w-2 h-2 bg-text-secondary/40 rounded-full animate-bounce" style={{ animationDelay: "150ms" }} />
              <div className="w-2 h-2 bg-text-secondary/40 rounded-full animate-bounce" style={{ animationDelay: "300ms" }} />
            </div>
          </div>
        </div>
      )}

      {/* Error */}
      {message.error && (
        <div className="max-w-[85%] rounded-2xl px-4 py-3 text-sm bg-red-500/10 text-red-400 border border-red-500/20">
          {message.error}
        </div>
      )}
    </div>
  );
}

function ThinkingBlock({ content, isStreaming }: { content: string; isStreaming: boolean }) {
  const lines = content.split("\n").filter(Boolean);
  const summary = lines[0] || "Thinking...";
  const isLong = lines.length > 3;

  return (
    <details className="ml-4 group" open={!isLong}>
      <summary className="cursor-pointer text-xs text-text-muted hover:text-text-secondary transition-colors select-none">
        {isStreaming ? (
          <span className="flex items-center gap-1.5">
            <span className="w-3 h-3 border-2 border-text-muted border-t-transparent rounded-full animate-spin" />
            Thinking...
          </span>
        ) : (
          <span>{summary}</span>
        )}
      </summary>
      <div className="mt-2 ml-2 pl-3 border-l-2 border-surface-3 text-xs text-text-muted whitespace-pre-wrap max-h-48 overflow-y-auto">
        {content}
      </div>
    </details>
  );
}

function ToolCallItem({ tool }: { tool: ToolCall }) {
  const label = getToolLabel(tool);

  return (
    <div className="flex items-center gap-2 text-xs py-0.5">
      {tool.status === "running" ? (
        <span className="w-3 h-3 border-2 border-accent border-t-transparent rounded-full animate-spin" />
      ) : tool.status === "completed" ? (
        <span className="text-green-400">&#10003;</span>
      ) : tool.status === "error" ? (
        <span className="text-red-400">&#10007;</span>
      ) : (
        <span className="w-3 h-3" />
      )}
      <span className={`font-mono ${tool.status === "completed" ? "text-text-secondary" : "text-text-muted"}`}>
        {label}
      </span>
    </div>
  );
}

function getToolLabel(tool: ToolCall): string {
  if (tool.name === "write_file") {
    return tool.input?.path || "file";
  }
  if (tool.name === "plan_screen") {
    return tool.input?.name || "screen";
  }
  return tool.name;
}
