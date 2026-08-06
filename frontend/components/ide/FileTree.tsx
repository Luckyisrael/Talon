"use client";

import { useState } from "react";

interface FileNode {
  name: string;
  type: "file" | "folder";
  children?: FileNode[];
}

interface FileTreeProps {
  files: FileNode[];
  selectedFile?: string;
  onSelectFile?: (path: string) => void;
}

function FileNodeItem({
  node,
  path = "",
  selectedFile,
  onSelectFile,
}: {
  node: FileNode;
  path?: string;
  selectedFile?: string;
  onSelectFile?: (path: string) => void;
}) {
  const [expanded, setExpanded] = useState(node.type === "folder");
  const fullPath = path ? `${path}/${node.name}` : node.name;

  const icon = node.type === "folder" ? (
    expanded ? (
      <svg className="w-4 h-4 text-text-secondary" fill="none" viewBox="0 0 24 24" stroke="currentColor">
        <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M5 19a2 2 0 01-2-2V7a2 2 0 012-2h4l2 2h4a2 2 0 012 2v1M5 19h14a2 2 0 002-2v-5a2 2 0 00-2-2H9a2 2 0 00-2 2v5a2 2 0 01-2 2z" />
      </svg>
    ) : (
      <svg className="w-4 h-4 text-text-secondary" fill="none" viewBox="0 0 24 24" stroke="currentColor">
        <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M3 7v10a2 2 0 002 2h14a2 2 0 002-2V9a2 2 0 00-2-2h-6l-2-2H5a2 2 0 00-2 2z" />
      </svg>
    )
  ) : (
    <svg className="w-4 h-4 text-text-secondary" fill="none" viewBox="0 0 24 24" stroke="currentColor">
      <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M9 12h6m-6 4h6m2 5H7a2 2 0 01-2-2V5a2 2 0 012-2h5.586a1 1 0 01.707.293l5.414 5.414a1 1 0 01.293.707V19a2 2 0 01-2 2z" />
    </svg>
  );

  return (
    <div>
      <button
        onClick={() => {
          if (node.type === "folder") {
            setExpanded(!expanded);
          } else {
            onSelectFile?.(fullPath);
          }
        }}
        className={`w-full flex items-center gap-2 px-2 py-1 text-sm rounded-lg transition-colors ${
          selectedFile === fullPath
            ? "bg-accent/10 text-accent"
            : "text-text-primary hover:bg-surface-3"
        }`}
      >
        {icon}
        <span className="truncate">{node.name}</span>
      </button>

      {node.type === "folder" && expanded && node.children && (
        <div className="ml-4">
          {node.children.map((child) => (
            <FileNodeItem
              key={child.name}
              node={child}
              path={fullPath}
              selectedFile={selectedFile}
              onSelectFile={onSelectFile}
            />
          ))}
        </div>
      )}
    </div>
  );
}

export default function FileTree({ files, selectedFile, onSelectFile }: FileTreeProps) {
  return (
    <div className="py-2">
      {files.map((node) => (
        <FileNodeItem
          key={node.name}
          node={node}
          selectedFile={selectedFile}
          onSelectFile={onSelectFile}
        />
      ))}
    </div>
  );
}
