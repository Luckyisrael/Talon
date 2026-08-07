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

  return (
    <Link href={`/project/${project.id}`}>
      <TonalCard
        variant="elevated"
        padding="lg"
        className="group cursor-pointer hover:bg-surface-4 transition-all duration-200 h-full"
      >
        <div className="flex flex-col h-full">
          <div className="flex items-start justify-between mb-4">
            <div className="w-10 h-10 rounded-xl bg-accent/10 flex items-center justify-center">
              <span className="text-accent font-semibold text-sm">
                {project.name.charAt(0)}
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
                  className="p-1.5 rounded-lg text-text-muted hover:text-red-400 hover:bg-red-500/10 transition-colors opacity-0 group-hover:opacity-100"
                  title="Delete project"
                >
                  <Trash size="14" variant="Linear" />
                </button>
              )}
            </div>
          </div>

          <h3 className="text-base font-semibold text-text-primary mb-1 group-hover:text-accent transition-colors">
            {project.name}
          </h3>
          <p className="text-xs text-text-secondary font-mono">
            Created {new Date(project.created_at).toLocaleDateString()}
          </p>

          <div className="mt-auto pt-4">
            <div className="flex items-center justify-end">
              <ArrowRight size="16" className="text-text-secondary group-hover:text-accent transition-colors" variant="Linear" />
            </div>
          </div>
        </div>
      </TonalCard>
    </Link>
  );
}
