"use client";

import Link from "next/link";
import TonalCard from "@/components/ui/TonalCard";
import StatusBadge from "@/components/ui/StatusBadge";

interface Project {
  id: number;
  name: string;
  status: string;
  created_at: string;
  prompts_used?: number;
}

interface ProjectCardProps {
  project: Project;
}

export default function ProjectCard({ project }: ProjectCardProps) {
  const status = project.status === "active" ? "active" : "paused";

  return (
    <Link href={`/project/${project.id}`}>
      <TonalCard
        variant="elevated"
        padding="lg"
        className="group cursor-pointer hover:shadow-lg transition-all duration-200 h-full"
      >
        <div className="flex flex-col h-full">
          {/* Header */}
          <div className="flex items-start justify-between mb-4">
            <div className="w-10 h-10 rounded-xl bg-primary/10 flex items-center justify-center">
              <span className="text-primary font-semibold text-sm">
                {project.name.charAt(0)}
              </span>
            </div>
            <StatusBadge variant={status} />
          </div>

          {/* Content */}
          <h3 className="text-base font-semibold text-on-surface mb-1 group-hover:text-primary transition-colors">
            {project.name}
          </h3>
          <p className="text-xs text-on-surface-variant font-mono">
            Created {new Date(project.created_at).toLocaleDateString()}
          </p>

          {/* Footer */}
          <div className="mt-auto pt-4 hairline-top">
            <div className="flex items-center justify-between">
              <span className="text-xs text-on-surface-variant">
                {project.prompts_used || 0} prompts used
              </span>
              <svg
                className="w-4 h-4 text-on-surface-variant group-hover:text-primary transition-colors"
                fill="none"
                viewBox="0 0 24 24"
                stroke="currentColor"
              >
                <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M9 5l7 7-7 7" />
              </svg>
            </div>
          </div>
        </div>
      </TonalCard>
    </Link>
  );
}
