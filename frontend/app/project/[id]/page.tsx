"use client";

import { useState, useEffect } from "react";
import { useParams, useRouter } from "next/navigation";
import { IDELayout } from "@/components/ide";
import { Spinner } from "@/components/ui";

interface Project {
  id: number;
  name: string;
  status: string;
}

export default function ProjectPage() {
  const params = useParams();
  const router = useRouter();
  const [project, setProject] = useState<Project | null>(null);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    const token = localStorage.getItem("token");
    if (!token) {
      router.push("/login");
      return;
    }

    async function loadProject() {
      try {
        const res = await fetch(`http://localhost:8080/v1/projects/${params.id}`, {
          headers: { Authorization: `Bearer ${token}` },
        });

        if (!res.ok) {
          throw new Error("Project not found");
        }

        const data = await res.json();
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
      <div className="h-screen flex items-center justify-center bg-surface">
        <Spinner size="lg" />
      </div>
    );
  }

  if (!project) {
    return null;
  }

  return (
    <div className="h-screen flex flex-col bg-surface">
      {/* Top bar */}
      <div className="h-12 flex items-center justify-between px-4 hairline-bottom bg-surface-container-low">
        <div className="flex items-center gap-3">
          <button
            onClick={() => router.push("/dashboard")}
            className="p-2 rounded-lg hover:bg-surface-container-high transition-colors"
          >
            <svg className="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor">
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M15 19l-7-7 7-7" />
            </svg>
          </button>
          <span className="text-sm font-medium text-on-surface">{project.name}</span>
        </div>
        <div className="flex items-center gap-2">
          <span className="text-xs font-mono text-on-surface-variant capitalize">
            {project.status}
          </span>
        </div>
      </div>

      {/* IDE */}
      <div className="flex-1 overflow-hidden">
        <IDELayout projectId={project.id} projectName={project.name} />
      </div>
    </div>
  );
}
