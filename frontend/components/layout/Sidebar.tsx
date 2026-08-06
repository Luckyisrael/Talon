"use client";

import { useEffect, useRef } from "react";

interface SidebarProps {
  open: boolean;
  onClose: () => void;
}

export default function Sidebar({ open, onClose }: SidebarProps) {
  const sidebarRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    function handleClickOutside(e: MouseEvent) {
      if (sidebarRef.current && !sidebarRef.current.contains(e.target as Node)) {
        onClose();
      }
    }

    if (open) {
      document.addEventListener("mousedown", handleClickOutside);
    }

    return () => document.removeEventListener("mousedown", handleClickOutside);
  }, [open, onClose]);

  return (
    <>
      {open && (
        <div
          className="fixed inset-0 bg-black/50 z-40 md:hidden"
          onClick={onClose}
        />
      )}

      <aside
        ref={sidebarRef}
        className={`
          fixed md:static inset-y-0 left-0 z-50
          w-64 bg-surface-2
          transform transition-transform duration-200 ease-out
          ${open ? "translate-x-0" : "-translate-x-full md:translate-x-0"}
          md:block
        `}
      >
        <div className="flex flex-col h-full">
          <div className="h-14 flex items-center px-4 bg-surface-1">
            <span className="text-xs font-mono uppercase tracking-wider text-text-secondary">
              Explorer
            </span>
          </div>

          <div className="flex-1 p-4 overflow-auto">
            <p className="text-sm text-text-secondary">
              No files yet. Start building to see your project.
            </p>
          </div>
        </div>
      </aside>
    </>
  );
}
