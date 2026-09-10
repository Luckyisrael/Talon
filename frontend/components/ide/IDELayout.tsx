"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { projects, sessions } from "@/lib/api";
import { useIDEStore } from "@/lib/store";
import type { PlanData } from "@/lib/types";
import ResizablePanel from "../ui/ResizablePanel";
import AIChat, { type AIChatHandle } from "./AIChat";
import type { PlanTask, TraceRow } from "@/lib/types";
import CheckpointMenu from "./CheckpointMenu";
import CodeEditor from "./CodeEditor";
import AgentWorkspace from "./AgentWorkspace";
import CommandPalette from "./CommandPalette";
import FileTree from "./FileTree";
import MemoryPanel from "./MemoryPanel";
import PhonePreview from "./PhonePreview";
import PlanViewer from "./PlanViewer";
import TriageForm from "./TriageForm";
import PushToGitHub from "./PushToGitHub";
import ExportPanel from "./ExportPanel";
import Terminal from "./Terminal";

interface IDELayoutProps {
  projectId: number;
  projectName: string;
  projectStatus?: string;
  /** Leave the IDE — the identity segment's way back to the project list. */
  onExit: () => void;
}

export default function IDELayout({
  projectId,
  projectName,
  projectStatus,
  onExit,
}: IDELayoutProps) {
  const store = useIDEStore();
  const aiChatRef = useRef<AIChatHandle>(null);
  // §7: the live turn's trace, lifted from AIChat so the centre pane's
  // AgentWorkspace renders the full-fidelity transcript alongside the
  // compact rail. Stable identity keeps AIChat's lift effect from
  // refiring every render.
  const [liveTrace, setLiveTrace] = useState<TraceRow[]>([]);
  const [liveTouchedFiles, setLiveTouchedFiles] = useState<string[]>([]);
  const [livePlanTasks, setLivePlanTasks] = useState<PlanTask[]>([]);
  const handleLiveTrace = useCallback(
    (rows: TraceRow[], touched: string[], tasks: PlanTask[]) => {
      setLiveTrace(rows);
      setLiveTouchedFiles(touched);
      setLivePlanTasks(tasks);
    },
    [],
  );
  const [leftTab, setLeftTab] = useState<"chat" | "files" | "memory" | "triage">("chat");

  // Below md the agent panel is a slide-over drawer rather than a fixed column.
  // isDesktop gates which one mounts, so there is only ever one AIChat instance
  // and a build stream is never duplicated or torn down by the switch.
  const isDesktop = useIsDesktop();
  const [mobilePanelOpen, setMobilePanelOpen] = useState(false);

  // On a phone, picking a file should reveal the editor behind the drawer.
  // Harmless on desktop, where the drawer state is never shown.
  const selectFile = useCallback((path: string) => {
    useIDEStore.getState().selectFile(path);
    setMobilePanelOpen(false);
  }, []);

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
  const approvePlan = useCallback(
    async (plan: PlanData) => {
      const state = useIDEStore.getState();
      state.setCurrentPlan(null);
      const screens = (plan.screens ?? []).map((s) => s.name).filter(Boolean);
      // §7 DSH plan flow: approval writes the plan to PLAN.md — the task
      // file. The build works from the durable artifact (reload, resume, and
      // the checklist all read the same record), not from a chat message's
      // short-lived wording. A failed write still builds: the plan is in the
      // conversation too; the file is the durable copy, not the only one.
      const lines = ["# Build plan", ""];
      if (screens.length > 0) {
        lines.push("## Screens");
        for (const s of plan.screens ?? []) {
          lines.push(`- ${s.name}${s.description ? ` — ${s.description}` : ""}`);
        }
        lines.push("");
      }
      if (plan.navigation) {
        lines.push("## Navigation", plan.navigation, "");
      }
      const features = plan.features ?? [];
      if (features.length > 0) {
        lines.push("## Features", ...features.map((f) => `- ${f}`), "");
      }
      const packages = plan.packages ?? [];
      if (packages.length > 0) {
        lines.push("## Packages", ...packages.map((p) => `- ${p}`), "");
      }
      try {
        await projects.writeFiles(projectId, [
          { path: "PLAN.md", content: lines.join("\n") + "\n" },
        ]);
        state.addFiles([{ path: "PLAN.md", content: lines.join("\n") + "\n" }]);
      } catch {
        // The plan still lives in the conversation; the build proceeds.
      }
      const list = screens.length > 0 ? screens.join(", ") : "the plan we just agreed on";
      aiChatRef.current?.switchToBuild();
      aiChatRef.current?.sendMessage(
        `Build ${list} per PLAN.md in the workspace. Implement every screen fully — no placeholders, no TODOs — and wire up the navigation from the plan. Mark each plan item done as you finish it.`,
        "build",
      );
    },
    [projectId],
  );

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

  // The chat/files/memory switch. It lives in the command bar at md+ and in the
  // drawer's own header below md, so it is defined once here.
  const tabSwitcher = (
    <div
      role="group"
      aria-label="What the panel shows"
      className="flex overflow-hidden rounded-md border border-graphite"
    >
      {(["chat", "files", "memory", "triage"] as const).map((tab) => (
        <button
          key={tab}
          type="button"
          onClick={() => setLeftTab(tab)}
          aria-pressed={leftTab === tab}
          className={`h-6 px-2.5 font-mono text-xs capitalize transition-colors ${
            leftTab === tab
              ? "bg-white/10 text-paper"
              : "text-ash hover:bg-white/5 hover:text-fog"
          }`}
        >
          {tab}
        </button>
      ))}
    </div>
  );

  // The panel body — one instance, rendered either inside the resizable column
  // (md+) or inside the slide-over drawer (below md). Keeping it a single
  // element means the AIChat inside it, and its build stream, is never
  // duplicated across the two mounts.
  const leftPanelContent = (
    <div className="flex h-full flex-col overflow-hidden rounded-xl border border-graphite bg-void">
      {/* On a phone the panel is a drawer with no command-bar tabs above it, so
          it carries its own switcher and a way out. Hidden at md+, where the
          tabs live in the command bar. */}
      <div className="flex flex-shrink-0 items-center justify-between gap-2 border-b border-graphite p-2 md:hidden">
        {tabSwitcher}
        <button
          type="button"
          onClick={() => setMobilePanelOpen(false)}
          className="btn btn-ghost"
          aria-label="Close the panel"
        >
          Close
        </button>
      </div>

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
            onLiveTrace={handleLiveTrace}
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
              <p className="text-sm text-mist">Nothing here yet.</p>
              <p className="mt-1 font-mono text-xs text-fog">
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
              onSelectFile={selectFile}
              onNewFile={handleNewFile}
              onNewFolder={handleNewFolder}
              onRename={handleRename}
              onDelete={handleDelete}
            />
          )}
        </div>
      </div>

      <div className={leftTab === "memory" ? "flex min-h-0 flex-1 flex-col" : "hidden"}>
        <MemoryPanel projectId={projectId} active={leftTab === "memory"} />
      </div>

      <div className={leftTab === "triage" ? "flex min-h-0 flex-1 flex-col overflow-y-auto" : "hidden"}>
        <TriageForm projectId={projectId} />
      </div>
    </div>
  );

  return (
    <div className="flex h-full min-h-0 flex-col gap-2 overflow-hidden bg-void p-2">
      {/* ── Command bar ── One inset module: whose project this is on the left,
          the workspace controls in the middle, the device on the right. It used
          to be two stacked full-width rails; merging them gave the panels their
          height back. */}
      <div className="flex h-11 flex-shrink-0 items-center gap-2 rounded-xl border border-graphite bg-carbon px-2">
        {/* Identity */}
        <button
          type="button"
          onClick={onExit}
          className="btn btn-ghost gap-1.5"
          aria-label="Back to your projects"
        >
          <span aria-hidden>←</span>
          <span className="hidden sm:inline">Projects</span>
        </button>

        <span className="hairline h-4 w-px flex-shrink-0" />

        <div className="flex min-w-0 items-center gap-2">
          <span className="min-w-0 truncate text-sm text-mist" title={projectName}>
            {projectName}
          </span>
          {projectStatus && (
            <span className="gauge hidden flex-shrink-0 sm:inline">{projectStatus}</span>
          )}
        </div>

        {/* Workspace */}
        <span className="hairline mx-1 hidden h-4 w-px flex-shrink-0 md:block" />

        {/* Below md the panel is a slide-over drawer; this opens it. At md+ the
            same chat/files/memory switch sits here inline instead. */}
        <button
          type="button"
          onClick={() => setMobilePanelOpen(true)}
          className="btn btn-ghost md:hidden"
          aria-label="Open the agent panel"
        >
          <svg
            aria-hidden
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            strokeWidth={1.75}
            className="h-4 w-4"
          >
            <rect x="3" y="4" width="18" height="16" rx="2" />
            <path d="M9 4v16" />
          </svg>
        </button>
        <div className="hidden md:block">{tabSwitcher}</div>

        <button
          type="button"
          onClick={() => useIDEStore.getState().setCommandPaletteOpen(true)}
          className="btn btn-ghost hidden gap-2 lg:inline-flex"
        >
          Search files
          <span className="gauge">⌘K</span>
        </button>

        {/* Device */}
        <div className="ml-auto flex flex-shrink-0 items-center gap-1.5">
          {store.adbTarget && (
            <span className="gauge hidden md:inline" title="The adb target this session is attached to">
              {store.adbTarget}
            </span>
          )}
          <CheckpointMenu projectId={projectId} onRestored={handleFilesTouched} />
          <PushToGitHub projectId={projectId} />
          <ExportPanel projectId={projectId} />
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

      <div className="flex min-h-0 flex-1 gap-2 overflow-hidden">
        {/* ── Left: the agent, or the files ── On a wide screen it is a
            resizable column; on a phone it slides in over the centre. One
            AIChat instance either way (the ternary renders exactly one branch),
            so a build stream is never duplicated or torn down by the switch.
            Below md the drawer stays mounted while closed — just translated
            off-screen — so a build keeps streaming behind it. */}
        {isDesktop ? (
          <ResizablePanel
            direction="horizontal"
            defaultSize={340}
            minSize={280}
            maxSize={560}
            label="agent panel"
          >
            {leftPanelContent}
          </ResizablePanel>
        ) : (
          <>
            <div
              aria-hidden
              onClick={() => setMobilePanelOpen(false)}
              className={`fixed inset-0 z-30 bg-void/80 transition-opacity duration-200 ${
                mobilePanelOpen ? "opacity-100" : "pointer-events-none opacity-0"
              }`}
            />
            <div
              role="dialog"
              aria-modal="true"
              aria-label="Agent panel"
              className={`fixed inset-y-0 left-0 z-40 flex w-[88%] max-w-sm flex-col p-2 transition-transform duration-200 ${
                mobilePanelOpen ? "translate-x-0" : "-translate-x-full"
              }`}
            >
              {leftPanelContent}
            </div>
          </>
        )}

        {/* ── Centre: the code, or the plan ── */}
        <div className="flex min-h-0 min-w-0 flex-1 flex-col overflow-hidden rounded-xl border border-graphite bg-void">
          <div className="min-h-0 flex-1 overflow-hidden bg-void">
            {store.currentPlan ? (
              <PlanViewer
                message={store.currentPlan.message}
                plan={store.currentPlan.plan}
                onApprove={() => approvePlan(store.currentPlan!.plan)}
                onDismiss={() => store.setCurrentPlan(null)}
              />
            ) : (
              <AgentWorkspace
                projectId={projectId}
                traceRows={liveTrace}
                planTasks={livePlanTasks}
                selectedFile={store.selectedFile}
                fileContent={selectedContent}
                filesTouched={liveTouchedFiles}
              />
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
            <div className="flex h-full flex-col overflow-hidden border-t border-graphite bg-carbon">
              <button
                type="button"
                onClick={() => store.setTerminalOpen(!store.terminalOpen)}
                aria-expanded={store.terminalOpen}
                className="flex h-8 flex-shrink-0 items-center gap-2 px-3 text-left transition-colors hover:bg-white/5"
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
          <div className="flex h-full flex-col overflow-hidden rounded-xl border border-graphite bg-void">
            <PhonePreview
              status={store.previewStatus}
              sessionId={store.sessionId}
              projectId={projectId}
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

// Tracks the md breakpoint (768px, Tailwind's md) so the layout can *mount* the
// agent panel two different ways — a resizable column above it, a slide-over
// drawer below it — rather than restyle one element. ResizablePanel forces its
// width with an inline style, so CSS alone can't turn it into a drawer.
// Defaults to true so the server render and first client paint agree on the
// desktop markup (no hydration mismatch); the effect corrects it on a phone
// before anything interactive happens.
function useIsDesktop(): boolean {
  const [isDesktop, setIsDesktop] = useState(true);
  useEffect(() => {
    const mq = window.matchMedia("(min-width: 768px)");
    const sync = () => setIsDesktop(mq.matches);
    sync();
    mq.addEventListener("change", sync);
    return () => mq.removeEventListener("change", sync);
  }, []);
  return isDesktop;
}
