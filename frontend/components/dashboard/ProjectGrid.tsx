import ProjectCard from "./ProjectCard";

interface Project {
  id: number;
  name: string;
  status: string;
  created_at: string;
  prompts_used?: number;
}

interface ProjectGridProps {
  projects: Project[];
}

export default function ProjectGrid({ projects }: ProjectGridProps) {
  if (projects.length === 0) {
    return (
      <div className="flex flex-col items-center justify-center py-20">
        <div className="w-16 h-16 rounded-2xl bg-surface-container-high flex items-center justify-center mb-4">
          <svg className="w-8 h-8 text-on-surface-variant" fill="none" viewBox="0 0 24 24" stroke="currentColor">
            <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={1.5} d="M12 4v16m8-8H4" />
          </svg>
        </div>
        <h3 className="text-lg font-semibold text-on-surface mb-1">No projects yet</h3>
        <p className="text-sm text-on-surface-variant">Create your first project to get started</p>
      </div>
    );
  }

  return (
    <div className="bento-grid">
      {projects.map((project) => (
        <div key={project.id} className="md:col-span-4">
          <ProjectCard project={project} />
        </div>
      ))}
    </div>
  );
}
