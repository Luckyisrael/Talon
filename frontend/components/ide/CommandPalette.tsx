"use client";

import { useEffect, useMemo, useRef, useState } from "react";
import type { FileNode } from "@/lib/types";

/**
 * Jump to anything.
 *
 * Two changes of substance over the old palette: matching is subsequence-based,
 * so "shbal" finds "screens/Balance.tsx" the way every other editor's palette
 * would; and results are capped at 40 rather than 10, because a ten-item cap on
 * a generated project silently hides most of it.
 */

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

interface Entry {
  id: string;
  label: string;
  hint?: string;
  kind: "action" | "file";
  run: () => void;
  haystack: string;
}

const LIMIT = 40;

/**
 * Subsequence match, scored so the closest thing lands first: a run of adjacent
 * characters beats a scattered one, and a match on the filename beats the same
 * match buried in a directory name.
 */
function score(haystack: string, needle: string): number {
  if (!needle) return 1;
  let hi = 0;
  let points = 0;
  let streak = 0;
  for (const ch of needle) {
    const found = haystack.indexOf(ch, hi);
    if (found === -1) return -1;
    streak = found === hi ? streak + 1 : 0;
    points += 1 + streak * 3;
    hi = found + 1;
  }
  // Prefer the shorter of two otherwise-equal matches.
  return points * 100 - haystack.length;
}

export default function CommandPalette({ open, ...rest }: CommandPaletteProps) {
  // Mounting only while open is what resets the query and the cursor. Keeping it
  // mounted and clearing the state in an effect on `open` meant a render pass
  // with last session's search still in the box.
  if (!open) return null;
  return <Palette {...rest} />;
}

