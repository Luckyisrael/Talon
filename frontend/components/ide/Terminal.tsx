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
    <div className="flex flex-col h-full min-h-0 bg-surface-1">
      <div className="flex items-center h-10 px-2 flex-shrink-0 bg-surface-1">
        {tabs.map((tab) => (
          <button
            key={tab.id}
            onClick={() => setActiveTab(tab.id)}
            className={`px-3 h-8 text-xs font-mono transition-colors ${
              activeTab === tab.id
                ? "text-accent border-b-2 border-accent"
                : "text-text-secondary hover:text-text-primary"
            }`}
          >
            {tab.label}
          </button>
        ))}
      </div>

      <div className="flex-1 min-h-0 overflow-y-auto p-4 font-mono text-xs">
        {activeTab === "terminal" && (
          <div className="text-text-secondary">
            <div className="flex items-center gap-2 mb-2">
              <span className="text-success">$</span>
              <span className="animate-pulse">_</span>
            </div>
          </div>
        )}

        {activeTab === "output" && (
          <div className="text-text-secondary whitespace-pre-wrap">
            {output || "No build output yet."}
          </div>
        )}

        {activeTab === "logs" && (
          <div className="text-text-secondary whitespace-pre-wrap">
            {output || "No logs yet."}
          </div>
        )}
      </div>
    </div>
  );
}
