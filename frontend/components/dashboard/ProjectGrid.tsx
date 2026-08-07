import { Add } from "iconsax-reactjs";
import ProjectCard from "./ProjectCard";
import type { Project } from "@/lib/types";

interface ProjectGridProps {
  projects: Project[];
  onDelete?: (project: Project) => void;
}

export default function ProjectGrid({ projects, onDelete }: ProjectGridProps) {
  if (projects.length === 0) {
    return (
      <div className="flex flex-col items-center justify-center py-20">
        <div className="w-16 h-16 rounded-2xl bg-surface-3 flex items-center justify-center mb-4">
          <Add size="32" className="text-text-secondary" variant="Linear" />
        </div>
        <h3 className="text-lg font-semibold text-text-primary mb-1">No projects yet</h3>
        <p className="text-sm text-text-secondary">Create your first project to get started</p>
      </div>
    );
  }

  return (
    <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4">
      {projects.map((project) => (
        <ProjectCard key={project.id} project={project} onDelete={onDelete} />
      ))}
    </div>
  );
}
