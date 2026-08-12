import { Folder } from "iconsax-reactjs";
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
        <div className="w-14 h-14 rounded-2 bg-riser border border-rail flex items-center justify-center mb-4">
          <Folder size="28" className="text-dust" variant="Linear" />
        </div>
        <h3 className="text-body font-semibold text-paper mb-1">No projects yet</h3>
        <p className="text-small text-graphite">Create your first project to get started</p>
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
