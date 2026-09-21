"use client";

/**
 * Sidebar — the left edge of the project screen.
 *
 * Replaced the old icon-only view switcher. Files/Terminal/Memory/Triage/
 * Device all moved to the topbar's right-side dock (see Topbar.tsx) — this
 * rail's job now is project navigation: switch projects, start a new one,
 * reach account settings. Collapses to an icon-only strip; expanded, it
 * lists every project you own so switching never round-trips through the
 * dashboard.
 *
 * Filename kept as IconRail.tsx to avoid touching the one import site in
 * IDELayout.tsx; the exported component is what actually changed.
 */

import { useEffect, useState } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { conversations as conversationsApi, projects as projectsApi } from "@/lib/api";
import type { Conversation, Project } from "@/lib/types";
import CreateProject from "@/components/dashboard/CreateProject";

// Kept for the one call site in IDELayout.tsx that still switches the
// canvas's own content (chat is the only remaining "view" there).
export type RailView = "chat" | "files" | "memory" | "triage";

interface IconRailProps {
  projectId: number;
  projectName: string;
  /** The active project's current thread. undefined = its default thread. */
  conversationId?: number;
  /** Switch the active project's thread — a click under the expanded
      project, or the result of "+" starting a new one. */
  onSelectConversation?: (id: number | undefined) => void;
  /** Width in px while expanded — IDELayout owns this so its drag handle
      (at the rail's right edge) can resize it live. Ignored while collapsed. */
  width?: number;
  collapsed: boolean;
  onToggleCollapsed: () => void;
  /** True mid-drag — suppresses the width transition so the rail tracks the
      pointer instead of chasing it a beat behind. */
  isResizing?: boolean;
}

