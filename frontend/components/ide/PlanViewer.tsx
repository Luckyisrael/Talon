"use client";

import Markdown from "@/components/ui/Markdown";
import type { PlanData } from "@/lib/types";

interface PlanViewerProps {
  message: string;
  plan: PlanData;
  onApprove: () => void;
  /** Put the centre panel back on the code without approving anything. */
  onDismiss?: () => void;
}

/**
 * The plan, at reading size.
 *
 * This used to carry its own hand-rolled markdown renderer that understood bold
 * and nothing else — a code fence in a plan came out as literal backticks. It
 * now shares the one renderer with the chat.
 */
export default function PlanViewer({
  message,
  plan,
  onApprove,
  onDismiss,
}: PlanViewerProps) {
  const screens = plan.screens ?? [];
  const features = plan.features ?? [];
  const packages = plan.packages ?? [];

  return (
    <div className="flex h-full min-h-0 flex-col bg-void">
      <header className="bay-head justify-between">
        <div className="flex items-center gap-2">
          <span className="lamp" data-state="waiting" />
          <span className="gauge">Plan · needs your go-ahead</span>
        </div>
        {onDismiss && (
          <button type="button" onClick={onDismiss} className="btn btn-ghost font-mono text-xs">
            Back to the code
          </button>
        )}
      </header>

      <div className="min-h-0 flex-1 overflow-y-auto">
        <div className="mx-auto max-w-2xl px-6 py-6 pb-16">
          {message && <Markdown>{message}</Markdown>}

          <div className="mt-7 flex flex-col gap-2">
            {screens.length > 0 && (
              <section className="card !p-5">
                <div className="mb-3 flex items-baseline justify-between">
                  <h2 className="gauge">Screens</h2>
                  <span className="gauge tnum">{screens.length}</span>
                </div>
                <ol className="flex flex-col gap-3">
                  {screens.map((screen, i) => (
                    <li key={`${screen.name}-${i}`} className="flex gap-3">
                      <span className="gauge tnum mt-0.5 flex-shrink-0">
                        {String(i + 1).padStart(2, "0")}
                      </span>
                      <div className="min-w-0">
                        <p className="text-[15px] font-medium tracking-tight text-paper">{screen.name}</p>
                        {screen.description && (
                          <p className="mt-0.5 text-sm leading-relaxed text-fog">
                            {screen.description}
                          </p>
                        )}
                      </div>
                    </li>
                  ))}
                </ol>
              </section>
            )}

            {plan.navigation && (
              <section className="card !p-5">
                <h2 className="gauge mb-2">Navigation</h2>
                <p className="text-[15px] leading-relaxed text-mist">{plan.navigation}</p>
              </section>
            )}

            {features.length > 0 && (
              <section className="card !p-5">
                <h2 className="gauge mb-2.5">Features</h2>
                <ul className="flex flex-wrap gap-1.5">
                  {features.map((feature, i) => (
                    <li
                      key={`${feature}-${i}`}
                      className="pill"
                    >
                      {feature}
                    </li>
                  ))}
                </ul>
              </section>
            )}

            {packages.length > 0 && (
              <section className="card !p-5">
                <h2 className="gauge mb-2.5">Packages to install</h2>
                <ul className="flex flex-col gap-1 font-mono text-xs text-fog">
                  {packages.map((pkg, i) => (
                    <li key={`${pkg}-${i}`}>{pkg}</li>
                  ))}
                </ul>
                <p className="mt-2.5 font-mono text-xs text-ash">
                  A package with native code means the dev client gets rebuilt,
                  which takes a few minutes the first time.
                </p>
              </section>
            )}
          </div>

          <div className="mt-7 flex items-center gap-3">
            <button type="button" onClick={onApprove} className="btn btn-go h-9 px-4">
              Build this
            </button>
            <p className="text-sm text-fog">
              Or keep talking in the chat to change it first.
            </p>
          </div>
        </div>
      </div>
    </div>
  );
}
