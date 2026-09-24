"use client";

import { Fragment, useCallback, useEffect, useRef, useState } from "react";
import { conversations, git, projects, sessions, verifyBadge, verifyState } from "@/lib/api";
import { useIDEStore } from "@/lib/store";
import type { PlanData } from "@/lib/types";
import AIChat, { type AIChatHandle } from "./AIChat";
import type { PlanTask, TraceRow } from "@/lib/types";
import ChangedFiles from "./ChangedFiles";
import CodeEditor from "./CodeEditor";
import CommandPalette from "./CommandPalette";
import IconRail from "./IconRail";
import Markdown from "@/components/ui/Markdown";
import MemoryPanel from "./MemoryPanel";
import PhonePreview from "./PhonePreview";
import TestOnPhone from "./TestOnPhone";
import PlanViewer from "./PlanViewer";
import Topbar from "./Topbar";
import TriageForm from "./TriageForm";
import Terminal from "./Terminal";

interface IDELayoutProps {
  projectId: number;
  projectName: string;
  projectStatus?: string;
  /** Leave the IDE — the identity segment's way back to the project list. */
  onExit: () => void;
}

/** The five things that can dock on the right, opened from the topbar. */
export type DockPanel = "files" | "terminal" | "memory" | "triage" | "device";

const DOCK_TITLES: Record<DockPanel, string> = {
  files: "Changed files",
  terminal: "Terminal",
  memory: "Memory",
  triage: "Triage",
  device: "Device preview",
};

