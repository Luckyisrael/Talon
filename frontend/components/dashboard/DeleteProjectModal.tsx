"use client";

import { useState, useRef, useEffect } from "react";
import { Warning2, CloseSquare } from "iconsax-reactjs";
import PillButton from "@/components/ui/PillButton";
import { projects } from "@/lib/api";

interface DeleteProjectModalProps {
  open: boolean;
  projectId: number;
  projectName: string;
  onClose: () => void;
  onDeleted: () => void;
}

export default function DeleteProjectModal({
  open,
  projectId,
  projectName,
  onClose,
  onDeleted,
}: DeleteProjectModalProps) {
  const [confirmName, setConfirmName] = useState("");
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const inputRef = useRef<HTMLInputElement>(null);

  const isValid = confirmName === projectName;

  useEffect(() => {
    if (open) {
      setConfirmName("");
      setError("");
      setTimeout(() => inputRef.current?.focus(), 100);
    }
  }, [open]);

  if (!open) return null;

  async function handleDelete() {
    if (!isValid) return;

    setLoading(true);
    setError("");

    try {
      await projects.delete(projectId);
      onDeleted();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to delete project");
    } finally {
      setLoading(false);
    }
  }

  return (
    <>
      <div className="fixed inset-0 bg-black/60 z-50" onClick={onClose} />

      <div className="fixed inset-0 flex items-center justify-center z-50 p-4">
        <div className="w-full max-w-md bg-surface-3 rounded-[20px] shadow-lg overflow-hidden">
          {/* Header */}
          <div className="flex items-center justify-between px-6 pt-6 pb-2">
            <div className="flex items-center gap-3">
              <div className="w-10 h-10 rounded-xl bg-red-500/10 flex items-center justify-center">
                <Warning2 size="20" className="text-red-400" variant="Linear" />
              </div>
              <div>
                <h2 className="text-lg font-semibold text-text-primary">Delete project</h2>
                <p className="text-xs text-text-secondary">This action cannot be undone</p>
              </div>
            </div>
            <button onClick={onClose} className="p-2 rounded-lg hover:bg-surface-4 transition-colors">
              <CloseSquare size="20" className="text-text-secondary" variant="Linear" />
            </button>
          </div>

          {/* Body */}
          <div className="px-6 py-4">
            <div className="bg-red-500/5 border border-red-500/20 rounded-xl p-4 mb-4">
              <p className="text-sm text-text-primary mb-1">
                You are about to delete <span className="font-semibold text-red-400">{projectName}</span>
              </p>
              <p className="text-xs text-text-secondary">
                This will permanently delete the project, all generated files, and all associated data. This cannot be undone.
              </p>
            </div>

            <label className="block mb-4">
              <span className="text-xs font-mono uppercase tracking-wider text-text-secondary">
                Type <span className="text-text-primary font-semibold">{projectName}</span> to confirm
              </span>
              <input
                ref={inputRef}
                type="text"
                value={confirmName}
                onChange={(e) => setConfirmName(e.target.value)}
                placeholder={projectName}
                className="mt-2 w-full h-11 px-4 rounded-xl bg-surface-2 text-text-primary placeholder:text-text-muted focus:outline-none focus:ring-2 focus:ring-red-500/50 transition-all"
                onKeyDown={(e) => {
                  if (e.key === "Enter" && isValid) handleDelete();
                }}
              />
            </label>

            {error && <p className="text-sm text-red-400 mb-4">{error}</p>}
          </div>

          {/* Footer */}
          <div className="flex gap-3 px-6 pb-6">
            <PillButton
              type="button"
              variant="secondary"
              onClick={onClose}
              disabled={loading}
              className="flex-1"
            >
              Cancel
            </PillButton>
            <button
              onClick={handleDelete}
              disabled={!isValid || loading}
              className="flex-1 h-10 rounded-full font-medium text-sm transition-colors disabled:opacity-40 disabled:cursor-not-allowed bg-red-500 text-white hover:bg-red-600"
            >
              {loading ? (
                <span className="flex items-center justify-center gap-2">
                  <span className="w-4 h-4 border-2 border-white border-t-transparent rounded-full animate-spin" />
                  Deleting...
                </span>
              ) : (
                "Delete project"
              )}
            </button>
          </div>
        </div>
      </div>
    </>
  );
}
