"use client";

import { useState } from "react";
import { useRouter } from "next/navigation";
import { CloseSquare } from "iconsax-reactjs";
import PillButton from "@/components/ui/PillButton";
import Spinner from "@/components/ui/Spinner";
import { projects } from "@/lib/api";

interface CreateProjectProps {
  open: boolean;
  onClose: () => void;
  onCreated?: () => void;
}

export default function CreateProject({ open, onClose, onCreated }: CreateProjectProps) {
  const router = useRouter();
  const [name, setName] = useState("");
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");

  if (!open) return null;

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    if (!name.trim()) return;

    setLoading(true);
    setError("");

    try {
      const project = await projects.create(name.trim());
      onCreated?.();
      router.push(`/project/${project.id}`);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Something went wrong");
    } finally {
      setLoading(false);
    }
  }

  return (
    <>
      <div className="fixed inset-0 bg-black/60 z-50" onClick={onClose} />

      <div className="fixed inset-0 flex items-center justify-center z-50 p-4">
        <div className="w-full max-w-md bay p-5 rise">
          <div className="flex items-center justify-between mb-5">
            <h2 className="text-lead font-semibold text-paper">New project</h2>
            <button
              onClick={onClose}
              className="p-1.5 rounded-1 text-dust hover:text-paper hover:bg-rail transition-colors"
            >
              <CloseSquare size="20" variant="Linear" />
            </button>
          </div>

          <form onSubmit={handleSubmit}>
            <label className="block">
              <span className="gauge">Project name</span>
              <input
                type="text"
                value={name}
                onChange={(e) => setName(e.target.value)}
                placeholder="My Solana Wallet"
                className="field mt-2"
                autoFocus
              />
            </label>

            {error && <p className="text-small text-fault mt-2">{error}</p>}

            <div className="flex gap-3 mt-6">
              <PillButton
                type="button"
                variant="secondary"
                onClick={onClose}
                disabled={loading}
                className="flex-1"
              >
                Cancel
              </PillButton>
              <PillButton type="submit" disabled={loading || !name.trim()} className="flex-1">
                {loading ? <Spinner size="sm" color="currentColor" /> : "Create project"}
              </PillButton>
            </div>
          </form>
        </div>
      </div>
    </>
  );
}
