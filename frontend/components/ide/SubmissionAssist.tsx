"use client";

import { useEffect, useState } from "react";
import { exportApi } from "@/lib/api";
import type { SubmissionBundle } from "@/lib/types";

/**
 * What a finished export hands over — FM-SUBa.
 *
 * The bundle is written to the server's disk and served verbatim, so this
 * shows what the server committed to instead of re-authoring it. Two things
 * are deliberately NOT authored here:
 *
 *   the portal address — Solana Mobile documents no deep-link scheme for the
 *   Publisher Portal (publish.solanamobile.com takes no path and no query;
 *   you find the app in the sidebar after logging in), so there is nothing
 *   to deep-link with and the address has to travel on the wire;
 *
 *   the package name — `solanadappstore://details?id=` exists but opens the
 *   LISTING page, which does not exist until the app is approved, so it is
 *   useless at this moment. The portal matches by package name, which is why
 *   the name is printed beside the button: it is what the user carries there.
 *
 * Talon never submits anything — no publisher keypair is ever touched.
 */

/** Save a string to disk. Mirrors the APK download in ExportPanel. */
function save(filename: string, contents: string, mime: string) {
  const url = URL.createObjectURL(new Blob([contents], { type: mime }));
  const a = document.createElement("a");
  a.href = url;
  a.download = filename;
  a.click();
  URL.revokeObjectURL(url);
}

export function SubmissionAssist({ jobId }: { jobId: number }) {
  const [bundle, setBundle] = useState<SubmissionBundle | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let live = true;
    exportApi
      .bundle(jobId)
      .then((b) => {
        if (live) setBundle(b);
      })
      .catch(() => {
        if (live) setError("Could not load the submission bundle.");
      });
    return () => {
      live = false;
    };
  }, [jobId]);

  if (error) return <p className="mt-3 text-xs text-coral-red">{error}</p>;
  if (!bundle) {
    return <p className="mt-3 text-sm text-fog">Preparing the submission instructions…</p>;
  }

  const { manifest } = bundle;
  return (
    <div className="mt-3">
      <p className="text-sm text-mist">
        Your package{" "}
        <span className="font-mono text-lime">{manifest.android_package}</span>
      </p>
      <a
        href={manifest.portal_url}
        target="_blank"
        rel="noreferrer"
        className="btn btn-go mt-2 w-full text-center"
      >
        Open Publisher Portal
      </a>
      {/* The disclosure is the server's to state: hand-typed cost copy on two
          surfaces is a disclosure that eventually says something the server
          would not (FM-SUBa(iii)). */}
      <p className="mt-2 font-mono text-xs leading-relaxed text-ash">{manifest.costs_note}</p>
      <div className="mt-2 flex gap-2">
        <button
          type="button"
          onClick={() => save("SUBMIT.md", bundle.submit_md, "text/markdown")}
          className="btn w-full"
        >
          Download SUBMIT.md
        </button>
        <button
          type="button"
          onClick={() => save("dapp-store.json", JSON.stringify(manifest, null, 2), "application/json")}
          className="btn w-full"
        >
          Download dapp-store.json
        </button>
      </div>
    </div>
  );
}