export default function IDELayout({
  projectId,
  projectName,
  projectStatus,
  onExit,
}: IDELayoutProps) {
  const store = useIDEStore();
  const aiChatRef = useRef<AIChatHandle>(null);
  // Which chat thread within this project is open. undefined = the
  // project's default thread — every project that predates multi-chat, or
  // one nobody has started a second chat in yet.
  const [conversationId, setConversationId] = useState<number | undefined>(undefined);
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
  const [gitMeta, setGitMeta] = useState<null | { branch: string; dirty: number; ahead: number; behind: number; last_commit: string }>(null);
  // ── Topbar badges: git branch/dirty, and "Build verified · ago" (row 45) ──
  // Both re-read on project change and whenever a turn writes files (the
  // verify ladder runs at turn end — the only moment the verdict can change).
  // The setState calls live in the promise callbacks, not the effect body
  // (the shape react-hooks/set-state-in-effect accepts), and each fetch is
  // cancelled on re-run so a late reply from the previous project never
  // flashes its badges onto the new one's topbar. The verify result is also
  // keyed by project: only a PASSING sweep (verified | best-effort) claims
  // the badge; refused, never-verified (404 → null), and transient failures
  // all claim nothing.
  const [verifiedBadge, setVerifiedBadge] = useState<{
    projectId: number;
    ago: string | null;
  } | null>(null);
  useEffect(() => {
    let cancelled = false;
    git.getProjectStatus(projectId)
      .then((g) => {
        if (!cancelled) setGitMeta(g);
      })
      .catch(() => {
        // No git yet or workspace missing — badge stays hidden.
      });
    verifyState
      .get(projectId)
      .then((state) => {
        if (!cancelled) {
          setVerifiedBadge({ projectId, ago: verifyBadge(state) });
        }
      })
      .catch(() => {
        // A transient verify-state failure claims nothing — the badge hides
        // rather than showing a verdict it cannot confirm.
        if (!cancelled) setVerifiedBadge(null);
      });
    return () => {
      cancelled = true;
    };
  }, [projectId, liveTouchedFiles]);

  // ── Dock: Files/Terminal/Memory/Triage/Device, opened from the topbar.
  // Device starts open — it's the product's core feature, not an
  // opt-in extra. Two or more open at once can tile stacked or side by
  // side (dockSplit), the same pattern as Claude Desktop's multi-panel view.
  const [openPanels, setOpenPanels] = useState<Record<DockPanel, boolean>>({
    files: false,
    terminal: false,
    memory: false,
    triage: false,
    device: true,
  });
  const [dockSplit, setDockSplit] = useState<"col" | "row">("col");
  // Explicit pixel sizes for open dock panels, set only once someone drags a
  // split handle — until then every panel is an even 1fr and this stays
  // empty. Cleared whenever the open set or the split axis changes, since a
  // size measured under the old layout means nothing under the new one.
  const [panelFlex, setPanelFlex] = useState<Partial<Record<DockPanel, number>>>({});
  const sectionRefs = useRef<Partial<Record<DockPanel, HTMLElement | null>>>({});
  const togglePanel = useCallback((panel: DockPanel) => {
    setOpenPanels((prev) => ({ ...prev, [panel]: !prev[panel] }));
    setPanelFlex({});
  }, []);
  const openCount = Object.values(openPanels).filter(Boolean).length;
  const [deviceTab, setDeviceTab] = useState<"device" | "app">("device");

  const isDesktop = useIsDesktop();
  const [mobilePanelOpen, setMobilePanelOpen] = useState(false);

  const [dockWidth, setDockWidth] = useState(392);
  const [isDragging, setIsDragging] = useState(false);
  const startXDock = useRef(0);
  const startWidthDock = useRef(0);

  // ── Rail resize: drag the handle at its right edge. Dragging it thin
  // auto-collapses (matching a click on the rail's own toggle); dragging a
  // collapsed rail back out past the threshold re-expands it — the same
  // thresholds the design sample established.
  const [railWidth, setRailWidth] = useState(212);
  const [railCollapsed, setRailCollapsed] = useState(false);
  const [isDraggingRail, setIsDraggingRail] = useState(false);
  const startXRail = useRef(0);
  const startWidthRail = useRef(0);
  const draggingRailRef = useRef(false);

  // ── Dock split-drag: the handle between two open panels, dragged along
  // whichever axis dockSplit runs. Sizes are read off the live DOM at
  // pointerdown (not stored state), so the very first drag of a session
  // starts from each panel's real on-screen size instead of an invented one.
  const splitDrag = useRef<{
    a: DockPanel;
    b: DockPanel;
    axis: "row" | "col";
    startPos: number;
    startA: number;
    total: number;
  } | null>(null);
  const [isDraggingSplit, setIsDraggingSplit] = useState(false);

  useEffect(() => {
    function onPointerMove(e: PointerEvent) {
      if (startWidthDock.current > 0) {
        let newWidth = startWidthDock.current - (e.clientX - startXDock.current);
        if (newWidth < 280) newWidth = 280;
        if (newWidth > window.innerWidth * 0.7) newWidth = window.innerWidth * 0.7;
        setDockWidth(newWidth);
      }
      if (draggingRailRef.current) {
        const newWidth = startWidthRail.current + (e.clientX - startXRail.current);
        setRailCollapsed(newWidth <= 50);
        setRailWidth(Math.max(64, Math.min(480, newWidth)));
      }
      const split = splitDrag.current;
      if (split) {
        const pos = split.axis === "row" ? e.clientX : e.clientY;
        const delta = pos - split.startPos;
        const minSize = 80;
        const newA = Math.max(minSize, Math.min(split.total - minSize, split.startA + delta));
        setPanelFlex((prev) => ({ ...prev, [split.a]: newA, [split.b]: split.total - newA }));
      }
    }
    function onPointerUp() {
      if (startWidthDock.current > 0) {
        startWidthDock.current = 0;
        setIsDragging(false);
        document.body.style.cursor = "";
      }
      if (draggingRailRef.current) {
        draggingRailRef.current = false;
        setIsDraggingRail(false);
        document.body.style.cursor = "";
      }
      if (splitDrag.current) {
        splitDrag.current = null;
        setIsDraggingSplit(false);
        document.body.style.cursor = "";
      }
    }
    window.addEventListener("pointermove", onPointerMove);
    window.addEventListener("pointerup", onPointerUp);
    return () => {
      window.removeEventListener("pointermove", onPointerMove);
      window.removeEventListener("pointerup", onPointerUp);
    };
  }, []);

  const startSplitDrag = useCallback(
    (a: DockPanel, b: DockPanel, axis: "row" | "col", clientPos: number) => {
      const elA = sectionRefs.current[a];
      const elB = sectionRefs.current[b];
      if (!elA || !elB) return;
      const rectA = elA.getBoundingClientRect();
      const rectB = elB.getBoundingClientRect();
      const sizeA = axis === "row" ? rectA.width : rectA.height;
      const sizeB = axis === "row" ? rectB.width : rectB.height;
      splitDrag.current = { a, b, axis, startPos: clientPos, startA: sizeA, total: sizeA + sizeB };
      setIsDraggingSplit(true);
      document.body.style.cursor = axis === "row" ? "col-resize" : "row-resize";
    },
    [],
  );
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

  // Switching projects (a keyed route remount doesn't happen here — the
  // sidebar navigates via router.push, not a prop change) resets to that
  // project's default thread rather than carrying over a stale id. Adjusted
  // during render, not in an effect, per the same pattern as IconRail.
  const [seenProjectId, setSeenProjectId] = useState(projectId);
  if (projectId !== seenProjectId) {
    setSeenProjectId(projectId);
    setConversationId(undefined);
  }

  const handleFilesTouched = useCallback(
    async (paths: string[]) => {
      if (paths.length === 0) return;
      // Feed the dock panel (and the git/verify refreshes keyed off this
      // state): the live done frame and the restore seed both arrive here,
      // and pre-fix only live trace events ever wrote it — a reloaded turn's
      // panel kept rendering every diff file instead of the turn's set
      // (caught by F9 and the row-45 live browser E2E).
      setLiveTouchedFiles(paths);
      await refetchFiles();
      const state = useIDEStore.getState();
      if (!state.selectedFile && paths.length > 0) {
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

  useEffect(() => {
    let cancelled = false;
    void (async () => {
      try {
        const live = await sessions.liveFor(projectId);
        if (cancelled || !live) return;
        const state = useIDEStore.getState();
        state.setSessionId(live.id);
        state.setAdbTarget(live.adb_port ? `localhost:${live.adb_port}` : null);
        state.setPreviewStatus("streaming");
        if (live.state === "paused") {
          try {
            await sessions.resume(live.id);
          } catch {
            // The container may have been reaped between the row read and
            // the resume — the stream error state will say so honestly.
          }
        }
      } catch {
        // Unreachable live-session is not fatal: the panel simply starts
        // in its honest idle, the same state as a fresh project.
      }
    })();
    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
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

  const approvePlan = useCallback(
    async (plan: PlanData) => {
      const state = useIDEStore.getState();
      state.setCurrentPlan(null);
      const screens = (plan.screens ?? []).map((s) => s.name).filter(Boolean);
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
        togglePanel("terminal");
      }
    }
    document.addEventListener("keydown", onKeyDown);
    return () => document.removeEventListener("keydown", onKeyDown);
  }, [togglePanel]);

  const selectedContent = store.selectedFile ? store.fileContents[store.selectedFile] : undefined;
  const deviceLive = store.previewStatus === "streaming";

  // ── Canvas: chat, or a selected file taking its place. ──
  const canvasContent = (
    <div className="flex h-full min-h-0 flex-col overflow-hidden">
      <div className="surface-2 flex flex-shrink-0 items-center justify-between gap-2 p-2 md:hidden">
        <button type="button" onClick={() => setMobilePanelOpen(false)} className="btn btn-ghost" aria-label="Close the panel">
          Close
        </button>
      </div>

      {store.currentPlan ? (
        <div className="surface-2 flex-shrink-0 overflow-y-auto p-3 shadow-[0_1px_0_var(--seam)]">
          <PlanViewer
            message={store.currentPlan.message}
            plan={store.currentPlan.plan}
            onApprove={() => approvePlan(store.currentPlan!.plan)}
            onDismiss={() => store.setCurrentPlan(null)}
          />
        </div>
      ) : null}

      <div className={store.selectedFile ? "hidden" : "flex min-h-0 flex-1"}>
        <div className="min-h-0 flex-1">
          <AIChat
            ref={aiChatRef}
            projectId={projectId}
            conversationId={conversationId}
            onFilesTouched={handleFilesTouched}
            onPlan={handlePlan}
            onApprovePlan={approvePlan}
            onLiveTrace={handleLiveTrace}
            onNewThread={() => {
              conversations
                .create(projectId)
                .then((c) => setConversationId(c.id))
                .catch(() => {});
            }}
          />
        </div>
      </div>

      {store.selectedFile ? (
        <div className="flex min-h-0 flex-1 flex-col">
          <div className="bay-head justify-between">
            <button type="button" onClick={() => store.selectFile(null)} className="btn btn-ghost h-7 px-2 font-mono text-xs">
              ← Back to chat
            </button>
            <span className="gauge truncate" title={store.selectedFile}>
              {store.selectedFile.split("/").pop()}
            </span>
          </div>
          <div className="min-h-0 flex-1 overflow-hidden">
            {selectedContent !== undefined && !store.selectedFile.endsWith(".md") ? (
              <CodeEditor projectId={projectId} filePath={store.selectedFile} content={selectedContent} />
            ) : (
              <div className="h-full overflow-y-auto px-6 py-6">
                <div className="mx-auto max-w-2xl">
                  <Markdown>{selectedContent ?? ""}</Markdown>
                </div>
              </div>
            )}
          </div>
        </div>
      ) : null}
    </div>
  );

  const dockPaneBody = (panel: DockPanel) => {
    switch (panel) {
      case "files":
        return <ChangedFiles projectId={projectId} filesTouched={liveTouchedFiles} />;
      case "terminal":
        return <Terminal sessionId={store.sessionId} />;
      case "memory":
        return <MemoryPanel projectId={projectId} active={openPanels.memory} />;
      case "triage":
        return <TriageForm projectId={projectId} />;
      case "device":
        return (
          <div className="flex h-full min-h-0 flex-col">
            <div className="flex flex-shrink-0 gap-1 rounded-[10px] bg-panel-3 p-[3px]">
              {(["device", "app"] as const).map((tab) => (
                <button
                  key={tab}
                  type="button"
                  role="tab"
                  aria-selected={deviceTab === tab}
                  onClick={() => setDeviceTab(tab)}
                  className={`h-7 flex-1 rounded-[7px] text-xs font-semibold transition-colors duration-150 ease-out ${
                    deviceTab === tab ? "bg-smoke text-paper" : "text-ash hover:text-mist"
                  }`}
                >
                  {tab === "device" ? "Device" : "Get the app"}
                </button>
              ))}
            </div>
            <div className="mt-2 min-h-0 flex-1">
              {deviceTab === "device" ? (
                <PhonePreview
                  status={store.previewStatus}
                  sessionId={store.sessionId}
                  projectId={projectId}
                  adbTarget={store.adbTarget}
                  onStartSession={startSession}
                />
              ) : (
                <div className="h-full overflow-y-auto overscroll-contain">
                  <TestOnPhone projectId={projectId} epoch={store.sessionId ?? 0} />
                </div>
              )}
            </div>
          </div>
        );
    }
  };

  const dockOrder: DockPanel[] = ["device", "files", "terminal", "memory", "triage"];
  const openOrder = dockOrder.filter((p) => openPanels[p]);

  return (
    <div
      className="device-grid grid h-full min-h-0 overflow-hidden bg-[var(--bg)]"
      style={{
        // The dock spans both rows — it starts flush with the top of the
        // window, not below the topbar. The topbar itself only stretches
        // over the rail and canvas columns, matching the design sample.
        gridTemplateRows: "48px 1fr",
        gridTemplateColumns: "auto 1fr auto",
        gridTemplateAreas: `"top top dock" "rail canvas dock"`,
      }}
    >
      <div style={{ gridArea: "top" }}>
        <Topbar
          projectName={projectName}
          projectStatus={projectStatus}
          verifiedAgo={verifiedBadge?.projectId === projectId ? verifiedBadge.ago : null}
          gitBranch={gitMeta?.branch ?? null}
          gitDirty={gitMeta?.dirty ?? null}
          gitAheadBehind={
            gitMeta ? (gitMeta.ahead || gitMeta.behind ? `${gitMeta.ahead}↑ ${gitMeta.behind}↓` : null) : null
          }
          onExit={onExit}
          onOpenPalette={() => store.setCommandPaletteOpen(true)}
          openPanels={openPanels}
          onTogglePanel={togglePanel}
          changedCount={liveTouchedFiles.length}
          dockSplit={dockSplit}
          onToggleSplit={() => {
            setDockSplit((d) => (d === "col" ? "row" : "col"));
            setPanelFlex({});
          }}
        />
      </div>

      <div style={{ gridArea: "rail" }} className="relative min-h-0">
        <IconRail
          projectId={projectId}
          projectName={projectName}
          conversationId={conversationId}
          onSelectConversation={setConversationId}
          width={railWidth}
          collapsed={railCollapsed}
          onToggleCollapsed={() => {
            setRailCollapsed((c) => !c);
            setRailWidth((w) => (w <= 64 ? 212 : w));
          }}
          isResizing={isDraggingRail}
        />
        <div
          role="separator"
          aria-orientation="vertical"
          aria-label="Resize sidebar"
          className="absolute inset-y-0 -right-0.5 z-30 w-2 cursor-col-resize opacity-0 transition-opacity hover:bg-[var(--accent)] hover:opacity-50"
          onPointerDown={(e) => {
            draggingRailRef.current = true;
            setIsDraggingRail(true);
            startXRail.current = e.clientX;
            // Dragging out from collapsed starts from its real 64px edge,
            // not the last expanded width — otherwise the first pixel of
            // drag would snap the rail wide open.
            startWidthRail.current = railCollapsed ? 64 : railWidth;
            document.body.style.cursor = "col-resize";
            e.preventDefault();
          }}
        />
      </div>

      {isDesktop ? (
        <main style={{ gridArea: "canvas" }} className="canvas-wrap min-h-0">
          {canvasContent}
        </main>
      ) : (
        <>
          <div
            aria-hidden
            onClick={() => setMobilePanelOpen(false)}
            className={`fixed inset-0 z-30 bg-[var(--bg)]/80 transition-opacity duration-200 ${
              mobilePanelOpen ? "opacity-100" : "pointer-events-none opacity-0"
            }`}
          />
          <div
            role="dialog"
            aria-modal="true"
            aria-label="Agent panel"
            className={`fixed inset-y-0 left-0 z-40 flex w-[88%] max-w-sm flex-col transition-transform duration-200 ${
              mobilePanelOpen ? "translate-x-0" : "-translate-x-full"
            }`}
          >
            <div className="canvas-wrap h-full">{canvasContent}</div>
          </div>
        </>
      )}

      {/* ── Dock: every open panel tiles inside one column, stacked or
          side by side (dockSplit). Closed entirely, it takes no width —
          nothing false-advertises as "always there" the way the old fixed
          device column did. */}
      {openCount > 0 && isDesktop && (
        <div
          style={{ gridArea: "dock", justifySelf: "start", marginLeft: "-4px" }}
          className="relative z-30 w-2 cursor-col-resize hover:bg-[var(--accent)] opacity-0 hover:opacity-50 transition-opacity"
          onPointerDown={(e) => {
            setIsDragging(true);
            startXDock.current = e.clientX;
            startWidthDock.current = dockWidth;
            document.body.style.cursor = "col-resize";
            e.preventDefault();
          }}
        />
      )}
      <aside
        style={{ 
          gridArea: "dock",
          width: openCount > 0 ? dockWidth + "px" : 0,
          transition: isDragging ? "none" : "width 300ms cubic-bezier(0.34, 1.56, 0.64, 1)"
        }}
        className={`bg-panel-2 relative z-20 flex min-h-0 flex-col overflow-hidden shadow-[1px_0_0_var(--seam)]`}
      >
        {openCount > 0 ? (
          <div className={`flex min-h-0 flex-1 ${dockSplit === "row" ? "flex-row" : "flex-col"}`}>
            {openOrder.map((panel, i) => (
              <Fragment key={panel}>
                <section
                  ref={(el) => {
                    sectionRefs.current[panel] = el;
                  }}
                  style={{
                    flex: panelFlex[panel] != null ? `${panelFlex[panel]} 0 0px` : "1 1 0px",
                    transition: isDraggingSplit ? "none" : undefined,
                  }}
                  className={`flex min-h-0 min-w-0 flex-col ${
                    dockSplit === "row" ? "border-l border-[var(--seam)] first:border-l-0" : "border-t border-[var(--seam)] first:border-t-0"
                  }`}
                >
                  <header className="flex h-9 flex-shrink-0 items-center gap-2 px-3">
                    <span className="gauge">{DOCK_TITLES[panel]}</span>
                    {panel === "device" && deviceLive ? <span className="lamp" data-state="live" /> : null}
                    <span className="flex-1" />
                    <button
                      type="button"
                      onClick={() => togglePanel(panel)}
                      aria-label={`Close ${DOCK_TITLES[panel]}`}
                      className="grid size-6 place-items-center rounded-md text-[var(--label-4)] transition-colors duration-150 hover:bg-[var(--surface-2)] hover:text-[var(--label-2)]"
                    >
                      <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2} strokeLinecap="round">
                        <path d="M18 6 6 18M6 6l12 12" />
                      </svg>
                    </button>
                  </header>
                  <div className="min-h-0 flex-1 overflow-y-auto overscroll-contain px-3 pb-3">{dockPaneBody(panel)}</div>
                </section>
                {i < openOrder.length - 1 && (
                  <div
                    role="separator"
                    aria-orientation={dockSplit === "row" ? "vertical" : "horizontal"}
                    aria-label={`Resize between ${DOCK_TITLES[panel]} and ${DOCK_TITLES[openOrder[i + 1]]}`}
                    className={`z-10 flex-none opacity-0 transition-opacity hover:bg-[var(--accent)] hover:opacity-50 ${
                      dockSplit === "row" ? "-mx-[3px] w-[7px] cursor-col-resize" : "-my-[3px] h-[7px] cursor-row-resize"
                    }`}
                    onPointerDown={(e) => {
                      e.preventDefault();
                      startSplitDrag(panel, openOrder[i + 1], dockSplit, dockSplit === "row" ? e.clientX : e.clientY);
                    }}
                  />
                )}
              </Fragment>
            ))}
          </div>
        ) : null}
      </aside>

      <CommandPalette
        open={store.commandPaletteOpen}
        onClose={() => store.setCommandPaletteOpen(false)}
        files={store.files}
        onOpenFile={store.selectFile}
        onSwitchToChat={() => store.selectFile(null)}
        onSwitchToFiles={() => setOpenPanels((p) => ({ ...p, files: true }))}
        onToggleTerminal={() => togglePanel("terminal")}
        onStartSession={startSession}
      />
    </div>
  );
}

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
