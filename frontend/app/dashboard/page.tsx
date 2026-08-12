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

/**
 * Fetches without touching state, so the caller keeps its setState calls on the
 * awaited side of the effect boundary. Pulled out of the component because a
 * loader that both fetches and assigns cannot be reused by the effect and the
 * refresh handlers without one of them cascading a render.
 */
async function fetchDashboard() {
  const [projectsData, usageData] = await Promise.all([projectsApi.list(), usageApi.get()]);
  return { projectList: projectsData || [], tier: usageData.tier };
}

export default function DashboardPage() {
  const router = useRouter();
  const [projectList, setProjectList] = useState<Project[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [createOpen, setCreateOpen] = useState(false);
  const [user, setUser] = useState<{ email: string; tier: string } | null>(null);
  const [deleteTarget, setDeleteTarget] = useState<Project | null>(null);
  const [reloadKey, setReloadKey] = useState(0);

  // Refetching from a button or after a mutation asks the effect to run again
  // rather than kicking off a second loader of its own. One code path means two
  // overlapping refreshes cannot land out of order, which is what `cancelled`
  // below settles.
  const refresh = useCallback(() => {
    setLoading(true);
    setError(null);
    setReloadKey((k) => k + 1);
  }, []);

  useEffect(() => {
    if (!isAuthenticated()) {
      router.push("/login");
      return;
    }

    let cancelled = false;
    void (async () => {
      try {
        const data = await fetchDashboard();
        if (cancelled) return;
        setProjectList(data.projectList);
        setUser({ email: "user@example.com", tier: data.tier });
        setError(null);
      } catch (err) {
        if (cancelled) return;
        console.error("Failed to load dashboard:", err);
        setError("Failed to load projects. Please try again.");
      } finally {
        if (!cancelled) setLoading(false);
      }
    })();

    return () => {
      cancelled = true;
    };
  }, [router, reloadKey]);

  return (
    <AppShell>
      <div className="max-w-5xl mx-auto px-4 py-8">
        <div className="flex flex-col sm:flex-row items-start sm:items-center justify-between gap-4 mb-8">
          <div>
            <h1 className="text-title font-semibold text-paper mb-1">Projects</h1>
            <p className="text-small text-graphite">Build and manage your Solana Mobile apps</p>
          </div>

          <div className="flex items-center gap-3">
            {user && (
              <div className="hidden sm:flex items-center gap-2 text-tiny text-graphite">
                <span className="font-mono">{user.email}</span>
                <StatusBadge variant={user.tier as "free" | "pro"} />
              </div>
            )}
            <PillButton onClick={() => setCreateOpen(true)}>
              <Add size="16" variant="Linear" />
              New project
            </PillButton>
          </div>
        </div>

        {/* Loading state */}
        {loading && (
          <div className="flex flex-col items-center justify-center py-20">
            <Spinner size="lg" />
            <p className="text-small text-dust mt-4">Loading projects…</p>
          </div>
        )}

        {/* Error state */}
        {!loading && error && (
          <div className="flex flex-col items-center justify-center py-20">
            <div className="w-14 h-14 rounded-2 bg-fault/10 border border-fault/20 flex items-center justify-center mb-4">
              <Warning2 size="28" className="text-fault" variant="Linear" />
            </div>
            <p className="text-small text-graphite mb-3">{error}</p>
            <PillButton variant="secondary" onClick={refresh}>
              Try again
            </PillButton>
          </div>
        )}

        {/* Empty state */}
        {!loading && !error && projectList.length === 0 && (
          <div className="flex flex-col items-center justify-center py-20">
            <div className="w-14 h-14 rounded-2 bg-riser border border-rail flex items-center justify-center mb-4">
              <Folder size="28" className="text-dust" variant="Linear" />
            </div>
            <h3 className="text-body font-semibold text-paper mb-1">No projects yet</h3>
            <p className="text-small text-graphite mb-4">Create your first Solana Mobile app</p>
            <PillButton onClick={() => setCreateOpen(true)}>
              <Add size="16" variant="Linear" />
              Create project
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

      <CreateProject open={createOpen} onClose={() => setCreateOpen(false)} onCreated={refresh} />

      {deleteTarget && (
        <DeleteProjectModal
          open={!!deleteTarget}
          projectId={deleteTarget.id}
          projectName={deleteTarget.name}
          onClose={() => setDeleteTarget(null)}
          onDeleted={() => {
            setDeleteTarget(null);
            refresh();
          }}
        />
      )}
    </AppShell>
  );
}
