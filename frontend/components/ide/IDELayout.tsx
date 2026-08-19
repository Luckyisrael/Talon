"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { projects, sessions } from "@/lib/api";
import { useIDEStore } from "@/lib/store";
import type { PlanData } from "@/lib/types";
import ResizablePanel from "../ui/ResizablePanel";
import AIChat, { type AIChatHandle } from "./AIChat";
import CheckpointMenu from "./CheckpointMenu";
import CodeEditor from "./CodeEditor";
import CommandPalette from "./CommandPalette";
import FileTree from "./FileTree";
import PhonePreview from "./PhonePreview";
import PlanViewer from "./PlanViewer";
import Terminal from "./Terminal";

interface IDELayoutProps {
  projectId: number;
}

export default function IDELayout({ projectId }: IDELayoutProps) {
  const store = useIDEStore();
  const aiChatRef = useRef<AIChatHandle>(null);
  const [leftTab, setLeftTab] = useState<"chat" | "files">("chat");

  // ── Files ──

  const refetchFiles = useCallback(async () => {
    try {
      const files = await projects.getFiles(projectId, false);
      useIDEStore.getState().setFilesFromEntries(files);
    } catch {
      // The tree keeps whatever it already had rather than emptying itself.
    }
  }, [projectId]);

  useEffect(() => {
    void refetchFiles();
  }, [refetchFiles]);

  // A finished turn reports which paths it touched. Refetching their real
  // content is the fix for what used to happen: the paths were inserted with
  // empty content, so opening a file the agent had just written showed a blank
  // editor over the top of working code.
  const handleFilesTouched = useCallback(
    async (paths: string[]) => {
      if (paths.length === 0) return;
      await refetchFiles();
      const state = useIDEStore.getState();
      if (!state.selectedFile && paths.length > 0) {
        // Show the person what just got written instead of leaving the centre
        // panel on its empty state.
        const first = paths.find((p) => /\.(tsx?|jsx?|json|css|md)$/.test(p)) ?? paths[0];
        state.selectFile(first);
      }
    },
    [refetchFiles],
  );

  // ── Device ──

  const startSession = useCallback(async () => {
    const state = useIDEStore.getState();
    state.setPreviewStatus("connecting");
    try {
      const session = await sessions.create(projectId);
      state.setSessionId(session.id);
      state.setAdbTarget(session.adb_port ? `localhost:${session.adb_port}` : null);
      state.setPreviewStatus("streaming");
    } catch {
      state.setPreviewStatus("error");
    }
  }, [projectId]);

  const stopSession = useCallback(async () => {
    const state = useIDEStore.getState();
    const id = state.sessionId;
    if (!id) return;
    state.setPreviewStatus("idle");
    state.setSessionId(null);
    state.setAdbTarget(null);
    try {
      await sessions.destroy(id);
    } catch {
      // The container may already be gone; the panel is right either way.
    }
  }, []);

  // ── Plan ──

  const handlePlan = useCallback((plan: PlanData, message: string) => {
    useIDEStore.getState().setCurrentPlan({ plan, message });
  }, []);

  // One approval path, reachable from the card in the chat and from the full
  // plan in the centre panel.
  const approvePlan = useCallback((plan: PlanData) => {
    const state = useIDEStore.getState();
    state.setCurrentPlan(null);
    const screens = (plan.screens ?? []).map((s) => s.name).filter(Boolean);
    const list = screens.length > 0 ? screens.join(", ") : "the plan we just agreed on";
    aiChatRef.current?.switchToBuild();
    aiChatRef.current?.sendMessage(
      `Build ${list}. Implement every screen fully — no placeholders, no TODOs — and wire up the navigation from the plan.`,
      "build",
    );
  }, []);

  // ── File operations ──

  const handleNewFile = useCallback(
    async (parentPath: string, name: string) => {
      const filePath = parentPath ? `${parentPath}/${name}` : name;
      await projects.writeFiles(projectId, [{ path: filePath, content: "" }]);
      const state = useIDEStore.getState();
      state.addFiles([{ path: filePath, content: "" }]);
      state.selectFile(filePath);
    },
    [projectId],
  );

  const handleNewFolder = useCallback(
    async (parentPath: string, name: string) => {
      const folderPath = parentPath ? `${parentPath}/${name}` : name;
      // Git and the workspace walker both ignore empty directories, so the
      // folder only exists once something is in it.
      const gitkeep = `${folderPath}/.gitkeep`;
      await projects.writeFiles(projectId, [{ path: gitkeep, content: "" }]);
      useIDEStore.getState().addFiles([{ path: gitkeep, content: "" }]);
    },
    [projectId],
  );

  const handleRename = useCallback(
    async (oldPath: string, newName: string) => {
      const parts = oldPath.split("/");
      parts[parts.length - 1] = newName;
      const newPath = parts.join("/");
      if (newPath === oldPath) return;

      const state = useIDEStore.getState();
      // Prefer the unsaved draft: renaming a file should not quietly discard
      // the edit that is on screen.
      const body = state.drafts[oldPath] ?? state.fileContents[oldPath] ?? "";

      await projects.writeFiles(projectId, [{ path: newPath, content: body }]);
      await projects.deleteFile(projectId, oldPath);

      const wasOpen = state.selectedFile === oldPath;
      state.removeFileByPath(oldPath);
      state.addFiles([{ path: newPath, content: body }]);
      if (wasOpen) state.selectFile(newPath);
    },
    [projectId],
  );

  const handleDelete = useCallback(
    async (path: string) => {
      await projects.deleteFile(projectId, path);
      useIDEStore.getState().removeFileByPath(path);
    },
    [projectId],
  );

  // ── Shortcuts ──

  useEffect(() => {
    function onKeyDown(e: KeyboardEvent) {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "k") {
        e.preventDefault();
        const state = useIDEStore.getState();
        state.setCommandPaletteOpen(!state.commandPaletteOpen);
      }
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "j") {
        e.preventDefault();
        const state = useIDEStore.getState();
        state.setTerminalOpen(!state.terminalOpen);
      }
    }
    document.addEventListener("keydown", onKeyDown);
    return () => document.removeEventListener("keydown", onKeyDown);
  }, []);

  const selectedContent = store.selectedFile
    ? store.fileContents[store.selectedFile]
    : undefined;
  const deviceLive = store.previewStatus === "streaming";

  return (
    <div className="flex h-full min-h-0 flex-col overflow-hidden bg-ink">
      {/* ── Rail ── The one place that says what the machine is doing. */}
      <div className="flex h-9 flex-shrink-0 items-center gap-2 border-b border-rail bg-slate px-2">
        <div className="flex min-w-0 items-center gap-1">
          {(["chat", "files"] as const).map((tab) => (
            <button
              key={tab}
              type="button"
              onClick={() => setLeftTab(tab)}
              aria-pressed={leftTab === tab}
              className="btn btn-ghost capitalize"
            >
              {tab}
            </button>
          ))}
        </div>

        <span className="hairline mx-1 h-4 w-px flex-shrink-0" />

        <button
          type="button"
          onClick={() => useIDEStore.getState().setCommandPaletteOpen(true)}
          className="btn btn-ghost hidden gap-2 sm:inline-flex"
        >
          Search files
          <span className="gauge">⌘K</span>
        </button>

        <div className="ml-auto flex flex-shrink-0 items-center gap-1.5">
          {store.adbTarget && (
            <span className="gauge hidden md:inline" title="The adb target this session is attached to">
              {store.adbTarget}
            </span>
          )}
          <CheckpointMenu projectId={projectId} onRestored={handleFilesTouched} />
          <button
            type="button"
            onClick={() => (deviceLive ? void stopSession() : void startSession())}
            disabled={store.previewStatus === "connecting"}
            className={deviceLive ? "btn btn-stop" : "btn btn-go"}
          >
            {store.previewStatus === "connecting"
              ? "Starting a device"
              : deviceLive
                ? "Stop the device"
                : "Start a device"}
          </button>
        </div>
      </div>

      <div className="flex min-h-0 flex-1 overflow-hidden">
        {/* ── Left: the agent, or the files ── */}
        <ResizablePanel
          direction="horizontal"
          defaultSize={340}
          minSize={280}
          maxSize={560}
          className="hidden md:block"
          label="agent panel"
        >
          <div className="flex h-full flex-col overflow-hidden border-r border-rail bg-ink">
            {/* Both panels stay mounted: switching to the tree mid-build must
                not tear down the stream that is writing the files. */}
            <div className={leftTab === "chat" ? "flex min-h-0 flex-1" : "hidden"}>
              <div className="min-h-0 flex-1">
                <AIChat
                  ref={aiChatRef}
                  projectId={projectId}
                  onFilesTouched={handleFilesTouched}
                  onPlan={handlePlan}
                  onApprovePlan={approvePlan}
                />
              </div>
            </div>

            <div className={leftTab === "files" ? "flex min-h-0 flex-1 flex-col" : "hidden"}>
              <div className="bay-head justify-between">
                <span className="gauge">Workspace</span>
                <span className="gauge tnum">{countFiles(store.files)}</span>
              </div>
              <div className="min-h-0 flex-1 overflow-y-auto py-1">
                {store.files.length === 0 ? (
                  <div className="px-4 py-6">
                    <p className="text-small text-paper">Nothing here yet.</p>
                    <p className="mt-1 text-tiny text-graphite">
                      Describe what you want in the chat and the files will appear
                      as they are written.
                    </p>
                    <button
                      type="button"
                      onClick={() => setLeftTab("chat")}
                      className="btn mt-3"
                    >
                      Open the chat
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
          </div>
        </ResizablePanel>

        {/* ── Centre: the code, or the plan ── */}
        <div className="flex min-h-0 min-w-0 flex-1 flex-col overflow-hidden">
          <div className="min-h-0 flex-1 overflow-hidden bg-ink">
            {store.currentPlan ? (
              <PlanViewer
                message={store.currentPlan.message}
                plan={store.currentPlan.plan}
                onApprove={() => approvePlan(store.currentPlan!.plan)}
                onDismiss={() => store.setCurrentPlan(null)}
              />
            ) : store.selectedFile && selectedContent !== undefined ? (
              <CodeEditor
                projectId={projectId}
                filePath={store.selectedFile}
                content={selectedContent}
              />
            ) : (
              <div className="flex h-full flex-col items-center justify-center px-8 text-center">
                <p className="text-lead text-paper">Nothing open.</p>
                <p className="mt-1.5 max-w-sm text-small text-graphite">
                  Pick a file from the workspace, or tell the agent what to build
                  and watch it get written here.
                </p>
                <p className="gauge mt-6">⌘K to search files</p>
              </div>
            )}
          </div>

          {/* ── Terminal ── */}
          <ResizablePanel
            direction="vertical"
            defaultSize={240}
            minSize={120}
            maxSize={520}
            handleSide="start"
            collapsed={!store.terminalOpen}
            collapsedSize={32}
            label="terminal"
          >
            <div className="flex h-full flex-col overflow-hidden border-t border-rail bg-slate">
              <button
                type="button"
                onClick={() => store.setTerminalOpen(!store.terminalOpen)}
                aria-expanded={store.terminalOpen}
                className="flex h-8 flex-shrink-0 items-center gap-2 px-3 text-left transition-colors hover:bg-riser"
              >
                <span className="lamp" data-state={deviceLive ? "live" : "idle"} />
                {/* The label names what the shell is attached to, not the word
                    "terminal" over a black box. */}
                <span className="gauge">
                  {store.adbTarget ? `shell · ${store.adbTarget}` : "shell · no device"}
                </span>
                <span className="gauge ml-auto">{store.terminalOpen ? "⌘J hide" : "⌘J show"}</span>
              </button>
              {store.terminalOpen && (
                <div className="min-h-0 flex-1 overflow-hidden">
                  <Terminal sessionId={store.sessionId} />
                </div>
              )}
            </div>
          </ResizablePanel>
        </div>

        {/* ── Right: the device ── */}
        <ResizablePanel
          direction="horizontal"
          defaultSize={300}
          minSize={220}
          maxSize={520}
          className="hidden lg:block"
          handleSide="start"
          label="device panel"
        >
          <div className="flex h-full flex-col overflow-hidden border-l border-rail bg-ink">
            <PhonePreview
              status={store.previewStatus}
              sessionId={store.sessionId}
              adbTarget={store.adbTarget}
              onStartSession={startSession}
            />
          </div>
        </ResizablePanel>
      </div>

      <CommandPalette
        open={store.commandPaletteOpen}
        onClose={() => store.setCommandPaletteOpen(false)}
        files={store.files}
        onOpenFile={store.selectFile}
        onSwitchToChat={() => setLeftTab("chat")}
        onSwitchToFiles={() => setLeftTab("files")}
        onToggleTerminal={() => store.setTerminalOpen(!store.terminalOpen)}
        onStartSession={startSession}
      />
    </div>
  );
}

function countFiles(nodes: { type: "file" | "folder"; children?: { type: "file" | "folder" }[] }[]): number {
  let n = 0;
  for (const node of nodes) {
    if (node.type === "file") n++;
    else if (node.children) n += countFiles(node.children as typeof nodes);
  }
  return n;
}
