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
        className="group cursor-pointer hover:bg-rail hover:border-edge h-full"
      >
        <div className="flex flex-col h-full">
          <div className="flex items-start justify-between mb-4">
            <div className="w-9 h-9 rounded-1 bg-riser border border-rail flex items-center justify-center">
              <span className="text-paper font-semibold text-small">
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
                  className="p-1.5 rounded-1 text-dust hover:text-fault hover:bg-fault/10 transition-colors opacity-0 group-hover:opacity-100"
                  title="Delete project"
                >
                  <Trash size="14" variant="Linear" />
                </button>
              )}
            </div>
          </div>

          <h3 className="text-body font-semibold text-paper mb-1 group-hover:text-white transition-colors">
            {project.name}
          </h3>
          <p className="text-tiny text-dust font-mono">
            Created {new Date(project.created_at).toLocaleDateString()}
          </p>

          <div className="mt-auto pt-4">
            <div className="flex items-center justify-end">
              <ArrowRight
                size="16"
                className="text-dust group-hover:text-paper transition-colors"
                variant="Linear"
              />
            </div>
          </div>
        </div>
      </TonalCard>
    </Link>
  );
}
