"use client";

import { useState } from "react";

interface TerminalProps {
  output?: string;
}

export default function Terminal({ output = "" }: TerminalProps) {
  const [activeTab, setActiveTab] = useState<"terminal" | "output" | "logs">("terminal");

  const tabs = [
    { id: "terminal" as const, label: "Terminal" },
    { id: "output" as const, label: "Build Output" },
    { id: "logs" as const, label: "Logs" },
  ];

  return (
    <div className="flex flex-col h-full bg-surface-container-lowest">
      {/* Tabs */}
      <div className="flex items-center h-10 px-2 hairline-bottom">
        {tabs.map((tab) => (
          <button
            key={tab.id}
            onClick={() => setActiveTab(tab.id)}
            className={`px-3 h-8 text-xs font-mono transition-colors ${
              activeTab === tab.id
                ? "text-primary border-b-2 border-primary"
                : "text-on-surface-variant hover:text-on-surface"
            }`}
          >
            {tab.label}
          </button>
        ))}
      </div>

      {/* Content */}
      <div className="flex-1 overflow-auto p-4 font-mono text-xs">
        {activeTab === "terminal" && (
          <div className="text-on-surface-variant">
            <div className="flex items-center gap-2 mb-2">
              <span className="text-success">$</span>
              <span className="animate-pulse">_</span>
            </div>
          </div>
        )}

        {activeTab === "output" && (
          <div className="text-on-surface-variant whitespace-pre-wrap">
            {output || "No build output yet."}
          </div>
        )}

        {activeTab === "logs" && (
          <div className="text-on-surface-variant whitespace-pre-wrap">
            {output || "No logs yet."}
          </div>
        )}
      </div>
    </div>
  );
}
