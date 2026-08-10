"use client";

import { useState, useRef, useEffect, useMemo } from "react";
import { Document, Folder, Message } from "iconsax-reactjs";
import type { FileNode } from "@/lib/types";

interface CommandPaletteProps {
  open: boolean;
  onClose: () => void;
  files: FileNode[];
  onOpenFile: (path: string) => void;
  onSwitchToChat: () => void;
  onSwitchToFiles: () => void;
  onToggleTerminal: () => void;
  onStartSession: () => void;
}

interface Command {
  id: string;
  label: string;
  description?: string;
  icon: React.ReactNode;
  action: () => void;
  keywords: string[];
}

export default function CommandPalette({
  open,
  onClose,
  files,
  onOpenFile,
  onSwitchToChat,
  onSwitchToFiles,
  onToggleTerminal,
  onStartSession,
}: CommandPaletteProps) {
  const [query, setQuery] = useState("");
  const [selectedIndex, setSelectedIndex] = useState(0);
  const inputRef = useRef<HTMLInputElement>(null);
  const listRef = useRef<HTMLDivElement>(null);

  // Flatten files for search
  const flatFiles = useMemo(() => {
    const result: { path: string; name: string }[] = [];
    function walk(nodes: FileNode[], prefix: string) {
      for (const node of nodes) {
        const path = prefix ? `${prefix}/${node.name}` : node.name;
        if (node.type === "file") {
          result.push({ path, name: node.name });
        }
        if (node.children) {
          walk(node.children, path);
        }
      }
    }
    walk(files, "");
    return result;
  }, [files]);

  // Build commands list
  const commands: Command[] = useMemo(() => {
    const cmds: Command[] = [
      {
        id: "chat",
        label: "Open Chat",
        description: "Switch to AI assistant",
        icon: <Message size="14" variant="Linear" />,
        action: () => { onSwitchToChat(); onClose(); },
        keywords: ["chat", "ai", "assistant"],
      },
      {
        id: "files",
        label: "Open Files",
        description: "Switch to file tree",
        icon: <Folder size="14" variant="Linear" />,
        action: () => { onSwitchToFiles(); onClose(); },
        keywords: ["files", "tree", "browse"],
      },
      {
        id: "terminal",
        label: "Toggle Terminal",
        description: "Show or hide the terminal",
        icon: (
          <svg className="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
            <path strokeLinecap="round" strokeLinejoin="round" d="M8 9l3 3-3 3m5 0h3M5 20h14a2 2 0 002-2V6a2 2 0 00-2-2H5a2 2 0 00-2 2v12a2 2 0 002 2z" />
          </svg>
        ),
        action: () => { onToggleTerminal(); onClose(); },
        keywords: ["terminal", "shell", "console"],
      },
      {
        id: "session",
        label: "Start Session",
        description: "Start a device preview session",
        icon: (
          <svg className="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
            <path strokeLinecap="round" strokeLinejoin="round" d="M12 18h.01M8 21h8a2 2 0 002-2V5a2 2 0 00-2-2H8a2 2 0 00-2 2v14a2 2 0 002 2z" />
          </svg>
        ),
        action: () => { onStartSession(); onClose(); },
        keywords: ["session", "preview", "device", "start"],
      },
    ];

    // Add file commands
    for (const file of flatFiles) {
      cmds.push({
        id: `file-${file.path}`,
        label: file.name,
        description: file.path,
        icon: <Document size="14" variant="Linear" />,
        action: () => { onOpenFile(file.path); onClose(); },
        keywords: [file.name.toLowerCase(), file.path.toLowerCase()],
      });
    }

    return cmds;
  }, [flatFiles, onOpenFile, onSwitchToChat, onSwitchToFiles, onToggleTerminal, onStartSession, onClose]);

  // Filter commands
  const filtered = useMemo(() => {
    if (!query.trim()) return commands.slice(0, 10);
    const q = query.toLowerCase();
    return commands
      .filter((cmd) => cmd.keywords.some((kw) => kw.includes(q)) || cmd.label.toLowerCase().includes(q))
      .slice(0, 10);
  }, [commands, query]);

  // Reset on open
  useEffect(() => {
    if (open) {
      setQuery("");
      setSelectedIndex(0);
      setTimeout(() => inputRef.current?.focus(), 50);
    }
  }, [open]);

  // Keyboard navigation
  useEffect(() => {
    if (!open) return;

    function handleKeyDown(e: KeyboardEvent) {
      if (e.key === "ArrowDown") {
        e.preventDefault();
        setSelectedIndex((i) => Math.min(i + 1, filtered.length - 1));
      } else if (e.key === "ArrowUp") {
        e.preventDefault();
        setSelectedIndex((i) => Math.max(i - 1, 0));
      } else if (e.key === "Enter") {
        e.preventDefault();
        filtered[selectedIndex]?.action();
      } else if (e.key === "Escape") {
        onClose();
      }
    }

    document.addEventListener("keydown", handleKeyDown);
    return () => document.removeEventListener("keydown", handleKeyDown);
  }, [open, filtered, selectedIndex, onClose]);

  // Scroll selected item into view
  useEffect(() => {
    if (listRef.current) {
      const item = listRef.current.children[selectedIndex] as HTMLElement;
      item?.scrollIntoView({ block: "nearest" });
    }
  }, [selectedIndex]);

  if (!open) return null;

  return (
    <div className="fixed inset-0 z-50 flex items-start justify-center pt-[20vh]">
      {/* Backdrop */}
      <div className="absolute inset-0 bg-background/80 backdrop-blur-sm animate-fade-in" onClick={onClose} />

      {/* Palette */}
      <div className="relative w-full max-w-[480px] mx-4 bg-surface-1 rounded-xl border border-white/10 shadow-lg overflow-hidden animate-slide-up">
        {/* Search input */}
        <div className="flex items-center gap-3 px-4 h-12 border-b border-white/5">
          <svg className="w-4 h-4 text-text-muted flex-shrink-0" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
            <path strokeLinecap="round" strokeLinejoin="round" d="M21 21l-6-6m2-5a7 7 0 11-14 0 7 7 0 0114 0z" />
          </svg>
          <input
            ref={inputRef}
            type="text"
            value={query}
            onChange={(e) => { setQuery(e.target.value); setSelectedIndex(0); }}
            placeholder="Search files and actions..."
            className="flex-1 bg-transparent text-sm text-text-primary placeholder:text-text-muted focus:outline-none"
          />
          <kbd className="text-[10px] text-text-muted bg-surface-3 px-1.5 py-0.5 rounded">ESC</kbd>
        </div>

        {/* Results */}
        <div ref={listRef} className="max-h-[300px] overflow-y-auto py-1">
          {filtered.length === 0 ? (
            <div className="px-4 py-8 text-center">
              <p className="text-sm text-text-muted">No results found</p>
            </div>
          ) : (
            filtered.map((cmd, i) => (
              <button
                key={cmd.id}
                onClick={cmd.action}
                className={`w-full flex items-center gap-3 px-4 py-2.5 text-left transition-colors duration-75 ${
                  i === selectedIndex ? "bg-accent/10 text-accent" : "text-text-secondary hover:bg-surface-2"
                }`}
              >
                <span className={`flex-shrink-0 ${i === selectedIndex ? "text-accent" : "text-text-muted"}`}>
                  {cmd.icon}
                </span>
                <div className="flex-1 min-w-0">
                  <span className="text-sm font-medium block truncate">{cmd.label}</span>
                  {cmd.description && (
                    <span className="text-[11px] text-text-muted block truncate">{cmd.description}</span>
                  )}
                </div>
              </button>
            ))
          )}
        </div>
      </div>
    </div>
  );
}
