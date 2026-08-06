"use client";

import { useState, useRef, useEffect } from "react";
import { Message, Send, Document, ArrowRight } from "iconsax-reactjs";

interface ChatMessage {
  role: "user" | "assistant";
  content: string;
  plan?: {
    screens: { name: string; description: string }[];
    features: string[];
    navigation: string;
    packages: string[];
  } | null;
  files?: { path: string; content: string }[];
  buildResult?: string;
}

interface AIChatProps {
  projectId: number;
  onFilesGenerated?: (files: { path: string; content: string }[]) => void;
}

export default function AIChat({ projectId, onFilesGenerated }: AIChatProps) {
  const [messages, setMessages] = useState<ChatMessage[]>([]);
  const [input, setInput] = useState("");
  const [loading, setLoading] = useState(false);
  const [mode, setMode] = useState<"plan" | "build">("plan");
  const [error, setError] = useState("");
  const messagesEndRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    messagesEndRef.current?.scrollIntoView({ behavior: "smooth" });
  }, [messages]);

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    if (!input.trim() || loading) return;

    const userMessage = input.trim();
    setInput("");
    setError("");
    setMessages((prev) => [...prev, { role: "user", content: userMessage }]);
    setLoading(true);

    try {
      const token = localStorage.getItem("token");
      if (!token) {
        setError("Please sign in to use the AI assistant.");
        setLoading(false);
        return;
      }

      const res = await fetch("http://localhost:8080/v1/prompts", {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          Authorization: `Bearer ${token}`,
        },
        body: JSON.stringify({
          project_id: projectId,
          prompt: userMessage,
          mode,
        }),
      });

      if (!res.ok) {
        const errData = await res.json().catch(() => ({}));
        throw new Error(errData.error || `Request failed (${res.status})`);
      }

      const data = await res.json();
      const response = data.response;

      const assistantMsg: ChatMessage = {
        role: "assistant",
        content: response.message || "Done.",
        plan: response.plan || null,
        files: response.files || [],
        buildResult: response.build_result || null,
      };

      setMessages((prev) => [...prev, assistantMsg]);

      if (response.files && response.files.length > 0 && onFilesGenerated) {
        onFilesGenerated(response.files);
      }
    } catch (err) {
      const msg = err instanceof Error ? err.message : "Something went wrong.";
      setError(msg);
      setMessages((prev) => [
        ...prev,
        { role: "assistant", content: `Error: ${msg}` },
      ]);
    } finally {
      setLoading(false);
    }
  }

  return (
    <div className="flex flex-col h-full min-h-0 bg-surface-1">
      {/* Header */}
      <div className="h-12 flex items-center justify-between px-4 flex-shrink-0 bg-surface-1">
        <span className="text-xs font-mono uppercase tracking-wider text-text-secondary">
          AI Assistant
        </span>
        <div className="flex gap-1">
          <button
            onClick={() => setMode("plan")}
            className={`rounded-full h-7 px-3 text-xs font-medium transition-colors ${
              mode === "plan"
                ? "bg-accent text-background"
                : "bg-surface-3 text-text-secondary hover:bg-surface-4"
            }`}
          >
            Plan
          </button>
          <button
            onClick={() => setMode("build")}
            className={`rounded-full h-7 px-3 text-xs font-medium transition-colors ${
              mode === "build"
                ? "bg-accent text-background"
                : "bg-surface-3 text-text-secondary hover:bg-surface-4"
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

        {messages.map((msg, i) => (
          <div key={i} className="space-y-3">
            {/* Main message bubble */}
            <div
              className={`flex ${msg.role === "user" ? "justify-end" : "justify-start"}`}
            >
              <div
                className={`max-w-[85%] rounded-2xl px-4 py-3 text-sm whitespace-pre-wrap ${
                  msg.role === "user"
                    ? "bg-accent text-background"
                    : "bg-surface-3 text-text-primary"
                }`}
              >
                {msg.content}
              </div>
            </div>

            {/* Plan display */}
            {msg.role === "assistant" && msg.plan && (
              <div className="ml-4 space-y-3">
                {/* Screens */}
                {msg.plan.screens.length > 0 && (
                  <div className="bg-surface-2 rounded-xl p-3">
                    <p className="text-xs font-mono uppercase tracking-wider text-text-secondary mb-2">
                      Screens
                    </p>
                    <div className="space-y-2">
                      {msg.plan.screens.map((screen, j) => (
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

                {/* Features */}
                {msg.plan.features.length > 0 && (
                  <div className="bg-surface-2 rounded-xl p-3">
                    <p className="text-xs font-mono uppercase tracking-wider text-text-secondary mb-2">
                      Features
                    </p>
                    <div className="flex flex-wrap gap-1.5">
                      {msg.plan.features.map((feature, j) => (
                        <span key={j} className="h-6 px-2.5 rounded-full bg-surface-3 text-xs text-text-secondary">
                          {feature}
                        </span>
                      ))}
                    </div>
                  </div>
                )}

                {/* Navigation */}
                {msg.plan.navigation && (
                  <div className="bg-surface-2 rounded-xl p-3">
                    <p className="text-xs font-mono uppercase tracking-wider text-text-secondary mb-1">
                      Navigation
                    </p>
                    <p className="text-sm text-text-primary">{msg.plan.navigation}</p>
                  </div>
                )}
              </div>
            )}

            {/* Files display */}
            {msg.role === "assistant" && msg.files && msg.files.length > 0 && (
              <div className="ml-4 bg-surface-2 rounded-xl p-3">
                <p className="text-xs font-mono uppercase tracking-wider text-text-secondary mb-2">
                  Generated Files
                </p>
                <div className="space-y-1">
                  {msg.files.map((file, j) => (
                    <div key={j} className="flex items-center gap-2 text-sm">
                      <Document size="14" className="text-accent flex-shrink-0" variant="Linear" />
                      <span className="text-text-primary font-mono text-xs">{file.path}</span>
                    </div>
                  ))}
                </div>
              </div>
            )}

            {/* Build result */}
            {msg.role === "assistant" && msg.buildResult && (
              <div className="ml-4 bg-surface-2 rounded-xl p-3">
                <p className="text-xs font-mono uppercase tracking-wider text-text-secondary mb-2">
                  Build Result
                </p>
                <pre className="text-xs text-text-secondary whitespace-pre-wrap font-mono">
                  {msg.buildResult}
                </pre>
              </div>
            )}
          </div>
        ))}

        {loading && (
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

        {error && !loading && (
          <div className="flex justify-center">
            <p className="text-xs text-error">{error}</p>
          </div>
        )}

        <div ref={messagesEndRef} />
      </div>

      {/* Input */}
      <form onSubmit={handleSubmit} className="p-4 flex-shrink-0">
        <div className="flex gap-2">
          <input
            type="text"
            value={input}
            onChange={(e) => setInput(e.target.value)}
            placeholder={
              mode === "plan"
                ? "Describe your app idea..."
                : "What should I build?"
            }
            className="flex-1 h-10 px-4 rounded-xl bg-surface-2 text-text-primary placeholder:text-text-muted focus:outline-none focus:ring-2 focus:ring-accent/50 transition-all text-sm"
            disabled={loading}
          />
          <button
            type="submit"
            disabled={loading || !input.trim()}
            className="rounded-full h-10 w-10 flex items-center justify-center bg-accent text-background hover:bg-accent-hover transition-colors disabled:opacity-50"
          >
            <Send size="16" variant="Linear" />
          </button>
        </div>
      </form>
    </div>
  );
}
