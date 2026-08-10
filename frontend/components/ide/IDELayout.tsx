"use client";

import { useState, useCallback, useEffect, useRef } from "react";
import { ArrowDown, ArrowUp, Message, Folder } from "iconsax-reactjs";
import AIChat, { type AIChatHandle } from "./AIChat";
import FileTree from "./FileTree";
import PhonePreview from "./PhonePreview";
import Terminal from "./Terminal";
import CodeEditor from "./CodeEditor";
import PlanViewer from "./PlanViewer";
import CommandPalette from "./CommandPalette";
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
  const [leftTab, setLeftTab] = useState<"chat" | "files">("chat");

  // Cmd+K keyboard shortcut
  useEffect(() => {
    function handleKeyDown(e: KeyboardEvent) {
      if ((e.metaKey || e.ctrlKey) && e.key === "k") {
        e.preventDefault();
        store.setCommandPaletteOpen(!store.commandPaletteOpen);
      }
    }
    document.addEventListener("keydown", handleKeyDown);
    return () => document.removeEventListener("keydown", handleKeyDown);
  }, [store]);

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
        {/* LEFT PANEL — Chat is primary, files as tab */}
        <ResizablePanel direction="horizontal" defaultSize={320} minSize={260} maxSize={500} className="hidden md:block">
          <div className="flex flex-col h-full bg-surface-1 overflow-hidden">
            {/* Tab bar */}
            <div className="h-10 flex items-center gap-1 px-2 bg-surface-1 border-b border-white/5 flex-shrink-0">
              <button
                onClick={() => setLeftTab("chat")}
                className={`flex items-center gap-1.5 px-3 h-7 rounded-lg text-xs font-medium transition-all duration-150 ${
                  leftTab === "chat"
                    ? "bg-accent/10 text-accent"
                    : "text-text-secondary hover:bg-surface-3 hover:text-text-primary"
                }`}
              >
                <Message size="12" variant="Linear" />
                Chat
              </button>
              <button
                onClick={() => setLeftTab("files")}
                className={`flex items-center gap-1.5 px-3 h-7 rounded-lg text-xs font-medium transition-all duration-150 ${
                  leftTab === "files"
                    ? "bg-accent/10 text-accent"
                    : "text-text-secondary hover:bg-surface-3 hover:text-text-primary"
                }`}
              >
                <Folder size="12" variant="Linear" />
                Files
              </button>
            </div>

            {/* Tab content */}
            <div className="flex-1 min-h-0 overflow-hidden">
              {leftTab === "chat" ? (
                <AIChat ref={aiChatRef} projectId={projectId} />
              ) : (
                <div className="flex flex-col h-full min-h-0">
                  <div className="flex-1 min-h-0 overflow-y-auto px-1 py-1">
                    {store.files.length === 0 ? (
                      <div className="flex flex-col items-center justify-center h-full text-center px-6 py-8">
                        <div className="w-12 h-12 rounded-2xl bg-surface-3 flex items-center justify-center mb-3">
                          <Folder size="24" className="text-text-muted" variant="Linear" />
                        </div>
                        <p className="text-sm text-text-secondary mb-1">No files yet</p>
                        <p className="text-xs text-text-muted mb-3">Start building with the AI assistant</p>
                        <button
                          onClick={() => setLeftTab("chat")}
                          className="h-7 px-3 rounded-full bg-accent text-background text-xs font-medium hover:bg-accent-hover transition-colors active:scale-[0.98]"
                        >
                          Open Chat
                        </button>
                      </div>
                    ) : (
                      <FileTree
                        files={store.files}
                        selectedFile={store.selectedFile}
                        onSelectFile={store.selectFile}
                        onNewFile={handleNewFile}
                        onNewFolder={handleNewFolder}
                        onRename={handleRename}
                        onDelete={handleDelete}
                      />
                    )}
                  </div>
                </div>
              )}
            </div>
          </div>
        </ResizablePanel>

        {/* CENTER COLUMN */}
        <div className="flex-1 flex flex-col min-w-0 min-h-0 overflow-hidden">
          {/* Editor / Plan / Empty state */}
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
                <div className="w-16 h-16 rounded-2xl bg-surface-3 flex items-center justify-center mb-4">
                  <Message size="32" className="text-text-muted" variant="Linear" />
                </div>
                <h3 className="text-base font-medium text-text-primary mb-1">Ready to build</h3>
                <p className="text-sm text-text-secondary max-w-xs">
                  Select a file from the tree or send a prompt to the AI assistant to start generating code
                </p>
              </div>
            )}
          </div>

          {/* Terminal — collapsed by default, drag up to open */}
          <ResizablePanel direction="vertical" defaultSize={40} minSize={40} maxSize={400} handleSide="start">
            <div className="flex flex-col h-full bg-surface-1 overflow-hidden">
              <div
                className="h-10 flex items-center justify-between px-4 cursor-pointer select-none hover:bg-surface-2/50 transition-colors duration-150 flex-shrink-0"
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

        {/* RIGHT PANEL — Phone Preview */}
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

      {/* Command Palette */}
      <CommandPalette
        open={store.commandPaletteOpen}
        onClose={() => store.setCommandPaletteOpen(false)}
        files={store.files}
        onOpenFile={store.selectFile}
        onSwitchToChat={() => setLeftTab("chat")}
        onSwitchToFiles={() => setLeftTab("files")}
        onToggleTerminal={() => store.setTerminalOpen(!store.terminalOpen)}
        onStartSession={handleStartSession}
      />
    </div>
  );
}
