"use client";

import { useEffect, useLayoutEffect, useRef, useState } from "react";

/**
 * The right-click menu on the tree.
 *
 * Delete is two-step here. It used to remove the file from disk on a single
 * click, in a menu you can open by accident, with no undo anywhere in the
 * interface.
 */

interface FileContextMenuProps {
  x: number;
  y: number;
  type: "file" | "folder";
  /** Empty string means the menu was opened on the background — the root. */
  path: string;
  onNewFile: () => void;
  onNewFolder: () => void;
  onRename: () => void;
  onDelete: () => void;
  onClose: () => void;
}

export default function FileContextMenu({
  x,
  y,
  type,
  path,
  onNewFile,
  onNewFolder,
  onRename,
  onDelete,
  onClose,
}: FileContextMenuProps) {
  const ref = useRef<HTMLDivElement>(null);
  const [pos, setPos] = useState({ left: x, top: y });
  const [confirmingDelete, setConfirmingDelete] = useState(false);

  // Measure, then place. The old code subtracted a guessed 200px in both
  // directions, which pushed the menu away from the cursor near the edges and
  // still overflowed once the text got longer than the guess.
  useLayoutEffect(() => {
    const el = ref.current;
    if (!el) return;
    const { width, height } = el.getBoundingClientRect();
    setPos({
      left: Math.max(8, Math.min(x, window.innerWidth - width - 8)),
      top: Math.max(8, Math.min(y, window.innerHeight - height - 8)),
    });
  }, [x, y, confirmingDelete]);

  useEffect(() => {
    function onDown(e: MouseEvent) {
      if (!ref.current?.contains(e.target as Node)) onClose();
    }
    function onKey(e: KeyboardEvent) {
      if (e.key === "Escape") onClose();
    }
    document.addEventListener("mousedown", onDown);
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("mousedown", onDown);
      document.removeEventListener("keydown", onKey);
    };
  }, [onClose]);

  const name = path ? path.split("/").pop() : "project root";

  return (
    <div
      ref={ref}
      role="menu"
      style={{ left: pos.left, top: pos.top }}
      className="rise fixed z-50 w-60 overflow-hidden rounded-2 border border-edge bg-slate shadow-[0_18px_44px_-14px_rgba(0,0,0,0.85)]"
    >
      <div className="bay-head">
        <span className="gauge truncate" title={path || "project root"}>
          {name}
        </span>
      </div>

      <div className="py-1">
        <Item onClick={onNewFile}>New file</Item>
        <Item onClick={onNewFolder}>New folder</Item>

        {path && (
          <>
            <span aria-hidden className="my-1 block h-px bg-rail" />
            <Item onClick={onRename}>Rename</Item>
            <Item
              danger
              // Blur resets the confirmation, so a menu left open does not sit
              // there one stray click away from deleting something.
              onBlur={() => setConfirmingDelete(false)}
              onClick={() => {
                if (!confirmingDelete) {
                  setConfirmingDelete(true);
                  return;
                }
                onDelete();
              }}
            >
              {confirmingDelete
                ? type === "folder"
                  ? "Delete the folder and its contents?"
                  : "Delete for good?"
                : "Delete"}
            </Item>
          </>
        )}
      </div>
    </div>
  );
}

function Item({
  children,
  onClick,
  onBlur,
  danger,
}: {
  children: React.ReactNode;
  onClick: () => void;
  onBlur?: () => void;
  danger?: boolean;
}) {
  return (
    <button
      type="button"
      role="menuitem"
      onBlur={onBlur}
      onClick={(e) => {
        e.stopPropagation();
        onClick();
      }}
      className={`block w-full px-3 py-1.5 text-left text-small transition-colors ${
        danger
          ? "text-fault hover:bg-fault/12"
          : "text-graphite hover:bg-riser hover:text-paper"
      }`}
    >
      {children}
    </button>
  );
}
