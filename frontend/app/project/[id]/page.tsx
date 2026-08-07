"use client";

import { ArrowLeft } from "iconsax-reactjs";
import { useParams, useRouter } from "next/navigation";
import { IDELayout } from "@/components/ide";
import { Spinner } from "@/components/ui";
import { useState, useEffect } from "react";
import { projects } from "@/lib/api";
import { isAuthenticated } from "@/lib/auth";
import type { Project } from "@/lib/types";

export default function ProjectPage() {
  const params = useParams();
  const router = useRouter();
  const [project, setProject] = useState<Project | null>(null);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    if (!isAuthenticated()) {
      router.push("/login");
      return;
    }

    async function loadProject() {
      try {
        const data = await projects.get(Number(params.id));
        setProject(data);
      } catch (err) {
        console.error("Failed to load project:", err);
        router.push("/dashboard");
      } finally {
        setLoading(false);
      }
    }

    loadProject();
  }, [params.id, router]);

  if (loading) {
    return (
      <div className="h-screen flex items-center justify-center bg-background">
        <Spinner size="lg" />
      </div>
    );
  }

  if (!project) return null;

  return (
    <div className="h-screen flex flex-col bg-background">
      <div className="h-12 flex items-center justify-between px-4 bg-surface-1">
        <div className="flex items-center gap-3">
          <button onClick={() => router.push("/dashboard")} className="p-2 rounded-lg hover:bg-surface-3 transition-colors">
            <ArrowLeft size="16" className="text-text-secondary" variant="Linear" />
          </button>
          <span className="text-sm font-medium text-text-primary">{project.name}</span>
        </div>
        <div className="flex items-center gap-2">
          <span className="text-xs font-mono text-text-secondary capitalize">{project.status}</span>
        </div>
      </div>

      <div className="flex-1 overflow-hidden">
        <IDELayout projectId={project.id} projectName={project.name} />
      </div>
    </div>
  );
}
