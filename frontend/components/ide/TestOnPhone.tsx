"use client";

/**
 * TestOnPhone — the device rail's "Get the app" pane: a QR the installed dev
 * client opens, the deep link to copy, and the dev-client APK for first-time
 * phones.
 *
 * The panel is honesty-constrained like everything else: it renders from
 * the server's /test-info verdict. Unavailable shows the one-line reason
 * ("no device session running"), never a dead QR.
 */
import { useEffect, useState } from "react";
import QRCode from "@/components/ui/QRCode";
import { testInfo } from "@/lib/api";
import { copyText } from "@/lib/clipboard";

interface TestOnPhoneProps {
  projectId: number;
  /** Bumped by the parent when the device session changes so the panel
   * refetches: session start, stop, destroy. */
  epoch?: number;
}

export default function TestOnPhone({ projectId, epoch }: TestOnPhoneProps) {
  const [info, setInfo] = useState<Awaited<ReturnType<typeof testInfo.get>> | null>(null);
  const [copied, setCopied] = useState(false);

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
  // download; the API client knows the base. Derived, not effect-stored —
  // the url is a pure function of the verdict.
  const apkUrl = info?.available && info.dev_client_url ? info.dev_client_url : null;

  const copy = async () => {
    if (!info?.deep_link) return;
    // Works in insecure contexts too — the Clipboard API alone does not.
    if (await copyText(info.deep_link)) {
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    }
  };

  return (
    <section className="flex flex-col gap-4 px-4 py-5">
      {!info ? (
        <div className="flex flex-col items-center gap-3 py-10 text-center">
          <span className="lamp" data-state="waiting" aria-hidden />
          <p className="text-sm text-fog">Checking the phone link…</p>
        </div>
      ) : !info.available ? (
        /* The honest empty state: a dead QR is worse than a sentence. */
        <div className="surface-2 flex flex-col items-center gap-3 rounded-2xl px-6 py-10 text-center">
          <span className="lamp" data-state="fault" aria-hidden />
          <p className="text-sm text-mist">No phone link yet</p>
          <p className="text-xs leading-relaxed text-fog">{info.reason}</p>
          <p className="text-xs leading-relaxed text-ash">
            Start a device and the QR appears — it carries the token the relay
            needs to reach this build.
          </p>
        </div>
      ) : (
        <>
          {/* The QR — a white card so the modules stay scannable in a dark
              UI. It is the whole point of this pane. */}
          <div className="flex flex-col items-center gap-3">
            <div className="rounded-2xl bg-white p-4 shadow-[0_12px_40px_rgb(0_0_0/0.4)]">
              <QRCode text={info.deep_link ?? ""} size={192} />
            </div>
            <p className="text-center text-xs leading-relaxed text-fog">
              Scan with your phone camera, or open in the Talon dev client.
            </p>
          </div>

          {/* The deep link, copyable for typed entry. */}
          <div className="surface-2 flex items-center gap-2 rounded-lg p-1.5">
            <code className="min-w-0 flex-1 truncate px-1.5 font-mono text-[11px] text-mist">
              {info.deep_link}
            </code>
            <button
              type="button"
              onClick={copy}
              className="btn btn-ghost h-7 flex-shrink-0 px-2 text-xs"
            >
              {copied ? "copied" : "copy"}
            </button>
          </div>

          {/* Build metadata — what the person is about to install. */}
          <div className="flex flex-col">
            {(
              [
                ["Access", "this build only"],
                ["Signed by", "this studio's relay"],
                ["Expires", "with the session"],
              ] as const
            ).map(([k, v]) => (
              <div
                key={k}
                className="flex items-center justify-between border-b border-graphite/60 py-2 text-xs last:border-b-0"
              >
                <span className="text-fog">{k}</span>
                <span className="font-mono text-mist">{v}</span>
              </div>
            ))}
          </div>

          {/* First-time phones: the dev client itself. */}
          {apkUrl ? (
            <a
              href={apkUrl}
              className="btn btn-go h-10 w-full justify-center text-sm"
              title="The Talon dev client for Android — install once, opens any project's QR"
            >
              Install the dev client (APK)
            </a>
          ) : (
            <p className="text-center text-[11px] text-ash">
              Dev client APK not built on this server — the QR needs the
              installed app.
            </p>
          )}

          <p className="text-center text-[11px] leading-relaxed text-ash">
            On your own device the agent can&rsquo;t see the screen or logs —
            changes you test here are yours to verify, and the emulator preview
            stays its source of truth.
          </p>
        </>
      )}
    </section>
  );
}
