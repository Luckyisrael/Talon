"use client";

import Editor from "@monaco-editor/react";
import { Document } from "iconsax-reactjs";

interface CodeEditorProps {
  filePath: string;
  content: string;
}

function getLanguage(filePath: string): string {
  const ext = filePath.split(".").pop()?.toLowerCase();
  switch (ext) {
    case "ts":
    case "tsx":
      return "typescript";
    case "js":
    case "jsx":
      return "javascript";
    case "json":
      return "json";
    case "css":
      return "css";
    case "md":
      return "markdown";
    default:
      return "plaintext";
  }
}

export default function CodeEditor({ filePath, content }: CodeEditorProps) {
  const language = getLanguage(filePath);

  return (
    <div className="flex flex-col h-full min-h-0 bg-surface-1">
      {/* Tab bar */}
      <div className="h-10 flex items-center px-3 gap-2 bg-surface-1 flex-shrink-0">
        <Document size="14" className="text-accent" variant="Linear" />
        <span className="text-xs font-mono text-text-primary truncate">{filePath}</span>
      </div>

      {/* Editor */}
      <div className="flex-1 min-h-0">
        <Editor
          language={language}
          value={content}
          theme="vs-dark"
          options={{
            readOnly: true,
            minimap: { enabled: false },
            fontSize: 13,
            fontFamily: "var(--font-geist-mono), ui-monospace, monospace",
            lineNumbers: "on",
            scrollBeyondLastLine: false,
            padding: { top: 8, bottom: 8 },
            renderLineHighlight: "none",
            overviewRulerLanes: 0,
            hideCursorInOverviewRuler: true,
            overviewRulerBorder: false,
            scrollbar: {
              vertical: "auto",
              horizontal: "auto",
              verticalScrollbarSize: 6,
              horizontalScrollbarSize: 6,
            },
          }}
          loading={
            <div className="flex items-center justify-center h-full">
              <p className="text-xs text-text-muted">Loading editor...</p>
            </div>
          }
        />
      </div>
    </div>
  );
}
