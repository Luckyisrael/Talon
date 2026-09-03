"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { git as gitApi } from "@/lib/api";
import type { GitStatus, GitRemote, GitPushResult } from "@/lib/types";

/**
 * Push the project's workspace to GitHub, from inside the IDE.
 *
 * The control only exists when the server can actually honour it: the OAuth app
 * must be configured on the server and the user's tier must unlock it. A free
 * user, or a server with no GitHub app, sees nothing here rather than a button
 * that would only ever error — the capability is discovered and unlocked from
 * Settings, which is its canonical home. Every gate shown here is the server's
 * own answer from GET /v1/git/status, never a client guess, and the push route
 * re-checks all of it anyway; the UI just mirrors the truth so it never lies.
 */

interface PushToGitHubProps {
  projectId: number;
}

export default function PushToGitHub({ projectId }: PushToGitHubProps) {
  const [status, setStatus] = useState<GitStatus | null>(null);
  const [remote, setRemote] = useState<GitRemote | null>(null);
  const [open, setOpen] = useState(false);
  const [message, setMessage] = useState("");
  const [pushing, setPushing] = useState(false);
  const [result, setResult] = useState<GitPushResult | null>(null);
  const [error, setError] = useState<string | null>(null);
  const rootRef = useRef<HTMLDivElement>(null);

  // The status decides whether the control renders at all, so it loads once on
  // mount rather than waiting for the popover to open.
  useEffect(() => {
    let cancelled = false;
    gitApi
      .status()
      .then((s) => {
        if (!cancelled) setStatus(s);
      })
      .catch(() => {
        // A failed status check leaves the control hidden — better silent than a
        // button that pretends the feature is ready when we couldn't confirm it.
        if (!cancelled) setStatus(null);
      });
    return () => {
      cancelled = true;
    };
  }, []);

  // Re-read status and the project's remote each time the popover opens: the
  // user may have connected their account in another tab since mount, and the
  // remote tells them exactly which repo a push will land in.
  const load = useCallback(() => {
    gitApi
      .status()
      .then((s) => setStatus(s))
      .catch(() => {});
    gitApi
      .getRemote(projectId)
      .then((r) => setRemote(r))
      .catch(() => setRemote(null));
  }, [projectId]);

  useEffect(() => {
    if (open) load();
  }, [open, load]);

  // Close on outside click and Escape — a popover that traps you is worse than
  // no popover. Mirrors CheckpointMenu.
  useEffect(() => {
    if (!open) return;
    function onDown(e: MouseEvent) {
      if (!rootRef.current?.contains(e.target as Node)) setOpen(false);
    }
    function onKey(e: KeyboardEvent) {
      if (e.key === "Escape") setOpen(false);
    }
    document.addEventListener("mousedown", onDown);
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("mousedown", onDown);
      document.removeEventListener("keydown", onKey);
    };
  }, [open]);

  async function handlePush() {
    setPushing(true);
    setError(null);
    setResult(null);
    try {
      const trimmed = message.trim();
      const res = await gitApi.push(projectId, trimmed ? { message: trimmed } : {});
      setResult(res);
      // Keep the remote line in step with what we just pushed to.
      setRemote((prev) =>
        prev
          ? { ...prev, html_url: res.html_url }
          : prev,
      );
    } catch (err) {
      setError(err instanceof Error ? err.message : "Could not push to GitHub.");
    } finally {
      setPushing(false);
    }
  }

  // The control is dark unless the server has GitHub configured AND the user's
  // tier unlocks it. Both facts come from the server; neither is inferred.
  if (!status || !status.configured || !status.paid) return null;

  const connected = status.connected;

  return (
    <div ref={rootRef} className="relative">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        aria-expanded={open}
        aria-haspopup="menu"
        className="btn btn-ghost gap-1.5"
        data-active={open || undefined}
        title="Push this project to GitHub"
      >
        <svg
          aria-hidden
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          strokeWidth={1.75}
          strokeLinecap="round"
          strokeLinejoin="round"
          className="h-4 w-4"
        >
          <circle cx="6" cy="6" r="2.5" />
          <circle cx="6" cy="18" r="2.5" />
          <circle cx="18" cy="8" r="2.5" />
          <path d="M6 8.5v7" />
          <path d="M18 10.5c0 3.6-3.5 4.4-6.5 4.9" />
        </svg>
        <span className="hidden sm:inline">Push</span>
      </button>

      {open && (
        <div
          role="menu"
          className="rise absolute right-0 top-full z-30 mt-1 w-80 overflow-hidden rounded-xl border border-smoke bg-carbon shadow-xl"
        >
          <div className="bay-head">
            <span className="gauge">Push to GitHub</span>
          </div>

          {!connected ? (
            // Configured and paid, but no linked account yet. The connect flow
            // lives in Settings (its OAuth callback returns there), so we send
            // them there rather than starting a redirect that would tear down
            // the IDE mid-build.
            <div className="px-3 py-4">
              <p className="text-sm text-fog">
                Connect your GitHub account to push this project. You only need to
                do it once.
              </p>
              <button
                type="button"
                onClick={() => {
                  window.open("/settings", "_blank", "noopener,noreferrer");
                  setOpen(false);
                }}
                className="btn btn-ghost mt-3"
              >
                Open Settings ↗
              </button>
            </div>
          ) : (
            <div className="px-3 py-3">
              {status.login && (
                <p className="gauge mb-2">
                  as {status.login}
                  {remote ? ` · ${remote.owner}/${remote.repo}` : ""}
                </p>
              )}

              <label className="block">
                <span className="sr-only">Commit message</span>
                <input
                  type="text"
                  value={message}
                  onChange={(e) => setMessage(e.target.value)}
                  placeholder="Commit message (optional)"
                  maxLength={200}
                  disabled={pushing}
                  className="field"
                />
              </label>

              <button
                type="button"
                onClick={() => void handlePush()}
                disabled={pushing}
                className="btn btn-go mt-3 w-full"
              >
                {pushing
                  ? "Pushing…"
                  : remote
                    ? "Push changes"
                    : "Create repo & push"}
              </button>

              {result && !error && (
                <div className="mt-3 border-t border-graphite pt-3">
                  <p className="text-sm text-mist">
                    {result.nothing_todo
                      ? "Already up to date — nothing new to push."
                      : result.commit_made
                        ? `Pushed to ${result.repo} on ${result.branch}.`
                        : `Pushed to ${result.repo}.`}
                  </p>
                  {result.html_url && (
                    <a
                      href={result.html_url}
                      target="_blank"
                      rel="noopener noreferrer"
                      className="mt-1 inline-block font-mono text-xs text-[#4cc069] underline underline-offset-2"
                    >
                      View on GitHub ↗
                    </a>
                  )}
                </div>
              )}
            </div>
          )}

          {error && (
            <p className="border-t border-graphite px-3 py-2 font-mono text-xs text-coral-red">{error}</p>
          )}
        </div>
      )}
    </div>
  );
}