export default function IconRail({
  projectId,
  projectName,
  conversationId,
  onSelectConversation,
  width = 212,
  collapsed,
  onToggleCollapsed,
  isResizing = false,
}: IconRailProps) {
  const router = useRouter();
  const [list, setList] = useState<Project[] | null>(null);
  const [creating, setCreating] = useState(false);

  // Chat threads are only ever browsed for the project you're currently in —
  // switching to another project's threads means navigating there first, so
  // this state deliberately does not key by project id.
  const [expanded, setExpanded] = useState(false);
  const [chats, setChats] = useState<Conversation[] | null>(null);
  const loadChats = () => {
    if (chats !== null) return; // already loaded (or loading) this visit
    setChats([]); // placeholder so a second click doesn't refire the fetch
    void conversationsApi
      .list(projectId)
      .then(setChats)
      .catch(() => setChats(null));
  };
  // A project switch invalidates the cached thread list. Adjusted during
  // render (the React-recommended pattern for "reset state when a prop
  // changes") rather than in an effect, so it never cascades an extra pass.
  const [seenProjectId, setSeenProjectId] = useState(projectId);
  if (projectId !== seenProjectId) {
    setSeenProjectId(projectId);
    setChats(null);
    setExpanded(false);
  }

  const startNewChat = async () => {
    try {
      const c = await conversationsApi.create(projectId);
      setChats((prev) => (prev ? [c, ...prev] : [c]));
      setExpanded(true);
      onSelectConversation?.(c.id);
    } catch {
      // The composer still works against the default thread; the person can
      // just retry "new chat" a moment later.
    }
  };

  useEffect(() => {
    let cancelled = false;
    void (async () => {
      try {
        const rows = await projectsApi.list();
        if (!cancelled) setList(rows);
      } catch {
        // The rail still works without the list — it just shows the
        // current project until the request succeeds on a retry.
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [projectId]);

  return (
    <nav
      aria-label="Projects"
      style={{ width: collapsed ? 64 : width, transition: isResizing ? "none" : "width 200ms ease-out" }}
      className={`flex h-full flex-none flex-col gap-2.5 overflow-hidden border-r border-[var(--seam)] bg-panel-2 py-2.5 ${
        collapsed ? "items-center px-2" : "px-2.5"
      }`}
    >
      <div className={`flex items-center ${collapsed ? "flex-col gap-2" : "justify-between px-0.5"}`}>
        <Link href="/dashboard" className="flex items-center gap-2 text-[13px] font-semibold text-[var(--label)]">
          <span
            aria-hidden
            className="grid h-5 w-5 flex-none place-items-center rounded-[6px]"
            style={{ background: "linear-gradient(145deg, var(--color-acid-lime), #a85a41)" }}
          >
            <span className="h-1.5 w-1.5 rounded-full bg-[var(--label)]" />
          </span>
          {!collapsed && <span>Talon</span>}
        </Link>
        <button
          type="button"
          onClick={onToggleCollapsed}
          aria-label={collapsed ? "Expand sidebar" : "Collapse sidebar"}
          aria-pressed={collapsed}
          className="grid size-7 flex-none place-items-center rounded-lg text-[var(--label-3)] transition-colors duration-150 hover:bg-[var(--surface-2)] hover:text-[var(--label-2)]"
        >
          <svg
            aria-hidden
            width="14"
            height="14"
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            strokeWidth={2}
            strokeLinecap="round"
            strokeLinejoin="round"
            style={{ transform: collapsed ? "rotate(180deg)" : "none", transition: "transform 200ms ease" }}
          >
            <path d="M15 18l-6-6 6-6" />
          </svg>
        </button>
      </div>

      <button
        type="button"
        onClick={() => setCreating(true)}
        title="New project"
        className={`flex h-8 flex-none items-center gap-2 overflow-hidden rounded-[9px] bg-[var(--surface-2)] text-[13px] font-medium text-[var(--label)] transition-colors duration-150 hover:bg-[var(--surface-3)] ${
          collapsed ? "w-8 justify-center px-0" : "px-2.5"
        }`}
      >
        <svg
          aria-hidden
          width="14"
          height="14"
          viewBox="0 0 24 24"
          fill="none"
          stroke="var(--color-acid-lime)"
          strokeWidth={2}
          strokeLinecap="round"
          className="flex-none"
        >
          <path d="M12 5v14M5 12h14" />
        </svg>
        {!collapsed && <span>New project</span>}
      </button>

      {!collapsed && (
        <div className="flex min-h-0 flex-1 flex-col gap-0.5 overflow-y-auto">
          {(list ?? []).map((p) => {
            const active = p.id === projectId;
            return (
              <div key={p.id}>
                <div
                  className={`group flex h-8 items-center gap-1 overflow-hidden rounded-lg pr-1 pl-2 text-[13px] transition-colors duration-150 ${
                    active
                      ? "bg-[var(--accent-soft)] text-[var(--accent-2)]"
                      : "text-[var(--label-3)] hover:bg-[var(--surface-2)] hover:text-[var(--label-2)]"
                  }`}
                >
                  <button
                    type="button"
                    onClick={() => {
                      if (active) {
                        setExpanded((e) => {
                          const next = !e;
                          if (next) loadChats();
                          return next;
                        });
                      } else {
                        router.push(`/project/${p.id}`);
                      }
                    }}
                    aria-current={active ? "page" : undefined}
                    aria-expanded={active ? expanded : undefined}
                    className="flex min-w-0 flex-1 items-center gap-1.5 text-left"
                    title={p.name}
                  >
                    {active && (
                      <svg
                        aria-hidden
                        width="10"
                        height="10"
                        viewBox="0 0 24 24"
                        fill="none"
                        stroke="currentColor"
                        strokeWidth={2.5}
                        strokeLinecap="round"
                        strokeLinejoin="round"
                        className="flex-none"
                        style={{ transform: expanded ? "rotate(90deg)" : "none", transition: "transform 150ms ease" }}
                      >
                        <path d="M9 18l6-6-6-6" />
                      </svg>
                    )}
                    <span className="truncate">{p.name}</span>
                  </button>
                  {active && (
                    <button
                      type="button"
                      onClick={() => void startNewChat()}
                      title="New chat in this project"
                      aria-label="New chat in this project"
                      className="grid size-6 flex-none place-items-center rounded-md text-[var(--label-4)] opacity-0 transition-opacity duration-150 hover:bg-[var(--surface-3)] hover:text-[var(--label-2)] group-hover:opacity-100"
                    >
                      <svg aria-hidden width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2.2} strokeLinecap="round">
                        <path d="M12 5v14M5 12h14" />
                      </svg>
                    </button>
                  )}
                </div>

                {active && expanded && (
                  <div className="ml-4 flex flex-col gap-0.5 border-l border-[var(--seam)] pl-2">
                    {chats === null || chats.length === 0 ? (
                      <div className="px-2 py-1.5 text-[11.5px] text-[var(--label-4)]">
                        {chats === null ? "Loading chats…" : "No chats yet — start one"}
                      </div>
                    ) : (
                      chats.map((c) => {
                        const chatActive = (conversationId ?? 0) === c.id;
                        return (
                          <button
                            key={c.id}
                            type="button"
                            onClick={() => onSelectConversation?.(c.id)}
                            aria-current={chatActive ? "true" : undefined}
                            className={`flex h-7 items-center overflow-hidden rounded-md px-2 text-left text-[12px] transition-colors duration-150 ${
                              chatActive
                                ? "bg-[var(--surface-3)] text-[var(--label)]"
                                : "text-[var(--label-3)] hover:bg-[var(--surface-2)] hover:text-[var(--label-2)]"
                            }`}
                            title={c.title || "Untitled chat"}
                          >
                            <span className="truncate">{c.title || "Untitled chat"}</span>
                          </button>
                        );
                      })
                    )}
                  </div>
                )}
              </div>
            );
          })}
          {list === null && (
            <div className="px-2 py-2 text-xs text-[var(--label-4)]">Loading projects…</div>
          )}
        </div>
      )}
      {collapsed && <div className="flex-1" />}

      <Link
        href="/settings"
        className={`flex flex-none items-center gap-2 overflow-hidden rounded-lg py-1.5 transition-colors duration-150 hover:bg-[var(--surface-2)] ${
          collapsed ? "w-8 justify-center px-0" : "px-1.5"
        }`}
        title="Account settings"
      >
        <span className="grid size-6 flex-none place-items-center rounded-[7px] bg-[var(--surface-3)] text-[10.5px] font-bold text-[var(--label-2)]">
          {projectName.slice(0, 1).toUpperCase() || "T"}
        </span>
        {!collapsed && (
          <span className="min-w-0">
            <span className="block truncate text-[12.5px] font-medium text-[var(--label)]">Account</span>
            <span className="block truncate text-[11px] text-[var(--label-3)]">Settings</span>
          </span>
        )}
      </Link>

      <CreateProject open={creating} onClose={() => setCreating(false)} />
    </nav>
  );
}
