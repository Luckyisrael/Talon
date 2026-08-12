"use client";

import { useState } from "react";
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

export default function DeleteProjectModal({ open, ...rest }: DeleteProjectModalProps) {
  // Mounting only while open is what clears the typed confirmation. Resetting it
  // from an effect on `open` instead left the previous project's name sitting in
  // the box for a render — on the one dialog whose entire job is being certain
  // which project you meant.
  if (!open) return null;
  return <Confirm {...rest} />;
}

function Confirm({
  projectId,
  projectName,
  onClose,
  onDeleted,
}: Omit<DeleteProjectModalProps, "open">) {
  const [confirmName, setConfirmName] = useState("");
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");

  const isValid = confirmName === projectName;

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
      <div className="fixed inset-0 bg-black/70 z-50" onClick={onClose} />

      <div className="fixed inset-0 flex items-center justify-center z-50 p-4">
        <div className="w-full max-w-md bay overflow-hidden rise">
          {/* Header */}
          <div className="flex items-center justify-between px-5 pt-5 pb-2">
            <div className="flex items-center gap-3">
              <div className="w-9 h-9 rounded-1 bg-fault/10 border border-fault/20 flex items-center justify-center">
                <Warning2 size="20" className="text-fault" variant="Linear" />
              </div>
              <div>
                <h2 className="text-lead font-semibold text-paper">Delete project</h2>
                <p className="text-tiny text-dust">This action cannot be undone</p>
              </div>
            </div>
            <button
              onClick={onClose}
              className="p-1.5 rounded-1 text-dust hover:text-paper hover:bg-rail transition-colors"
            >
              <CloseSquare size="20" variant="Linear" />
            </button>
          </div>

          {/* Body */}
          <div className="px-5 py-4">
            <div className="bg-fault/5 border border-fault/20 rounded-2 p-4 mb-4">
              <p className="text-small text-paper mb-1">
                You are about to delete{" "}
                <span className="font-semibold text-fault">{projectName}</span>
              </p>
              <p className="text-tiny text-graphite">
                This will permanently delete the project, all generated files, and all
                associated data. This cannot be undone.
              </p>
            </div>

            <label className="block mb-4">
              <span className="gauge">
                Type <span className="text-paper">{projectName}</span> to confirm
              </span>
              <input
                autoFocus
                type="text"
                value={confirmName}
                onChange={(e) => setConfirmName(e.target.value)}
                placeholder={projectName}
                className="field mt-2"
                onKeyDown={(e) => {
                  if (e.key === "Enter" && isValid) handleDelete();
                }}
              />
            </label>

            {error && <p className="text-small text-fault mb-4">{error}</p>}
          </div>

          {/* Footer */}
          <div className="flex gap-3 px-5 pb-5">
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
              className="flex-1 inline-flex items-center justify-center gap-2 h-9 rounded-1 text-small font-medium transition-colors disabled:opacity-40 disabled:cursor-not-allowed bg-fault text-ink hover:brightness-110"
            >
              {loading ? (
                <>
                  <span className="w-4 h-4 border-2 border-ink border-t-transparent rounded-full animate-spin" />
                  Deleting...
                </>
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
