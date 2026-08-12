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
      <div className="flex h-screen items-center justify-center bg-ink">
        <p className="gauge">Opening the project</p>
      </div>
    );
  }

  if (error || !project) {
    return (
      <div className="flex h-screen flex-col items-center justify-center gap-3 bg-ink px-6 text-center">
        <p className="text-lead text-paper">That project would not open.</p>
        <p className="max-w-sm text-small text-graphite">{error}</p>
        <button type="button" onClick={() => router.push("/dashboard")} className="btn mt-1">
          Back to your projects
        </button>
      </div>
    );
  }

  return (
    <div className="flex h-screen flex-col overflow-hidden bg-ink">
      {/* The identity rail: whose project this is and what state it is in. The
          machine's own status lives one row down, in the IDE. */}
      <header className="flex h-10 flex-shrink-0 items-center gap-3 border-b border-rail bg-slate px-3">
        <button
          type="button"
          onClick={() => router.push("/dashboard")}
          className="btn btn-ghost gap-1.5"
          aria-label="Back to your projects"
        >
          <span aria-hidden>←</span>
          Projects
        </button>

        <span className="hairline h-4 w-px flex-shrink-0" />

        <h1 className="min-w-0 truncate text-small text-paper" title={project.name}>
          {project.name}
        </h1>

        <span className="gauge ml-auto flex-shrink-0">{project.status}</span>
      </header>

      <div className="min-h-0 flex-1 overflow-hidden">
        <IDELayout projectId={project.id} />
      </div>
    </div>
  );
}
