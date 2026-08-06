"use client";

import { useState } from "react";
import { useRouter } from "next/navigation";
import PillButton from "@/components/ui/PillButton";
import Spinner from "@/components/ui/Spinner";

interface CreateProjectProps {
  open: boolean;
  onClose: () => void;
}

export default function CreateProject({ open, onClose }: CreateProjectProps) {
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
      const token = localStorage.getItem("token");
      const res = await fetch("http://localhost:8080/v1/projects", {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          Authorization: `Bearer ${token}`,
        },
        body: JSON.stringify({ name: name.trim() }),
      });

      if (!res.ok) {
        throw new Error("Failed to create project");
      }

      const project = await res.json();
      router.push(`/project/${project.id}`);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Something went wrong");
    } finally {
      setLoading(false);
    }
  }

  return (
    <>
      <div
        className="fixed inset-0 bg-black/50 z-50"
        onClick={onClose}
      />

      <div className="fixed inset-0 flex items-center justify-center z-50 p-4">
        <div className="w-full max-w-md bg-surface-3 rounded-[20px] shadow-lg p-6">
          <div className="flex items-center justify-between mb-6">
            <h2 className="text-lg font-semibold text-text-primary">New Project</h2>
            <button
              onClick={onClose}
              className="p-2 rounded-lg hover:bg-surface-4 transition-colors"
            >
              <svg className="w-5 h-5 text-text-secondary" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M6 18L18 6M6 6l12 12" />
              </svg>
            </button>
          </div>

          <form onSubmit={handleSubmit}>
            <label className="block mb-2">
              <span className="text-xs font-mono uppercase tracking-wider text-text-secondary">
                Project Name
              </span>
              <input
                type="text"
                value={name}
                onChange={(e) => setName(e.target.value)}
                placeholder="My Solana Wallet"
                className="mt-2 w-full h-12 px-4 rounded-xl bg-surface-2 text-text-primary placeholder:text-text-muted focus:outline-none focus:ring-2 focus:ring-accent/50 transition-all"
                autoFocus
              />
            </label>

            {error && (
              <p className="text-sm text-error mt-2">{error}</p>
            )}

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
              <PillButton
                type="submit"
                disabled={loading || !name.trim()}
                className="flex-1"
              >
                {loading ? (
                  <Spinner size="sm" color="currentColor" />
                ) : (
                  "Create Project"
                )}
              </PillButton>
            </div>
          </form>
        </div>
      </div>
    </>
  );
}
