"use client";

import { Add } from "iconsax-reactjs";
import AppShell from "@/components/layout/AppShell";
import { PillButton, StatusBadge, Spinner } from "@/components/ui";
import { ProjectGrid, CreateProject } from "@/components/dashboard";
import { useState, useEffect, useCallback } from "react";
import { useRouter } from "next/navigation";

interface Project {
  id: number;
  name: string;
  status: string;
  created_at: string;
  prompts_used?: number;
}

export default function DashboardPage() {
  const router = useRouter();
  const [projects, setProjects] = useState<Project[]>([]);
  const [loading, setLoading] = useState(true);
  const [createOpen, setCreateOpen] = useState(false);
  const [user, setUser] = useState<{ email: string; tier: string } | null>(null);

  const loadProjects = useCallback(async () => {
    const token = localStorage.getItem("token");
    if (!token) {
      router.push("/login");
      return;
    }

    try {
      const [projectsRes, usageRes] = await Promise.all([
        fetch("http://localhost:8080/v1/projects", {
          headers: { Authorization: `Bearer ${token}` },
        }),
        fetch("http://localhost:8080/v1/usage", {
          headers: { Authorization: `Bearer ${token}` },
        }),
      ]);

      if (!projectsRes.ok || !usageRes.ok) {
        throw new Error("Failed to load data");
      }

      const projectsData = await projectsRes.json();
      const usageData = await usageRes.json();

      setProjects(projectsData || []);
      setUser({ email: "user@example.com", tier: usageData.tier });
    } catch (err) {
      console.error("Failed to load dashboard:", err);
    } finally {
      setLoading(false);
    }
  }, [router]);

  useEffect(() => {
    loadProjects();
  }, [loadProjects]);

  return (
    <AppShell>
      <div className="max-w-5xl mx-auto px-4 py-8">
        <div className="flex flex-col sm:flex-row items-start sm:items-center justify-between gap-4 mb-8">
          <div>
            <h1 className="text-2xl font-bold text-text-primary mb-1">Projects</h1>
            <p className="text-sm text-text-secondary">
              Build and manage your Solana Mobile apps
            </p>
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
          <ProjectGrid projects={projects} />
        )}
      </div>

      <CreateProject
        open={createOpen}
        onClose={() => setCreateOpen(false)}
        onCreated={loadProjects}
      />
    </AppShell>
  );
}
