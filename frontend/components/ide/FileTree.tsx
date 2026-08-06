"use client";

import { useState } from "react";
import { Document, Folder, ArrowRight } from "iconsax-reactjs";

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
    <Folder size="16" className="text-text-secondary" variant={expanded ? "Bulk" : "Linear"} />
  ) : (
    <Document size="16" className="text-text-secondary" variant="Linear" />
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
