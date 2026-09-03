"use client";

import { ArrowRight, Trash } from "iconsax-reactjs";
import Link from "next/link";
import TonalCard from "@/components/ui/TonalCard";
import StatusBadge from "@/components/ui/StatusBadge";
import type { Project } from "@/lib/types";

interface ProjectCardProps {
  project: Project;
  onDelete?: (project: Project) => void;
}

export default function ProjectCard({ project, onDelete }: ProjectCardProps) {
  const status = project.status === "active" ? "active" : "paused";
  const created = new Date(project.created_at).toLocaleDateString(undefined, {
    year: "numeric",
    month: "short",
    day: "numeric",
  });

  return (
    <Link href={`/project/${project.id}`} className="group block h-full">
      <TonalCard
        variant="elevated"
        padding="lg"
        className="flex h-full flex-col transition-colors hover:border-smoke"
      >
        <div className="flex items-start justify-between">
          <div className="flex h-9 w-9 items-center justify-center rounded-md border border-graphite bg-white/5">
            <span className="font-mono text-sm font-medium text-paper">
              {project.name.charAt(0).toUpperCase()}
            </span>
          </div>
          <div className="flex items-center gap-2">
            <StatusBadge variant={status} />
            {onDelete && (
              <button
                onClick={(e) => {
                  e.preventDefault();
                  e.stopPropagation();
                  onDelete(project);
                }}
                className="p-1.5 rounded-md text-ash hover:text-coral-red hover:bg-coral-red/10 transition-colors opacity-0 group-hover:opacity-100"
                title="Delete project"
              >
                <Trash size="14" variant="Linear" />
              </button>
            )}
          </div>
        </div>

        <h3 className="mt-4 mb-2 text-[15px] font-medium tracking-tight text-paper transition-colors group-hover:text-white">
          {project.name}
        </h3>

        {/* Footer readout: creation telemetry on the left, the open affordance on
            the right, parted by a hairline so the card reads as one module. */}
        <div className="mt-auto flex items-center justify-between border-t border-graphite pt-3 mt-4">
          <span className="gauge">Created {created}</span>
          <span className="inline-flex items-center gap-1 font-mono text-xs text-ash transition-colors group-hover:text-mist">
            Open
            <ArrowRight
              size="14"
              variant="Linear"
              className="transition-transform group-hover:translate-x-0.5"
            />
          </span>
        </div>
      </TonalCard>
    </Link>
  );
}
