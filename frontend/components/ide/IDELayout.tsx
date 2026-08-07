"use client";

import { useCallback, useEffect, useRef } from "react";
import { ArrowDown, ArrowUp, Message } from "iconsax-reactjs";
import AIChat, { type AIChatHandle } from "./AIChat";
import FileTree from "./FileTree";
import PhonePreview from "./PhonePreview";
import Terminal from "./Terminal";
import CodeEditor from "./CodeEditor";
import PlanViewer from "./PlanViewer";
import ResizablePanel from "../ui/ResizablePanel";
import { useIDEStore } from "@/lib/store";
import { projects } from "@/lib/api";

interface IDELayoutProps {
  projectId: number;
  projectName: string;
}

export default function IDELayout({ projectId, projectName }: IDELayoutProps) {
  const store = useIDEStore();
  const aiChatRef = useRef<AIChatHandle>(null);

  // Load existing project files on mount (exclude scaffold)
  useEffect(() => {
    async function loadFiles() {
      try {
        const files = await projects.getFiles(projectId, false);
        if (files.length > 0) {
          store.setFilesFromEntries(files);
        }
      } catch {
        // Silently ignore — files will be populated by AI responses
      }
    }
    loadFiles();
  }, [projectId]);

  const handleStartSession = useCallback(async () => {
    store.setPreviewStatus("connecting");
    try {
      const { sessions } = await import("@/lib/api");
      const data = await sessions.create(projectId);
      store.setSessionId(data.id);
      store.setPreviewStatus("streaming");
    } catch {
      store.setPreviewStatus("error");
    }
  }, [projectId]);

  const handlePlanApproved = useCallback(() => {
    const plan = store.currentPlan;
    if (!plan || !aiChatRef.current) return;
    store.setCurrentPlan(null);
    aiChatRef.current.switchToBuild();
    const screenNames = plan.plan.screens.map((s) => s.name).join(", ");
    aiChatRef.current.sendMessage(
      `Build the following screens: ${screenNames}. Generate all necessary components with full implementation.`,
    );
  }, [store]);

  const handleNewFile = useCallback(
    async (parentPath: string, name: string) => {
      const filePath = parentPath ? `${parentPath}/${name}` : name;
      await projects.writeFiles(projectId, [{ path: filePath, content: "" }]);
      store.addFiles([{ path: filePath, content: "" }]);
    },
    [projectId, store],
  );

  const handleNewFolder = useCallback(
    async (parentPath: string, name: string) => {
      // Create a .gitkeep inside the folder so it exists on disk
      const folderPath = parentPath ? `${parentPath}/${name}` : name;
      const gitkeep = `${folderPath}/.gitkeep`;
      await projects.writeFiles(projectId, [{ path: gitkeep, content: "" }]);
      store.addFiles([{ path: gitkeep, content: "" }]);
    },
    [projectId, store],
  );

  const handleRename = useCallback(
    async (oldPath: string, newName: string) => {
      const parts = oldPath.split("/");
      parts[parts.length - 1] = newName;
      const newPath = parts.join("/");

      const oldContent = store.fileContents[oldPath] || "";
      await projects.writeFiles(projectId, [{ path: newPath, content: oldContent }]);
      await projects.deleteFile(projectId, oldPath);
      store.removeFileByPath(oldPath);
      store.addFiles([{ path: newPath, content: oldContent }]);
    },
    [projectId, store],
  );

  const handleDelete = useCallback(
    async (path: string) => {
      await projects.deleteFile(projectId, path);
      store.removeFileByPath(path);
    },
    [projectId, store],
  );

  const selectedContent = store.selectedFile ? store.fileContents[store.selectedFile] : undefined;

  return (
    <div className="flex flex-col h-full overflow-hidden bg-background">
      <div className="flex-1 flex overflow-hidden min-h-0">
        {/* LEFT PANEL */}
        <ResizablePanel direction="horizontal" defaultSize={240} minSize={180} maxSize={400} className="hidden md:block">
          <div className="flex flex-col h-full bg-surface-1 overflow-hidden">
            {/* File Tree */}
            <div className="flex flex-col flex-1 min-h-0">
              <div className="h-10 flex items-center px-4 bg-surface-2 flex-shrink-0">
                <span className="text-[11px] font-mono uppercase tracking-wider text-text-muted">Files</span>
              </div>
              <div className="flex-1 min-h-0 overflow-y-auto px-1 py-1">
                <FileTree
                  files={store.files}
                  selectedFile={store.selectedFile}
                  onSelectFile={store.selectFile}
                  onNewFile={handleNewFile}
                  onNewFolder={handleNewFolder}
                  onRename={handleRename}
                  onDelete={handleDelete}
                />
              </div>
            </div>

            <div className="h-px bg-surface-3 flex-shrink-0" />

            {/* AI Chat */}
            <div className="flex flex-col flex-1 min-h-0">
              <div className="h-10 flex items-center gap-2 px-4 bg-surface-2 flex-shrink-0">
                <Message size="13" className="text-accent" variant="Linear" />
                <span className="text-[11px] font-mono uppercase tracking-wider text-text-muted">AI Assistant</span>
              </div>
              <div className="flex-1 min-h-0 overflow-hidden">
                <AIChat ref={aiChatRef} projectId={projectId} />
              </div>
            </div>
          </div>
        </ResizablePanel>

        {/* CENTER COLUMN */}
        <div className="flex-1 flex flex-col min-w-0 min-h-0 overflow-hidden">
          {/* Editor / Plan */}
          <div className="flex-1 min-h-0 overflow-hidden bg-surface-2">
            {store.currentPlan ? (
              <PlanViewer
                message={store.currentPlan.message}
                plan={store.currentPlan.plan}
                onApprove={handlePlanApproved}
              />
            ) : store.selectedFile && selectedContent !== undefined ? (
              <CodeEditor filePath={store.selectedFile} content={selectedContent} />
            ) : (
              <div className="flex flex-col items-center justify-center h-full text-center px-8">
                <div className="w-14 h-14 rounded-2xl bg-surface-3 flex items-center justify-center mb-4">
                  <Message size="28" className="text-text-muted" variant="Linear" />
                </div>
                <p className="text-sm text-text-secondary mb-1">Select a file to view code</p>
                <p className="text-xs text-text-muted">Or use the AI assistant to generate new files</p>
              </div>
            )}
          </div>

          {/* Terminal */}
          <ResizablePanel direction="vertical" defaultSize={200} minSize={40} maxSize={400} handleSide="start">
            <div className="flex flex-col h-full bg-surface-1 overflow-hidden">
              <div
                className="h-10 flex items-center justify-between px-4 cursor-pointer select-none hover:bg-surface-2 transition-colors flex-shrink-0"
                onClick={() => store.setTerminalOpen(!store.terminalOpen)}
              >
                <span className="text-[11px] font-mono uppercase tracking-wider text-text-muted">Terminal</span>
                {store.terminalOpen ? (
                  <ArrowDown size="14" className="text-text-muted" variant="Linear" />
                ) : (
                  <ArrowUp size="14" className="text-text-muted" variant="Linear" />
                )}
              </div>
              {store.terminalOpen && (
                <div className="flex-1 min-h-0 overflow-hidden">
                  <Terminal sessionId={store.sessionId} />
                </div>
              )}
            </div>
          </ResizablePanel>
        </div>

        {/* RIGHT PANEL */}
        <ResizablePanel direction="horizontal" defaultSize={288} minSize={200} maxSize={500} className="hidden lg:block" handleSide="start">
          <div className="flex flex-col h-full bg-surface-1 overflow-hidden">
            <PhonePreview
              status={store.previewStatus}
              sessionId={store.sessionId}
              onStartSession={handleStartSession}
            />
          </div>
        </ResizablePanel>
      </div>
    </div>
  );
}
