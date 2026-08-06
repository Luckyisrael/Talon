"use client";

import { useState, useCallback } from "react";
import AIChat from "./AIChat";
import FileTree from "./FileTree";
import PhonePreview from "./PhonePreview";
import Terminal from "./Terminal";

interface IDELayoutProps {
  projectId: number;
  projectName: string;
}

interface FileNode {
  name: string;
  type: "file" | "folder";
  children?: FileNode[];
}

function buildFileTree(files: { path: string; content: string }[]): FileNode[] {
  const root: FileNode[] = [];

  for (const file of files) {
    const parts = file.path.split("/").filter(Boolean);
    let current = root;

    for (let i = 0; i < parts.length; i++) {
      const part = parts[i];
      const isFile = i === parts.length - 1;

      const existing = current.find((n) => n.name === part);
      if (existing) {
        if (!isFile && existing.type === "file") {
          existing.type = "folder";
          existing.children = [];
        }
        if (existing.children) {
          current = existing.children;
        }
      } else {
        const node: FileNode = {
          name: part,
          type: isFile ? "file" : "folder",
          children: isFile ? undefined : [],
        };
        current.push(node);
        if (node.children) {
          current = node.children;
        }
      }
    }
  }

  return root;
}

export default function IDELayout({ projectId, projectName }: IDELayoutProps) {
  const [selectedFile, setSelectedFile] = useState<string>();
  const [files, setFiles] = useState<FileNode[]>([]);
  const [fileContents] = useState<Record<string, string>>({});
  const [previewStatus] = useState<"idle" | "connecting" | "streaming" | "error">("idle");
  const [buildOutput, setBuildOutput] = useState("");

  const handleFilesGenerated = useCallback(
    (newFiles: { path: string; content: string }[]) => {
      setFiles((prev) => {
        const tree = buildFileTree(newFiles);
        return mergeFileTrees(prev, tree);
      });

      setBuildOutput((prev) => {
        const fileNames = newFiles.map((f) => f.path).join("\n");
        return prev
          ? `${prev}\n\nGenerated ${newFiles.length} files:\n${fileNames}`
          : `Generated ${newFiles.length} files:\n${fileNames}`;
      });
    },
    []
  );

  return (
    <div className="flex flex-col h-full overflow-hidden">
      <div className="flex-1 flex overflow-hidden min-h-0">
        {/* File Tree */}
        <div className="w-56 flex-shrink-0 bg-surface-2 hidden md:block overflow-hidden">
          <div className="h-10 flex items-center px-4 bg-surface-1">
            <span className="text-xs font-mono uppercase tracking-wider text-text-secondary">
              Files
            </span>
          </div>
          <div className="overflow-y-auto" style={{ height: "calc(100% - 40px)" }}>
            {files.length > 0 ? (
              <FileTree
                files={files}
                selectedFile={selectedFile}
                onSelectFile={setSelectedFile}
              />
            ) : (
              <div className="p-4">
                <p className="text-xs text-text-muted">
                  No files yet. Use the AI assistant to generate code.
                </p>
              </div>
            )}
          </div>
        </div>

        {/* AI Chat */}
        <div className="flex-1 flex flex-col min-w-0 overflow-hidden">
          <AIChat
            projectId={projectId}
            onFilesGenerated={handleFilesGenerated}
          />
        </div>

        {/* Phone Preview */}
        <div className="w-80 flex-shrink-0 hidden lg:block overflow-hidden">
          <PhonePreview status={previewStatus} />
        </div>
      </div>

      {/* Terminal */}
      <div className="h-48 flex-shrink-0 overflow-hidden">
        <Terminal output={buildOutput} />
      </div>
    </div>
  );
}

function mergeFileTrees(existing: FileNode[], newNodes: FileNode[]): FileNode[] {
  const result = [...existing];

  for (const newNode of newNodes) {
    const existingIdx = result.findIndex((n) => n.name === newNode.name);
    if (existingIdx >= 0) {
      const existingNode = result[existingIdx];
      if (newNode.type === "folder" && existingNode.type === "folder" && newNode.children && existingNode.children) {
        result[existingIdx] = {
          ...existingNode,
          children: mergeFileTrees(existingNode.children, newNode.children),
        };
      } else if (newNode.type === "file") {
        result[existingIdx] = newNode;
      }
    } else {
      result.push(newNode);
    }
  }

  return result;
}
