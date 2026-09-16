"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { memory as memoryApi } from "@/lib/api";
import type { ArchitectureDecision, ProjectMemory } from "@/lib/types";

/**
 * "What the agent knows" — a read-only window onto the durable memory the agent
 * curates for a project (the write_memory tool) plus the architecture decisions
 * it has accepted. It answers the recurring "I thought we gave it a memory?"
 * question by simply showing that memory.
 *
 * Read-only on purpose: this is the agent's notebook, not yours. Editing it here
 * would let the UI claim the agent believes something it was never told, so the
 * panel only ever reflects what is stored.
 *
 * Fetches when the tab becomes visible so it is fresh each time you look, with a
 * manual refresh for mid-build checks. The agent writes memory as it works; a
 * panel that fetched once on mount would quietly go stale.
 */

// The five categories the write_memory tool allows. Everything is the agent's
// knowledge, so entries wear neutral mist — except warnings, which the agent
// flagged and keep fog emphasis. Unknown types fall back to ash rather than
// inventing a meaning.
const TYPE_TONE: Record<string, string> = {
  convention: "text-mist",
  decision: "text-mist",
  architecture: "text-mist",
  fact: "text-mist",
  warning: "text-fog",
};

function toneFor(entryType: string): string {
  return TYPE_TONE[entryType.toLowerCase()] ?? "text-ash";
}

export default function MemoryPanel({
  projectId,
  active,
}: {
  projectId: number;
  active: boolean;
}) {
  const [memories, setMemories] = useState<ProjectMemory[]>([]);
  const [decisions, setDecisions] = useState<ArchitectureDecision[]>([]);
  const [summary, setSummary] = useState("");
  const [status, setStatus] = useState<"idle" | "loading" | "loaded" | "error">(
    "idle",
  );
  // A load in flight when the panel is hidden or the project changes must not
  // write its result over newer state.
  const reqRef = useRef(0);

  const load = useCallback(async () => {
    const seq = ++reqRef.current;
    setStatus("loading");
    try {
      const snapshot = await memoryApi.get(projectId);
      if (seq !== reqRef.current) return;
      setMemories(snapshot.memories);
      setDecisions(snapshot.decisions);
      setSummary(snapshot.summary);
      setStatus("loaded");
    } catch {
      if (seq !== reqRef.current) return;
      setStatus("error");
    }
  }, [projectId]);

  // Refetch whenever the tab opens or the project changes; ignore while hidden.
  // Deferred a macrotask so the fetch (which setStates synchronously) doesn't
  // cascade-render inside the effect commit.
  useEffect(() => {
    if (!active) return;
    const t = window.setTimeout(() => void load(), 0);
    return () => window.clearTimeout(t);
  }, [active, load]);

  const total = memories.length + decisions.length;

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <div className="bay-head justify-between">
        <span className="gauge">What the agent knows</span>
        <div className="flex items-center gap-2">
          {status === "loaded" && <span className="gauge tnum">{total}</span>}
          <button
            type="button"
            onClick={() => void load()}
            disabled={status === "loading"}
            className="gauge text-ash transition-colors hover:text-fog disabled:opacity-50"
            aria-label="Refresh what the agent knows"
          >
            {status === "loading" ? "…" : "↻"}
          </button>
        </div>
      </div>

      <div className="min-h-0 flex-1 overflow-y-auto px-3 py-3">
        {status === "error" ? (
          <div className="px-1 py-4">
            <p className="text-sm text-coral-red">Couldn&apos;t load the agent&apos;s memory.</p>
            <button type="button" onClick={() => void load()} className="btn mt-3">
              Try again
            </button>
          </div>
        ) : status !== "loaded" ? (
          <p className="gauge px-1">loading…</p>
        ) : total === 0 && summary === "" ? (
          <div className="px-1 py-4">
            <p className="text-sm text-mist">The agent hasn&apos;t noted anything yet.</p>
            <p className="mt-1 font-mono text-xs leading-relaxed text-fog">
              As it builds, the conventions it sets, the decisions it makes, and
              the hazards it spots get written here — and carried into every later
              turn.
            </p>
          </div>
        ) : (
          <div className="flex flex-col gap-5">
            {/* The one-line digest the backend folds into each prompt. Probe
                left rule marks it as the agent's own summary of itself. */}
            {summary !== "" && (
              <div className="border-l-2 border-probe pl-3">
                <p className="gauge mb-1 text-acid-lime">In one line</p>
                <p className="text-sm leading-relaxed text-fog">{summary}</p>
              </div>
            )}

            {memories.length > 0 && (
              <section className="flex flex-col gap-2">
                <p className="gauge">Memory · {memories.length}</p>
                {memories.map((m) => (
                  <article
                    key={m.id}
                    className="surface-2 rounded-xl p-3"
                  >
                    <div className="mb-1.5 flex items-center gap-2">
                      <span className={`gauge ${toneFor(m.entry_type)}`}>
                        {m.entry_type}
                      </span>
                      {m.metadata && (
                        <span className="gauge truncate text-ash" title={m.metadata}>
                          {m.metadata}
                        </span>
                      )}
                    </div>
                    <p className="text-sm leading-relaxed text-mist">{m.content}</p>
                  </article>
                ))}
              </section>
            )}

            {decisions.length > 0 && (
              <section className="flex flex-col gap-2">
                <p className="gauge">Decisions · {decisions.length}</p>
                {decisions.map((d) => (
                  <article
                    key={d.id}
                    className="surface-2 rounded-xl p-3"
                  >
                    <p className="text-sm font-medium leading-relaxed text-mist">
                      {d.decision}
                    </p>
                    {d.outcome && (
                      <p className="mt-1 font-mono text-xs leading-relaxed text-fog">
                        {d.outcome}
                      </p>
                    )}
                  </article>
                ))}
              </section>
            )}
          </div>
        )}
      </div>
    </div>
  );
}
