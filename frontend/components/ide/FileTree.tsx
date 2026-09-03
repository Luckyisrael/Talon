"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useIDEStore } from "@/lib/store";
import type { FileNode } from "@/lib/types";
import FileContextMenu from "./FileContextMenu";

/**
 * The workspace tree.
 *
 * Everything here is a path, so everything here is set in mono — the same
 * typeface the traces and the editor use for the same reason. Names carry an
 * unsaved marker straight from the draft store, so a file with pending edits is
 * visible from the tree rather than only once you open it.
 *
 * The tree is keyboard-navigable (ARIA tree pattern): up/down walk the visible
 * rows, right expands a folder then steps into it, left collapses then steps out
 * to the parent, Enter opens a file, F2 renames. To make that work the folder
 * open/closed state lives here rather than in each row, so this component knows
 * exactly which rows are on screen and in what order.
 */

interface FileTreeProps {
  files: FileNode[];
  selectedFile?: string | null;
  onSelectFile?: (path: string) => void;
  onNewFile?: (parentPath: string, name: string) => void;
  onNewFolder?: (parentPath: string, name: string) => void;
  onRename?: (oldPath: string, newName: string) => void;
  onDelete?: (path: string) => void;
}

interface FlatNode {
  node: FileNode;
  fullPath: string;
  parent: string;
  depth: number;
}

// Walk the tree in render order, descending only into open folders. This is the
// exact list the arrow keys step through, so it has to match what is on screen.
function flatten(
  nodes: FileNode[],
  parent: string,
  depth: number,
  isOpen: (path: string, depth: number) => boolean,
  out: FlatNode[],
): FlatNode[] {
  for (const node of nodes) {
    const fullPath = parent ? `${parent}/${node.name}` : node.name;
    out.push({ node, fullPath, parent, depth });
    if (node.type === "folder" && node.children?.length && isOpen(fullPath, depth)) {
      flatten(node.children, fullPath, depth + 1, isOpen, out);
    }
  }
  return out;
}

interface RowProps {
  node: FileNode;
  parent: string;
  depth: number;
  selectedFile?: string | null;
  drafts: Record<string, string>;
  isOpen: (path: string, depth: number) => boolean;
  activePath: string | null;
  registerRef: (path: string, el: HTMLButtonElement | null) => void;
  onToggle: (path: string) => void;
  onSelectFile?: (path: string) => void;
  onFocusRow: (path: string) => void;
  onContextMenu: (e: React.MouseEvent, type: "file" | "folder", path: string) => void;
  renamingPath: string | null;
  onRenameSubmit: (newName: string) => void;
  onRenameCancel: () => void;
}

