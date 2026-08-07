"use client";

import { useState, useCallback } from "react";
import { Document, Folder } from "iconsax-reactjs";
import type { FileNode } from "@/lib/types";
import FileContextMenu from "./FileContextMenu";

interface FileTreeProps {
  files: FileNode[];
  selectedFile?: string | null;
  onSelectFile?: (path: string) => void;
  onNewFile?: (parentPath: string, name: string) => void;
  onNewFolder?: (parentPath: string, name: string) => void;
  onRename?: (oldPath: string, newName: string) => void;
  onDelete?: (path: string) => void;
}

function FileNodeItem({
  node,
  path = "",
  selectedFile,
  onSelectFile,
  onContextMenu,
  renamingPath,
  onRenameSubmit,
  onRenameCancel,
}: {
  node: FileNode;
  path?: string;
  selectedFile?: string | null;
  onSelectFile?: (path: string) => void;
  onContextMenu?: (e: React.MouseEvent, type: "file" | "folder", fullPath: string) => void;
  renamingPath?: string | null;
  onRenameSubmit?: (newName: string) => void;
  onRenameCancel?: () => void;
}) {
  const [expanded, setExpanded] = useState(node.type === "folder");
  const fullPath = path ? `${path}/${node.name}` : node.name;
  const isRenaming = renamingPath === fullPath;

  const icon = node.type === "folder" ? (
    <Folder size="16" className="text-text-secondary" variant={expanded ? "Bulk" : "Linear"} />
  ) : (
    <Document size="16" className="text-text-secondary" variant="Linear" />
  );

  const handleContextMenu = (e: React.MouseEvent) => {
    e.preventDefault();
    onContextMenu?.(e, node.type, fullPath);
  };

  if (isRenaming) {
    return (
      <div className="flex items-center gap-2 px-2 py-1">
        {icon}
        <input
          autoFocus
          defaultValue={node.name}
          className="flex-1 bg-surface-3 text-text-primary text-sm px-2 py-0.5 rounded outline-none border border-accent/50"
          onKeyDown={(e) => {
            if (e.key === "Enter") {
              onRenameSubmit?.((e.target as HTMLInputElement).value);
            } else if (e.key === "Escape") {
              onRenameCancel?.();
            }
          }}
          onBlur={(e) => onRenameSubmit?.(e.target.value)}
        />
      </div>
    );
  }

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
        onContextMenu={handleContextMenu}
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
              onContextMenu={onContextMenu}
              renamingPath={renamingPath}
              onRenameSubmit={onRenameSubmit}
              onRenameCancel={onRenameCancel}
            />
          ))}
        </div>
      )}
    </div>
  );
}

export default function FileTree({
  files,
  selectedFile,
  onSelectFile,
  onNewFile,
  onNewFolder,
  onRename,
  onDelete,
}: FileTreeProps) {
  const [contextMenu, setContextMenu] = useState<{
    x: number;
    y: number;
    type: "file" | "folder";
    path: string;
  } | null>(null);
  const [renamingPath, setRenamingPath] = useState<string | null>(null);
  const [pendingNew, setPendingNew] = useState<{
    parentPath: string;
    type: "file" | "folder";
  } | null>(null);

  const handleContextMenu = useCallback(
    (e: React.MouseEvent, type: "file" | "folder", fullPath: string) => {
      setContextMenu({ x: e.clientX, y: e.clientY, type, path: fullPath });
    },
    [],
  );

  const handleNewFile = useCallback(() => {
    if (!contextMenu) return;
    setPendingNew({ parentPath: contextMenu.type === "folder" ? contextMenu.path : "", type: "file" });
    setContextMenu(null);
  }, [contextMenu]);

  const handleNewFolder = useCallback(() => {
    if (!contextMenu) return;
    setPendingNew({ parentPath: contextMenu.type === "folder" ? contextMenu.path : "", type: "folder" });
    setContextMenu(null);
  }, [contextMenu]);

  const handleRename = useCallback(() => {
    if (!contextMenu) return;
    setRenamingPath(contextMenu.path);
    setContextMenu(null);
  }, [contextMenu]);

  const handleDelete = useCallback(() => {
    if (!contextMenu) return;
    onDelete?.(contextMenu.path);
    setContextMenu(null);
  }, [contextMenu, onDelete]);

  // Handle new file/folder name input
  if (pendingNew) {
    return (
      <div className="p-2">
        <NewNameInput
          prompt={pendingNew.type === "file" ? "New file name:" : "New folder name:"}
          onSubmit={(name) => {
            if (pendingNew.type === "file") {
              onNewFile?.(pendingNew.parentPath, name);
            } else {
              onNewFolder?.(pendingNew.parentPath, name);
            }
            setPendingNew(null);
          }}
          onCancel={() => setPendingNew(null)}
        />
        {files.map((node) => (
          <FileNodeItem
            key={node.name}
            node={node}
            selectedFile={selectedFile}
            onSelectFile={onSelectFile}
            onContextMenu={handleContextMenu}
          />
        ))}
      </div>
    );
  }

  return (
    <div
      className="py-2"
      onContextMenu={(e) => {
        // Right-click on empty space → create at root
        if (e.target === e.currentTarget) {
          e.preventDefault();
          setContextMenu({ x: e.clientX, y: e.clientY, type: "folder", path: "" });
        }
      }}
    >
      {files.length === 0 ? (
        <p className="px-4 py-2 text-[11px] text-text-muted">
          No files yet. Use the AI assistant to generate code.
        </p>
      ) : (
        files.map((node) => (
          <FileNodeItem
            key={node.name}
            node={node}
            selectedFile={selectedFile}
            onSelectFile={onSelectFile}
            onContextMenu={handleContextMenu}
            renamingPath={renamingPath}
            onRenameSubmit={(newName) => {
              if (renamingPath) {
                onRename?.(renamingPath, newName);
                setRenamingPath(null);
              }
            }}
            onRenameCancel={() => setRenamingPath(null)}
          />
        ))
      )}

      {contextMenu && (
        <FileContextMenu
          x={contextMenu.x}
          y={contextMenu.y}
          type={contextMenu.type}
          onNewFile={handleNewFile}
          onNewFolder={handleNewFolder}
          onRename={handleRename}
          onDelete={handleDelete}
          onClose={() => setContextMenu(null)}
        />
      )}
    </div>
  );
}

function NewNameInput({
  prompt,
  onSubmit,
  onCancel,
}: {
  prompt: string;
  onSubmit: (name: string) => void;
  onCancel: () => void;
}) {
  return (
    <div className="mb-2 rounded-lg border border-surface-3 bg-surface-2 p-2">
      <p className="text-[11px] text-text-muted mb-1">{prompt}</p>
      <input
        autoFocus
        className="w-full bg-surface-3 text-text-primary text-sm px-2 py-1 rounded outline-none border border-accent/50"
        onKeyDown={(e) => {
          if (e.key === "Enter") {
            const val = (e.target as HTMLInputElement).value.trim();
            if (val) onSubmit(val);
          } else if (e.key === "Escape") {
            onCancel();
          }
        }}
        onBlur={(e) => {
          const val = e.target.value.trim();
          if (val) onSubmit(val);
          else onCancel();
        }}
      />
    </div>
  );
}
