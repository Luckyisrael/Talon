"use client";

/**
 * TestOnPhone — the DV2 phone surface: a QR the installed dev client opens,
 * the deep link to copy, and the dev-client APK for first-time phones.
 *
 * The panel is honesty-constrained like everything else: it renders from
 * the server's /test-info verdict. Unavailable shows the one-line reason
 * ("no device session running"), never a dead QR.
 */
import { useEffect, useState } from "react";
import QRCode from "@/components/ui/QRCode";
import { testInfo } from "@/lib/api";

interface TestOnPhoneProps {
  projectId: number;
  /** Bumped by the parent when the device session changes so the panel
   * refetches: session start, stop, destroy. */
  epoch?: number;
}

export default function TestOnPhone({ projectId, epoch }: TestOnPhoneProps) {
  const [info, setInfo] = useState<Awaited<ReturnType<typeof testInfo.get>> | null>(null);
  const [copied, setCopied] = useState(false);
  const [apkUrl, setApkUrl] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    testInfo
      .get(projectId)
      .then((res) => {
        if (cancelled) return;
        setInfo(res);
      })
      .catch(() => {
        // An unreachable endpoint leaves the panel in its honest default
        // (null → the quiet state), not a spinner forever.
        if (!cancelled) setInfo(null);
      });
    return () => {
      cancelled = true;
    };
  }, [projectId, epoch]);

  // The APK link needs the browser's own origin for the token-authed
  // download; the API client knows the base.
  useEffect(() => {
    if (info?.available && info.dev_client_url) {
      setApkUrl(info.dev_client_url);
    } else {
      setApkUrl(null);
    }
  }, [info]);

  const copy = async () => {
    if (!info?.deep_link) return;
    try {
      await navigator.clipboard.writeText(info.deep_link);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      // Clipboard API denied: the link stays selectable text.
    }
  };

  return (
    <section className="border-t border-graphite px-3 py-3">
      <div className="flex items-baseline justify-between gap-2">
        <span className="gauge">Test on your phone</span>
        {info?.available ? (
          <span className="lamp" data-state="ready" aria-hidden />
        ) : null}
      </div>

      {!info ? (
        <p className="mt-1.5 text-xs text-fog">Checking the phone link…</p>
      ) : !info.available ? (
        <p className="mt-1.5 text-xs text-fog">{info.reason}</p>
      ) : (
        <div className="mt-2.5 flex flex-col gap-2.5">
          {/* The QR the phone camera (or the installed dev client) reads. */}
          <div className="flex justify-center rounded-lg border border-graphite bg-white p-2.5">
            <QRCode text={info.deep_link ?? ""} size={168} />
          </div>
          <p className="text-center text-[11px] leading-relaxed text-fog">
            Scan with your phone camera, or open in the Talon dev client.
          </p>

          {/* The deep link, copyable for typed entry. */}
          <div className="flex items-center gap-1.5">
            <code className="flex-1 truncate rounded border border-graphite bg-ink/40 px-2 py-1 font-mono text-[11px] text-mist">
              {info.deep_link}
            </code>
            <button type="button" onClick={copy} className="btn btn-ghost h-7 px-2 text-xs">
              {copied ? "copied" : "copy"}
            </button>
          </div>

          {/* First-time phones: the dev client itself. */}
          {apkUrl ? (
            <a
              href={apkUrl}
              className="btn btn-sm w-full text-center text-xs"
              title="The Talon dev client for Android — install once, opens any project's QR"
            >
              Install the dev client (APK)
            </a>
          ) : (
            <p className="text-[11px] text-fog">
              Dev client APK not built on this server — the QR needs the
              installed app.
            </p>
          )}
        </div>
      )}
    </section>
  );
}
