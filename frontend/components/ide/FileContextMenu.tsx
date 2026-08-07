"use client";

import { useEffect, useRef } from "react";
import { Add, Edit, Trash } from "iconsax-reactjs";

interface FileContextMenuProps {
  x: number;
  y: number;
  type: "file" | "folder";
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
  onNewFile,
  onNewFolder,
  onRename,
  onDelete,
  onClose,
}: FileContextMenuProps) {
  const menuRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    function handleClick(e: MouseEvent) {
      if (menuRef.current && !menuRef.current.contains(e.target as Node)) {
        onClose();
      }
    }
    function handleKey(e: KeyboardEvent) {
      if (e.key === "Escape") onClose();
    }
    document.addEventListener("mousedown", handleClick);
    document.addEventListener("keydown", handleKey);
    return () => {
      document.removeEventListener("mousedown", handleClick);
      document.removeEventListener("keydown", handleKey);
    };
  }, [onClose]);

  // Adjust position so menu doesn't overflow viewport
  const adjustedX = Math.min(x, window.innerWidth - 200);
  const adjustedY = Math.min(y, window.innerHeight - 200);

  return (
    <div
      ref={menuRef}
      className="fixed z-50 min-w-[180px] rounded-xl border border-surface-3 bg-surface-1 py-1 shadow-lg"
      style={{ left: adjustedX, top: adjustedY }}
    >
      <MenuItem icon={<Add size="14" variant="Linear" />} label="New File" onClick={onNewFile} />
      <MenuItem
        icon={<Add size="14" variant="Linear" />}
        label="New Folder"
        onClick={onNewFolder}
      />
      <div className="my-1 h-px bg-surface-3" />
      <MenuItem
        icon={<Edit size="14" variant="Linear" />}
        label="Rename"
        onClick={onRename}
      />
      <MenuItem
        icon={<Trash size="14" variant="Linear" />}
        label="Delete"
        onClick={onDelete}
        danger
      />
    </div>
  );
}

function MenuItem({
  icon,
  label,
  onClick,
  danger = false,
}: {
  icon: React.ReactNode;
  label: string;
  onClick: () => void;
  danger?: boolean;
}) {
  return (
    <button
      className={`w-full flex items-center gap-2 px-3 py-2 text-[13px] text-left transition-colors
        ${danger ? "text-red-400 hover:bg-red-500/10" : "text-text-secondary hover:bg-surface-2"}`}
      onClick={(e) => {
        e.stopPropagation();
        onClick();
      }}
    >
      {icon}
      {label}
    </button>
  );
}
