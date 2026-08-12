"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { checkpoints as api, type Checkpoint } from "@/lib/api";

/**
 * Undo, for whole turns.
 *
 * Every build turn writes a checkpoint of the files it is about to change, and
 * the API has always been able to roll one back. Nothing in the interface ever
 * said so, which meant the only way to recover from a turn that made things
 * worse was to ask the agent to undo it in prose.
 */

interface CheckpointMenuProps {
  projectId: number;
  /** Called with the paths that were rolled back, so the editor can refetch. */
  onRestored: (paths: string[]) => void;
  disabled?: boolean;
}

function ago(iso: string): string {
  const then = Date.parse(iso);
  if (Number.isNaN(then)) return "";
  const secs = Math.max(0, Math.round((Date.now() - then) / 1000));
  if (secs < 60) return "just now";
  const mins = Math.round(secs / 60);
  if (mins < 60) return `${mins}m ago`;
  const hours = Math.round(mins / 60);
  if (hours < 24) return `${hours}h ago`;
  return `${Math.round(hours / 24)}d ago`;
}

export default function CheckpointMenu({
  projectId,
  onRestored,
  disabled,
}: CheckpointMenuProps) {
  const [open, setOpen] = useState(false);
  const [items, setItems] = useState<Checkpoint[] | null>(null);
  const [busy, setBusy] = useState<number | null>(null);
  const [confirming, setConfirming] = useState<number | null>(null);
  const [error, setError] = useState<string | null>(null);
  const rootRef = useRef<HTMLDivElement>(null);

  const load = useCallback(() => {
    api
      .list(projectId)
      .then((list) => setItems(list))
      .catch(() => setItems([]));
  }, [projectId]);

  useEffect(() => {
    if (open) load();
  }, [open, load]);

  // Close on outside click and on Escape — a menu that traps you is worse than
  // no menu.
  useEffect(() => {
    if (!open) return;
    function onDown(e: MouseEvent) {
      if (!rootRef.current?.contains(e.target as Node)) setOpen(false);
    }
    function onKey(e: KeyboardEvent) {
      if (e.key === "Escape") setOpen(false);
    }
    document.addEventListener("mousedown", onDown);
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("mousedown", onDown);
      document.removeEventListener("keydown", onKey);
    };
  }, [open]);

  async function restore(cp: Checkpoint) {
    if (confirming !== cp.id) {
      setConfirming(cp.id);
      return;
    }
    setConfirming(null);
    setError(null);
    setBusy(cp.id);
    try {
      const res = await api.restore(projectId, cp.id);
      onRestored(res?.restored ?? cp.files.map((f) => f.path));
      setOpen(false);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Could not restore that.");
    } finally {
      setBusy(null);
    }
  }

  return (
    <div ref={rootRef} className="relative">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        disabled={disabled}
        aria-expanded={open}
        aria-haspopup="menu"
        className="btn btn-ghost"
        data-active={open || undefined}
      >
        History
      </button>

      {open && (
        <div
          role="menu"
          className="rise absolute right-0 top-full z-30 mt-1 w-80 overflow-hidden rounded-2 border border-edge bg-slate shadow-[0_18px_44px_-14px_rgba(0,0,0,0.85)]"
        >
          <div className="bay-head">
            <span className="gauge">Restore point</span>
          </div>

          {items === null && <p className="gauge px-3 py-4">Loading</p>}

          {items?.length === 0 && (
            <p className="px-3 py-4 text-small text-graphite">
              Nothing to roll back yet. Each build turn saves the files it is
              about to change, and they show up here.
            </p>
          )}

          {items && items.length > 0 && (
            <ul className="max-h-72 overflow-y-auto">
              {items.map((cp) => {
                const count = cp.files?.length ?? 0;
                return (
                  <li key={cp.id} className="border-b border-rail last:border-0">
                    <div className="flex items-start gap-2 px-3 py-2">
                      <div className="min-w-0 flex-1">
                        <p className="truncate text-small text-paper" title={cp.label}>
                          {cp.label || `Checkpoint ${cp.id}`}
                        </p>
                        <p className="gauge tnum mt-0.5">
                          {ago(cp.created_at)} · {count} {count === 1 ? "file" : "files"}
                        </p>
                      </div>
                      <button
                        type="button"
                        onClick={() => void restore(cp)}
                        onBlur={() => setConfirming((c) => (c === cp.id ? null : c))}
                        disabled={busy !== null}
                        className={`btn flex-shrink-0 text-tiny ${
                          confirming === cp.id ? "btn-stop" : "btn-ghost"
                        }`}
                      >
                        {busy === cp.id
                          ? "Restoring"
                          : confirming === cp.id
                            ? "Overwrite files?"
                            : "Restore"}
                      </button>
                    </div>
                  </li>
                );
              })}
            </ul>
          )}

          {error && (
            <p className="border-t border-rail px-3 py-2 text-tiny text-fault">{error}</p>
          )}
        </div>
      )}
    </div>
  );
}
