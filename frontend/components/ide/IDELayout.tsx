"use client";

import { useState } from "react";
import AIChat from "./AIChat";
import FileTree from "./FileTree";
import PhonePreview from "./PhonePreview";
import Terminal from "./Terminal";

interface IDELayoutProps {
  projectId: number;
  projectName: string;
}

const defaultFiles = [
  {
    name: "src",
    type: "folder" as const,
    children: [
      {
        name: "screens",
        type: "folder" as const,
        children: [
          { name: "HomeScreen.tsx", type: "file" as const },
        ],
      },
      {
        name: "components",
        type: "folder" as const,
        children: [
          { name: "ErrorBoundary.tsx", type: "file" as const },
        ],
      },
      { name: "constants.ts", type: "file" as const },
      { name: "polyfill.ts", type: "file" as const },
    ],
  },
  { name: "App.tsx", type: "file" as const },
  { name: "package.json", type: "file" as const },
  { name: "tsconfig.json", type: "file" as const },
];

export default function IDELayout({ projectId, projectName }: IDELayoutProps) {
  const [selectedFile, setSelectedFile] = useState<string>();
  const [files] = useState(defaultFiles);
  const [previewStatus] = useState<"idle" | "connecting" | "streaming" | "error">("idle");
  const [buildOutput, setBuildOutput] = useState("");

  return (
    <div className="flex flex-col h-full overflow-hidden">
      <div className="flex-1 flex overflow-hidden min-h-0">
        <div className="w-56 flex-shrink-0 bg-surface-2 hidden md:block overflow-hidden">
          <div className="h-10 flex items-center px-4 bg-surface-1">
            <span className="text-xs font-mono uppercase tracking-wider text-text-secondary">
              Files
            </span>
          </div>
          <div className="overflow-y-auto" style={{ height: "calc(100% - 40px)" }}>
            <FileTree
              files={files}
              selectedFile={selectedFile}
              onSelectFile={setSelectedFile}
            />
          </div>
        </div>

        <div className="flex-1 flex flex-col min-w-0 overflow-hidden">
          <AIChat
            projectId={projectId}
            onFilesGenerated={(newFiles) => {
              setBuildOutput(`Generated ${newFiles.length} files`);
            }}
          />
        </div>

        <div className="w-80 flex-shrink-0 hidden lg:block overflow-hidden">
          <PhonePreview status={previewStatus} />
        </div>
      </div>

      <div className="h-48 flex-shrink-0 overflow-hidden">
        <Terminal output={buildOutput} />
      </div>
    </div>
  );
}