function Row({
  node,
  parent,
  depth,
  selectedFile,
  drafts,
  isOpen,
  activePath,
  registerRef,
  onToggle,
  onSelectFile,
  onFocusRow,
  onContextMenu,
  renamingPath,
  onRenameSubmit,
  onRenameCancel,
}: RowProps) {
  const fullPath = parent ? `${parent}/${node.name}` : node.name;
  const selected = selectedFile === fullPath;
  const dirty = node.type === "file" && fullPath in drafts;
  const open = node.type === "folder" && isOpen(fullPath, depth);
  // Exactly one row is tabbable at a time — the roving tabindex the tree pattern
  // wants, so Tab lands on the tree once and the arrows take over from there.
  const active = activePath === fullPath;

  if (renamingPath === fullPath) {
    return (
      <div className="px-1 py-0.5" style={{ paddingLeft: 8 + depth * 12 }}>
        <RenameInput defaultValue={node.name} onSubmit={onRenameSubmit} onCancel={onRenameCancel} />
      </div>
    );
  }

  return (
    <div>
      <button
        ref={(el) => registerRef(fullPath, el)}
        type="button"
        role="treeitem"
        aria-level={depth + 1}
        aria-selected={selected}
        aria-expanded={node.type === "folder" ? open : undefined}
        tabIndex={active ? 0 : -1}
        onFocus={() => onFocusRow(fullPath)}
        onClick={() => (node.type === "folder" ? onToggle(fullPath) : onSelectFile?.(fullPath))}
        onContextMenu={(e) => {
          e.preventDefault();
          e.stopPropagation();
          onContextMenu(e, node.type, fullPath);
        }}
        title={fullPath}
        className={`flex h-6 w-full items-center gap-1 pr-2 text-left font-mono text-xs outline-none transition-colors focus-visible:outline focus-visible:outline-1 focus-visible:-outline-offset-1 focus-visible:outline-acid-lime ${
          selected
            ? "bg-white/10 text-paper"
            : "text-fog hover:bg-white/5 hover:text-paper"
        }`}
        style={{ paddingLeft: 6 + depth * 12 }}
      >
        {node.type === "folder" ? (
          <svg
            aria-hidden
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            strokeWidth={2.5}
            className={`h-2.5 w-2.5 flex-shrink-0 text-ash transition-transform ${
              open ? "rotate-90" : ""
            }`}
          >
            <path strokeLinecap="round" strokeLinejoin="round" d="M9 5l7 7-7 7" />
          </svg>
        ) : (
          <span aria-hidden className="w-2.5 flex-shrink-0" />
        )}

        <span className="truncate">{node.name}</span>

        {node.type === "folder" && <span aria-hidden className="text-ash">/</span>}

        {dirty && (
          <span className="ml-auto flex-shrink-0 text-mist" title="Unsaved changes">
            ●
          </span>
        )}
      </button>

      {node.type === "folder" && open && node.children && node.children.length > 0 && (
        <div role="group" className="relative">
          {/* One hairline per level, so nesting is readable without indentation
              doing all the work. */}
          <span
            aria-hidden
            className="absolute top-0 bottom-0 w-px bg-graphite"
            style={{ left: 10 + depth * 12 }}
          />
          {node.children.map((child) => (
            <Row
              key={child.name}
              node={child}
              parent={fullPath}
              depth={depth + 1}
              selectedFile={selectedFile}
              drafts={drafts}
              isOpen={isOpen}
              activePath={activePath}
              registerRef={registerRef}
              onToggle={onToggle}
              onSelectFile={onSelectFile}
              onFocusRow={onFocusRow}
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
  const drafts = useIDEStore((s) => s.drafts);
  const [menu, setMenu] = useState<{
    x: number;
    y: number;
    type: "file" | "folder";
    path: string;
  } | null>(null);
  const [renaming, setRenaming] = useState<string | null>(null);
  const [pendingNew, setPendingNew] = useState<{
    parent: string;
    type: "file" | "folder";
  } | null>(null);

  // We track only the folders the user has flipped away from their default, so
  // openness stays correct as the workspace loads in without an effect resetting
  // state on every file batch. Default: top-level open, everything deeper closed.
  const [toggled, setToggled] = useState<Set<string>>(() => new Set());
  const [focusedPath, setFocusedPath] = useState<string | null>(null);
  const rowRefs = useRef<Map<string, HTMLButtonElement>>(new Map());

  const isOpen = useCallback(
    (path: string, depth: number) => (depth === 0 ? !toggled.has(path) : toggled.has(path)),
    [toggled],
  );

  const visible = useMemo(() => flatten(files, "", 0, isOpen, []), [files, isOpen]);

  // The focused row must be one that is actually on screen; if a collapse hid it,
  // fall back to the first row so Tab still has somewhere to land.
  const activePath =
    focusedPath && visible.some((v) => v.fullPath === focusedPath)
      ? focusedPath
      : visible[0]?.fullPath ?? null;

  const toggle = useCallback((path: string) => {
    setToggled((prev) => {
      const next = new Set(prev);
      if (next.has(path)) next.delete(path);
      else next.add(path);
      return next;
    });
  }, []);

  const registerRef = useCallback((path: string, el: HTMLButtonElement | null) => {
    if (el) rowRefs.current.set(path, el);
    else rowRefs.current.delete(path);
  }, []);

  const focusRow = useCallback((path: string) => {
    setFocusedPath(path);
    rowRefs.current.get(path)?.focus();
  }, []);

  const openMenu = useCallback(
    (e: React.MouseEvent, type: "file" | "folder", path: string) => {
      setMenu({ x: e.clientX, y: e.clientY, type, path });
    },
    [],
  );

  const onKeyDown = (e: React.KeyboardEvent) => {
    // The rename and new-file inputs live inside the tree; let them own their keys.
    if ((e.target as HTMLElement).tagName === "INPUT") return;
    if (visible.length === 0) return;

    const i = visible.findIndex((v) => v.fullPath === activePath);
    const cur = i >= 0 ? visible[i] : visible[0];

    switch (e.key) {
      case "ArrowDown": {
        e.preventDefault();
        focusRow(visible[Math.min(i + 1, visible.length - 1)].fullPath);
        break;
      }
      case "ArrowUp": {
        e.preventDefault();
        focusRow(visible[Math.max(i - 1, 0)].fullPath);
        break;
      }
      case "Home": {
        e.preventDefault();
        focusRow(visible[0].fullPath);
        break;
      }
      case "End": {
        e.preventDefault();
        focusRow(visible[visible.length - 1].fullPath);
        break;
      }
      case "ArrowRight": {
        e.preventDefault();
        if (cur.node.type !== "folder") break;
        if (!isOpen(cur.fullPath, cur.depth)) {
          toggle(cur.fullPath); // first press opens, focus stays put
        } else {
          const child = visible[i + 1];
          if (child && child.parent === cur.fullPath) focusRow(child.fullPath);
        }
        break;
      }
      case "ArrowLeft": {
        e.preventDefault();
        if (cur.node.type === "folder" && isOpen(cur.fullPath, cur.depth)) {
          toggle(cur.fullPath); // collapse, focus stays on the folder
        } else if (cur.parent) {
          focusRow(cur.parent);
        }
        break;
      }
      case "Enter":
      case " ": {
        e.preventDefault();
        if (cur.node.type === "folder") toggle(cur.fullPath);
        else onSelectFile?.(cur.fullPath);
        break;
      }
      case "F2": {
        if (cur.fullPath) {
          e.preventDefault();
          setRenaming(cur.fullPath);
        }
        break;
      }
    }
  };

  // Creating inside a file means creating beside it — the parent folder.
  const parentFor = (m: { type: string; path: string }) =>
    m.type === "folder" ? m.path : m.path.includes("/") ? m.path.slice(0, m.path.lastIndexOf("/")) : "";

  return (
    <div
      className="pb-2"
      role="tree"
      aria-label="Workspace files"
      onKeyDown={onKeyDown}
      onContextMenu={(e) => {
        if (e.target === e.currentTarget) {
          e.preventDefault();
          setMenu({ x: e.clientX, y: e.clientY, type: "folder", path: "" });
        }
      }}
    >
      {pendingNew && (
        <div className="px-2 pb-1 pt-2">
          <p className="gauge mb-1">
            New {pendingNew.type} in {pendingNew.parent || "the project root"}
          </p>
          <RenameInput
            defaultValue=""
            placeholder={pendingNew.type === "file" ? "screens/Balance.tsx" : "components"}
            onSubmit={(name) => {
              if (pendingNew.type === "file") onNewFile?.(pendingNew.parent, name);
              else onNewFolder?.(pendingNew.parent, name);
              setPendingNew(null);
            }}
            onCancel={() => setPendingNew(null)}
          />
        </div>
      )}

      {files.map((node) => (
        <Row
          key={node.name}
          node={node}
          parent=""
          depth={0}
          selectedFile={selectedFile}
          drafts={drafts}
          isOpen={isOpen}
          activePath={activePath}
          registerRef={registerRef}
          onToggle={toggle}
          onSelectFile={onSelectFile}
          onFocusRow={setFocusedPath}
          onContextMenu={openMenu}
          renamingPath={renaming}
          onRenameSubmit={(newName) => {
            if (renaming && newName.trim() && newName !== renaming.split("/").pop()) {
              onRename?.(renaming, newName.trim());
            }
            setRenaming(null);
          }}
          onRenameCancel={() => setRenaming(null)}
        />
      ))}

      {menu && (
        <FileContextMenu
          x={menu.x}
          y={menu.y}
          type={menu.type}
          path={menu.path}
          onNewFile={() => {
            setPendingNew({ parent: parentFor(menu), type: "file" });
            setMenu(null);
          }}
          onNewFolder={() => {
            setPendingNew({ parent: parentFor(menu), type: "folder" });
            setMenu(null);
          }}
          onRename={() => {
            setRenaming(menu.path);
            setMenu(null);
          }}
          onDelete={() => {
            onDelete?.(menu.path);
            setMenu(null);
          }}
          onClose={() => setMenu(null)}
        />
      )}
    </div>
  );
}

function RenameInput({
  defaultValue,
  placeholder,
  onSubmit,
  onCancel,
}: {
  defaultValue: string;
  placeholder?: string;
  onSubmit: (name: string) => void;
  onCancel: () => void;
}) {
  const [value, setValue] = useState(defaultValue);
  // Escape unmounts this input, which fires blur on the way out. Without this
  // flag the cancel was immediately followed by a submit of the same text.
  const cancelled = useRef(false);

  useEffect(() => {
    cancelled.current = false;
  }, []);

  return (
    <input
      autoFocus
      value={value}
      placeholder={placeholder}
      onChange={(e) => setValue(e.target.value)}
      onKeyDown={(e) => {
        if (e.key === "Enter") {
          const v = value.trim();
          if (v) onSubmit(v);
          else onCancel();
        } else if (e.key === "Escape") {
          cancelled.current = true;
          onCancel();
        }
      }}
      onBlur={() => {
        if (cancelled.current) return;
        const v = value.trim();
        if (v && v !== defaultValue) onSubmit(v);
        else onCancel();
      }}
      className="field h-6 py-0! font-mono text-xs"
    />
  );
}
