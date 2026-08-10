"use client";

import { Add, Warning2, Folder } from "iconsax-reactjs";
import AppShell from "@/components/layout/AppShell";
import { PillButton, StatusBadge, Spinner } from "@/components/ui";
import { ProjectGrid, CreateProject, DeleteProjectModal } from "@/components/dashboard";
import { useState, useEffect, useCallback } from "react";
import { useRouter } from "next/navigation";
import { projects as projectsApi, usage as usageApi } from "@/lib/api";
import { isAuthenticated } from "@/lib/auth";
import type { Project } from "@/lib/types";

export default function DashboardPage() {
  const router = useRouter();
  const [projectList, setProjectList] = useState<Project[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [createOpen, setCreateOpen] = useState(false);
  const [user, setUser] = useState<{ email: string; tier: string } | null>(null);
  const [deleteTarget, setDeleteTarget] = useState<Project | null>(null);

  const loadData = useCallback(async () => {
    if (!isAuthenticated()) {
      router.push("/login");
      return;
    }

    try {
      setLoading(true);
      setError(null);
      const [projectsData, usageData] = await Promise.all([projectsApi.list(), usageApi.get()]);
      setProjectList(projectsData || []);
      setUser({ email: "user@example.com", tier: usageData.tier });
    } catch (err) {
      console.error("Failed to load dashboard:", err);
      setError("Failed to load projects. Please try again.");
    } finally {
      setLoading(false);
    }
  }, [router]);

  useEffect(() => { loadData(); }, [loadData]);

  return (
    <AppShell>
      <div className="max-w-5xl mx-auto px-4 py-8">
        <div className="flex flex-col sm:flex-row items-start sm:items-center justify-between gap-4 mb-8">
          <div>
            <h1 className="text-2xl font-bold text-text-primary mb-1">Projects</h1>
            <p className="text-sm text-text-secondary">Build and manage your Solana Mobile apps</p>
          </div>

          <div className="flex items-center gap-3">
            {user && (
              <div className="hidden sm:flex items-center gap-2 text-xs text-text-secondary">
                <span className="font-mono">{user.email}</span>
                <StatusBadge variant={user.tier as "free" | "pro"} />
              </div>
            )}
            <PillButton onClick={() => setCreateOpen(true)}>
              <Add size="16" variant="Linear" />
              New Project
            </PillButton>
          </div>
        </div>

        {/* Loading state */}
        {loading && (
          <div className="flex flex-col items-center justify-center py-20">
            <Spinner size="lg" />
            <p className="text-sm text-text-muted mt-4">Loading projects...</p>
          </div>
        )}

        {/* Error state */}
        {!loading && error && (
          <div className="flex flex-col items-center justify-center py-20">
            <div className="w-14 h-14 rounded-2xl bg-error/10 flex items-center justify-center mb-4">
              <Warning2 size="28" className="text-error" variant="Linear" />
            </div>
            <p className="text-sm text-text-secondary mb-3">{error}</p>
            <PillButton variant="secondary" onClick={loadData}>
              Try Again
            </PillButton>
          </div>
        )}

        {/* Empty state */}
        {!loading && !error && projectList.length === 0 && (
          <div className="flex flex-col items-center justify-center py-20">
            <div className="w-14 h-14 rounded-2xl bg-surface-3 flex items-center justify-center mb-4">
              <Folder size="28" className="text-text-muted" variant="Linear" />
            </div>
            <h3 className="text-base font-medium text-text-primary mb-1">No projects yet</h3>
            <p className="text-sm text-text-secondary mb-4">Create your first Solana Mobile app</p>
            <PillButton onClick={() => setCreateOpen(true)}>
              <Add size="16" variant="Linear" />
              Create Project
            </PillButton>
          </div>
        )}

        {/* Project grid */}
        {!loading && !error && projectList.length > 0 && (
          <ProjectGrid
            projects={projectList}
            onDelete={(project) => setDeleteTarget(project)}
          />
        )}
      </div>

      <CreateProject open={createOpen} onClose={() => setCreateOpen(false)} onCreated={loadData} />

      {deleteTarget && (
        <DeleteProjectModal
          open={!!deleteTarget}
          projectId={deleteTarget.id}
          projectName={deleteTarget.name}
          onClose={() => setDeleteTarget(null)}
          onDeleted={() => {
            setDeleteTarget(null);
            loadData();
          }}
        />
      )}
    </AppShell>
  );
}
