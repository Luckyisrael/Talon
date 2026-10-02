"use client";

import { useEffect, useRef, useState } from "react";
import { exportApi, projects as projectsApi, usage as usageApi } from "@/lib/api";
import type { ExportJob, Project, ProjectAsset } from "@/lib/types";
import { SubmissionAssist } from "./SubmissionAssist";

/**
 * Release export, from inside the IDE.
 *
 * Starts a tier-gated, async release build (signed APK + dApp Store
 * submission bundle), polls the job while it runs, and hands over the
 * download when it lands — plus the honest SOL-cost copy before the user
 * leaves for Solana's own submission flow. The server enforces the tier and
 * does the work; this panel only mirrors the truth, so a free user sees why
 * the button refuses rather than a button that pretends otherwise.
 */
interface ExportPanelProps {
  projectId: number;
}

export default function ExportPanel({ projectId }: ExportPanelProps) {
  const [open, setOpen] = useState(false);
  const [isPro, setIsPro] = useState(false);
  const [whatsNew, setWhatsNew] = useState("");
  const [job, setJob] = useState<ExportJob | null>(null);
  const [starting, setStarting] = useState(false);
  const [downloading, setDownloading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [project, setProject] = useState<Project | null>(null);
  const [assets, setAssets] = useState<ProjectAsset[]>([]);
  const [uploading, setUploading] = useState<string | null>(null);
  const rootRef = useRef<HTMLDivElement>(null);

  // Tier comes from the server on open — the button label never decides
  // eligibility, the export endpoint does. Project + assets load alongside
  // so the store checklist is visible before the (long) build starts.
  useEffect(() => {
    if (!open) return;
    usageApi
      .get()
      .then((u) => setIsPro(u.tier !== "free"))
      .catch(() => {});
    projectsApi
      .get(projectId)
      .then(setProject)
      .catch(() => {});
    exportApi
      .listAssets(projectId)
      .then(setAssets)
      .catch(() => {});
  }, [open, projectId]);

  // Close on outside click and Escape. Mirrors CheckpointMenu.
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
  }, [open ]);

  // Poll while the job is live. Release gradle builds take tens of minutes;
  // the poll stops itself the moment the job lands somewhere terminal.
  useEffect(() => {
    if (!open || !job || (job.status !== "queued" && job.status !== "running")) return;
    const id = setInterval(async () => {
      try {
        const next = await exportApi.get(job.id);
        setJob(next);
      } catch {
        /* keep polling; a transient failure is not a verdict */
      }
    }, 10000);
    return () => clearInterval(id);
  }, [open, job]);

  async function start() {
    if (!whatsNew.trim()) return;
    setStarting(true);
    setError(null);
    try {
      const res = await exportApi.start(projectId, whatsNew.trim());
      setJob({ id: res.job_id, project_id: res.project_id, status: "queued" });
    } catch (err) {
      setError(err instanceof Error ? err.message : "Could not start the export.");
    } finally {
      setStarting(false);
    }
  }

  // Downloads ride fetch, not an anchor: the endpoint needs the Bearer token
  // and a plain link cannot send one.
  async function download() {
    if (!job || job.status !== "done") return;
    setDownloading(true);
    try {
      const token = localStorage.getItem("token");
      const res = await fetch(exportApi.downloadUrl(job.id), {
        headers: token ? { Authorization: `Bearer ${token}` } : {},
      });
      if (!res.ok) throw new Error(`download failed (${res.status})`);
      const blob = await res.blob();
      const url = URL.createObjectURL(blob);
      const a = document.createElement("a");
      a.href = url;
      a.download = `project-${projectId}-release.apk`;
      a.click();
      URL.revokeObjectURL(url);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Could not download the APK.");
    } finally {
      setDownloading(false);
    }
  }

  const live = job && (job.status === "queued" || job.status === "running");

  const shots = assets.filter((a) => a.kind === "screenshot");
  const icon = assets.find((a) => a.kind === "icon");
  const banner = assets.find((a) => a.kind === "banner");

  async function upload(kind: "screenshot" | "icon" | "banner", file: File) {
    setUploading(kind);
    setError(null);
    try {
      await exportApi.uploadAsset(projectId, kind, file);
      setAssets(await exportApi.listAssets(projectId));
    } catch (err) {
      setError(err instanceof Error ? err.message : "Upload failed.");
    } finally {
      setUploading(null);
    }
  }

  function row(
    label: string,
    present: boolean,
    detail: string,
    kind?: "screenshot" | "icon" | "banner",
  ) {
    return (
      <div className="flex items-center justify-between py-1">
        <span className="text-sm text-fog">
          <span className={present ? "text-lime" : "text-ash"}>{present ? "● " : "○ "}</span>
          {label} <span className="font-mono text-xs text-ash">{detail}</span>
        </span>
        {kind && (
          <label className="cursor-pointer font-mono text-xs text-mist underline underline-offset-2 hover:text-paper">
            {uploading === kind ? "…" : "upload"}
            <input
              type="file"
              accept="image/png,image/jpeg"
              className="hidden"
              disabled={uploading !== null}
              onChange={(e) => {
                const f = e.target.files?.[0];
                e.target.value = "";
                if (f) void upload(kind, f);
              }}
            />
          </label>
        )}
      </div>
    );
  }

  return (
    <div ref={rootRef} className="relative">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        title="Release export — signed APK + store bundle"
        className="btn btn-ghost"
      >
        Export
      </button>

      {open && (
        <div className="rise absolute right-0 top-full z-30 mt-1 w-80 overflow-hidden rounded-xl border border-smoke bg-carbon shadow-xl">
          <div className="border-b border-graphite p-3">
            <p className="gauge mb-1">Release export</p>
            <p className="text-sm text-mist">
              A signed release APK plus the dApp Store submission bundle. You submit
              with your own publisher wallet — Talon never touches it.
            </p>
          </div>

          <div className="p-3">
            {!isPro ? (
              <p className="text-sm text-fog">
                Release export needs the Pro plan.{" "}
                <a href="/pricing" className="text-mist underline underline-offset-2 hover:text-paper">
                  See pricing
                </a>
              </p>
            ) : !job ? (
              <>
                <p className="gauge mb-1">Store checklist</p>
                <div className="mb-3 rounded-lg border border-graphite px-2 py-1">
                  <div className="flex items-center justify-between py-1">
                    <span className="text-sm text-fog">
                      <span className="text-lime">● </span>
                      Bundle <span className="font-mono text-xs text-ash">{project?.android_package || "…"}</span>
                    </span>
                  </div>
                  {row("Icon", !!icon, icon ? `${icon.width}×${icon.height}` : "512×512 PNG", "icon")}
                  {row("Banner", !!banner, banner ? `${banner.width}×${banner.height}` : "1024×500", "banner")}
                  {row("Screenshots", shots.length > 0, `${shots.length} uploaded`, "screenshot")}
                </div>
                <label className="block">
                  <span className="gauge">What&apos;s new in this release</span>
                  <textarea
                    rows={2}
                    value={whatsNew}
                    onChange={(e) => setWhatsNew(e.target.value)}
                    placeholder="Bug fixes and performance improvements"
                    className="field mt-2 resize-none leading-snug"
                  />
                </label>
                {error && <p className="mt-2 text-xs text-coral-red">{error}</p>}
                <button
                  type="button"
                  onClick={start}
                  disabled={starting || !whatsNew.trim()}
                  className="btn btn-go mt-3 w-full"
                >
                  {starting ? "Starting…" : "Start release build"}
                </button>
                <p className="mt-2 font-mono text-xs text-ash">
                  Full clean build — takes a while, runs in the background.
                  Publishing costs real SOL (~0.2); you submit yourself.
                </p>
              </>
            ) : (
              <>
                <div className="flex items-center justify-between">
                  <span className="gauge">Job #{job.id}</span>
                  <span className="badge">{job.status}</span>
                </div>
                {live ? (
                  <p className="mt-2 text-sm text-fog">
                    Building your release — prebuild, sign, verify. This takes a
                    while; you can close this and keep working.
                  </p>
                ) : job.status === "done" ? (
                  <div className="mt-2">
                    <button
                      type="button"
                      onClick={download}
                      disabled={downloading}
                      className="btn btn-go w-full"
                    >
                      {downloading ? "Downloading…" : "Download signed APK"}
                    </button>
                    {/* FM-SUBa: the how-to-submit is served, never typed here.
                        SubmissionAssist carries the package, the portal
                        address, the server's own costs note and both files. */}
                    <SubmissionAssist jobId={job.id} />
                  </div>
                ) : (
                  <p className="mt-2 text-sm text-coral-red">
                    {job.error || "The export failed."}
                  </p>
                )}
                {error && <p className="mt-2 text-xs text-coral-red">{error}</p>}
                {job.status === "done" || job.status === "failed" ? (
                  <button
                    type="button"
                    onClick={() => {
                      setJob(null);
                      setError(null);
                    }}
                    className="btn mt-3 w-full"
                  >
                    Start a new export
                  </button>
                ) : null}
              </>
            )}
          </div>
        </div>
      )}
    </div>
  );
}
