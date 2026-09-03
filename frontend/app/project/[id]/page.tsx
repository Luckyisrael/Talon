"use client";

import { useEffect, useState } from "react";
import { useParams, useRouter } from "next/navigation";
import { IDELayout } from "@/components/ide";
import { projects } from "@/lib/api";
import { isAuthenticated } from "@/lib/auth";
import type { Project } from "@/lib/types";

export default function ProjectPage() {
  const params = useParams();
  const router = useRouter();
  const [project, setProject] = useState<Project | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (!isAuthenticated()) {
      router.push("/login");
      return;
    }

    let cancelled = false;
    projects
      .get(Number(params.id))
      .then((data) => {
        if (!cancelled) setProject(data);
      })
      .catch((err: unknown) => {
        // A failed load used to bounce straight back to the dashboard with no
        // explanation, which looks identical to the button not working.
        if (!cancelled) {
          setError(err instanceof Error ? err.message : "Could not open that project.");
        }
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });

    return () => {
      cancelled = true;
    };
  }, [params.id, router]);

  if (loading) {
    return (
      <div className="flex h-screen items-center justify-center bg-void">
        <p className="gauge">Opening the project</p>
      </div>
    );
  }

  if (error || !project) {
    return (
      <div className="flex h-screen flex-col items-center justify-center gap-3 bg-void px-6 text-center">
        <p className="text-[17px] font-medium tracking-tight text-paper">That project would not open.</p>
        <p className="max-w-sm text-sm text-fog">{error}</p>
        <button type="button" onClick={() => router.push("/dashboard")} className="btn mt-1">
          Back to your projects
        </button>
      </div>
    );
  }

  return (
    <div className="h-screen overflow-hidden bg-void">
      <IDELayout
        projectId={project.id}
        projectName={project.name}
        projectStatus={project.status}
        onExit={() => router.push("/dashboard")}
      />
    </div>
  );
}