function Palette({
  onClose,
  files,
  onOpenFile,
  onSwitchToChat,
  onSwitchToFiles,
  onToggleTerminal,
  onStartSession,
}: Omit<CommandPaletteProps, "open">) {
  const [query, setQuery] = useState("");
  const [rawCursor, setRawCursor] = useState(0);
  const listRef = useRef<HTMLDivElement>(null);

  const flatFiles = useMemo(() => {
    const out: { path: string; name: string }[] = [];
    function walk(nodes: FileNode[], prefix: string) {
      for (const node of nodes) {
        const path = prefix ? `${prefix}/${node.name}` : node.name;
        if (node.type === "file") out.push({ path, name: node.name });
        if (node.children) walk(node.children, path);
      }
    }
    walk(files, "");
    return out;
  }, [files]);

  const entries: Entry[] = useMemo(() => {
    const actions: Entry[] = [
      {
        id: "chat",
        label: "Talk to the agent",
        hint: "left panel",
        kind: "action",
        run: onSwitchToChat,
        haystack: "talk to the agent chat ai assistant build",
      },
      {
        id: "files",
        label: "Show the workspace",
        hint: "left panel",
        kind: "action",
        run: onSwitchToFiles,
        haystack: "show the workspace files tree browse",
      },
      {
        id: "terminal",
        label: "Toggle the shell",
        hint: "⌘J",
        kind: "action",
        run: onToggleTerminal,
        haystack: "toggle the shell terminal console logs output",
      },
      {
        id: "session",
        label: "Start a device",
        hint: "right panel",
        kind: "action",
        run: onStartSession,
        haystack: "start a device session preview emulator run",
      },
    ];

    const fileEntries: Entry[] = flatFiles.map((f) => ({
      id: `file:${f.path}`,
      label: f.name,
      hint: f.path.includes("/") ? f.path.slice(0, f.path.lastIndexOf("/")) : undefined,
      kind: "file",
      run: () => onOpenFile(f.path),
      // The filename is repeated so a match there outweighs the same match in a
      // directory name.
      haystack: `${f.name.toLowerCase()} ${f.path.toLowerCase()}`,
    }));

    return [...actions, ...fileEntries];
  }, [flatFiles, onOpenFile, onSwitchToChat, onSwitchToFiles, onToggleTerminal, onStartSession]);

  const results = useMemo(() => {
    const q = query.trim().toLowerCase();
    if (!q) return entries.slice(0, LIMIT);
    return entries
      .map((e) => ({ e, s: score(e.haystack, q) }))
      .filter((r) => r.s >= 0)
      .sort((a, b) => b.s - a.s)
      .slice(0, LIMIT)
      .map((r) => r.e);
  }, [entries, query]);

  // The list shrinks as you type, so the cursor is clamped where it is read
  // rather than corrected in an effect — otherwise Enter fires on a row that no
  // longer exists for one render.
  const cursor = Math.min(rawCursor, Math.max(0, results.length - 1));

  useEffect(() => {
    function onKeyDown(e: KeyboardEvent) {
      if (e.key === "ArrowDown" || (e.key === "n" && e.ctrlKey)) {
        e.preventDefault();
        setRawCursor(results.length === 0 ? 0 : (cursor + 1) % results.length);
      } else if (e.key === "ArrowUp" || (e.key === "p" && e.ctrlKey)) {
        e.preventDefault();
        setRawCursor(results.length === 0 ? 0 : (cursor - 1 + results.length) % results.length);
      } else if (e.key === "Enter") {
        e.preventDefault();
        const hit = results[cursor];
        if (hit) {
          hit.run();
          onClose();
        }
      } else if (e.key === "Escape") {
        e.preventDefault();
        onClose();
      }
    }
    document.addEventListener("keydown", onKeyDown);
    return () => document.removeEventListener("keydown", onKeyDown);
  }, [results, cursor, onClose]);

  useEffect(() => {
    const item = listRef.current?.children[cursor] as HTMLElement | undefined;
    item?.scrollIntoView({ block: "nearest" });
  }, [cursor]);

  return (
    <div className="fixed inset-0 z-50 flex items-start justify-center pt-[16vh]">
      <button
        type="button"
        aria-label="Close"
        onClick={onClose}
        className="absolute inset-0 cursor-default bg-ink/70 backdrop-blur-[2px]"
      />

      <div
        role="dialog"
        aria-modal="true"
        aria-label="Jump to a file or an action"
        className="rise relative mx-4 w-full max-w-lg overflow-hidden rounded-3 border border-edge bg-slate shadow-[0_28px_70px_-20px_rgba(0,0,0,0.9)]"
      >
        <div className="flex h-11 items-center gap-2.5 border-b border-rail px-3.5">
          <svg
            aria-hidden
            className="h-3.5 w-3.5 flex-shrink-0 text-dust"
            fill="none"
            viewBox="0 0 24 24"
            stroke="currentColor"
            strokeWidth={2}
          >
            <path strokeLinecap="round" strokeLinejoin="round" d="M21 21l-6-6m2-5a7 7 0 11-14 0 7 7 0 0114 0z" />
          </svg>
          <input
            autoFocus
            type="text"
            value={query}
            onChange={(e) => {
              setQuery(e.target.value);
              setRawCursor(0);
            }}
            placeholder="Jump to a file, or run something"
            className="min-w-0 flex-1 bg-transparent font-mono text-small text-paper placeholder:font-sans placeholder:text-dust focus:outline-none"
          />
          <span className="gauge flex-shrink-0">esc</span>
        </div>

        <div ref={listRef} className="max-h-[46vh] overflow-y-auto py-1">
          {results.length === 0 ? (
            <div className="px-4 py-7 text-center">
              <p className="text-small text-graphite">Nothing matches that.</p>
            </div>
          ) : (
            results.map((entry, i) => {
              const active = i === cursor;
              return (
                <button
                  key={entry.id}
                  type="button"
                  onClick={() => {
                    entry.run();
                    onClose();
                  }}
                  onMouseMove={() => setRawCursor(i)}
                  className={`flex w-full items-baseline gap-2.5 px-3.5 py-1.5 text-left transition-colors ${
                    active ? "bg-riser text-paper" : "text-graphite"
                  }`}
                >
                  <span
                    className={`min-w-0 truncate ${
                      entry.kind === "file" ? "font-mono text-tiny" : "text-small"
                    }`}
                  >
                    {entry.label}
                  </span>
                  {entry.hint && (
                    <span
                      className={`ml-auto flex-shrink-0 truncate pl-3 text-tiny ${
                        entry.kind === "file" ? "font-mono text-dust" : "text-dust"
                      }`}
                    >
                      {entry.hint}
                    </span>
                  )}
                </button>
              );
            })
          )}
        </div>

        <div className="flex items-center gap-3 border-t border-rail px-3.5 py-1.5">
          <span className="gauge">↑↓ move</span>
          <span className="gauge">↵ open</span>
          {results.length >= LIMIT && (
            <span className="gauge ml-auto">first {LIMIT} — keep typing</span>
          )}
        </div>
      </div>
    </div>
  );
}
