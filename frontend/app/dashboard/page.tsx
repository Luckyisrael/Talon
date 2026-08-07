"use client";

import { Add } from "iconsax-reactjs";
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
  const [createOpen, setCreateOpen] = useState(false);
  const [user, setUser] = useState<{ email: string; tier: string } | null>(null);

  // Delete modal state
  const [deleteTarget, setDeleteTarget] = useState<Project | null>(null);

  const loadData = useCallback(async () => {
    if (!isAuthenticated()) {
      router.push("/login");
      return;
    }

    try {
      const [projectsData, usageData] = await Promise.all([projectsApi.list(), usageApi.get()]);
      setProjectList(projectsData || []);
      setUser({ email: "user@example.com", tier: usageData.tier });
    } catch (err) {
      console.error("Failed to load dashboard:", err);
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

        {loading ? (
          <div className="flex items-center justify-center py-20">
            <Spinner size="lg" />
          </div>
        ) : (
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
