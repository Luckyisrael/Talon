"use client";

/**
 * ChangedFiles — the canvas's files view: the files this turn touched, each
 * a row that expands into its inline diff.
 *
 * The prototype's design: a file row (icon, path, +/− delta, chevron) with
 * the diff folding open beneath it on the grid-rows 0fr→1fr collapse. The
 * whole turn's changes are readable without leaving the conversation, and
 * the diff is real — the workspace's working tree against the provision-time
 * template baseline, the same endpoint the old DiffView used.
 *
 * The diff is parsed per-line into add/del/context so the colour carries
 * meaning. A file row's delta is counted from its own hunks, not from the
 * whole diff, so a row's numbers describe that file.
 */

import { useEffect, useMemo, useState } from "react";
import { conversation } from "@/lib/api";

interface ChangedFilesProps {
  projectId: number;
  /** The paths the last turn touched. Drives the refetch and the row set. */
  filesTouched: string[];
}

type Line = { kind: "add" | "del" | "ctx" | "hunk" | "meta"; text: string };

// The unified diff's per-file sections are separated by the "---/+++ meta
// pair and a diff --git line. Splitting on those gives one block per file,
// which is how the row's own delta is counted.
function splitByFile(diff: string): Map<string, string[]> {
  const out = new Map<string, string[]>();
  const lines = diff.split("\n");
  let current: string | null = null;
  for (const line of lines) {
    // "+++ b/path" names the file; the b/ prefix is the git convention.
    if (line.startsWith("+++ ")) {
      const path = line.slice(4).replace(/^b\//, "");
      if (path !== "/dev/null") {
        current = path;
        if (!out.has(current)) out.set(current, []);
      }
      continue;
    }
    if (current) {
      out.get(current)!.push(line);
    }
  }
  return out;
}

function classify(line: string): Line {
  if (line.startsWith("@@")) return { kind: "hunk", text: line };
  if (line.startsWith("+++") || line.startsWith("---")) return { kind: "meta", text: line };
  if (line.startsWith("+")) return { kind: "add", text: line };
  if (line.startsWith("-")) return { kind: "del", text: line };
  return { kind: "ctx", text: line };
}

export default function ChangedFiles({ projectId, filesTouched }: ChangedFilesProps) {
  const [diff, setDiff] = useState<string>("");
  const [note, setNote] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [expanded, setExpanded] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    setLoading(true);
    conversation
      .diff(projectId)
      .then((res) => {
        if (cancelled) return;
        setDiff(res?.diff ?? "");
        setNote(res?.note ?? null);
      })
      .catch(() => {
        if (!cancelled) {
          setDiff("");
          setNote("The diff could not be loaded.");
        }
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [projectId, filesTouched.length]);

  // Per-file hunks, each classified. Memoised because the diff can be large.
  const fileMap = useMemo(() => (diff ? splitByFile(diff) : new Map()), [diff]);

  const rows = useMemo(() => {
    if (!diff) return [];
    // Show the touched files that actually appear in the diff; the rest of
    // the diff is other files the person did not ask about this turn.
    const touched = new Set(filesTouched);
    return [...fileMap.keys()]
      .filter((p) => touched.size === 0 || touched.has(p))
      .map((path) => {
        const lines = fileMap.get(path) ?? [];
        let add = 0;
        let del = 0;
        for (const l of lines) {
          if (l.startsWith("+") && !l.startsWith("+++")) add++;
          else if (l.startsWith("-") && !l.startsWith("---")) del++;
        }
        return { path, add, del, lines };
      });
  }, [fileMap, filesTouched, diff]);

  if (loading) {
    return (
      <div className="files-view">
        <h2>Changed files</h2>
        <p className="lede">Reading the workspace changes…</p>
      </div>
    );
  }

  if (note && !diff) {
    return (
      <div className="files-view">
        <h2>Changed files</h2>
        <p className="lede">{note}</p>
      </div>
    );
  }

  if (!rows.length) {
    return (
      <div className="files-view">
        <h2>Changed files</h2>
        <p className="lede">
          {filesTouched.length > 0
            ? "This turn's changes are not in the diff yet — it lands when the turn finishes."
            : "Nothing has been changed yet. Describe what to build and the changes appear here."}
        </p>
      </div>
    );
  }

  return (
    <div className="files-view">
      <h2>Changed files</h2>
      <p className="lede">
        {rows.length} file{rows.length === 1 ? "" : "s"} changed · against the template baseline
      </p>

      {rows.map(({ path, add, del, lines }) => {
        const open = expanded === path;
        return (
          <div key={path}>
            <button
              type="button"
              aria-expanded={open}
              onClick={() => setExpanded(open ? null : path)}
              className="file-row"
            >
              <span className="ico" aria-hidden>
                <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={1.75} strokeLinecap="round" strokeLinejoin="round">
                  <path d="M4 4.5A1.5 1.5 0 0 1 5.5 3H10l2 2.5h6.5A1.5 1.5 0 0 1 20 7v12.5a1.5 1.5 0 0 1-1.5 1.5h-13A1.5 1.5 0 0 1 4 19.5z" />
                </svg>
              </span>
              <span className="path">{path}</span>
              <span className={`delta ${del > add ? "del" : "add"}`}>
                +{add} −{del}
              </span>
              <span className="ico chev" aria-hidden>
                <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2} strokeLinecap="round" strokeLinejoin="round">
                  <path d="M9 18l6-6-6-6" />
                </svg>
              </span>
            </button>
            <div className="diff-collapse" data-open={open}>
              <div>
                <pre>
                  {lines.map((line: string, i: number) => {
                    const l = classify(line);
                    return (
                      <span key={i} className={l.kind}>
                        {l.text}
                        {"\n"}
                      </span>
                    );
                  })}
                </pre>
              </div>
            </div>
          </div>
        );
      })}
    </div>
  );
}
