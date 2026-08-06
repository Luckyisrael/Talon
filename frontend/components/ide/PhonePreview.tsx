"use client";

import { Mobile, Warning2 } from "iconsax-reactjs";

interface PhonePreviewProps {
  status?: "idle" | "connecting" | "streaming" | "error";
}

export default function PhonePreview({ status = "idle" }: PhonePreviewProps) {
  return (
    <div className="flex flex-col h-full min-h-0 bg-surface-1">
      <div className="h-12 flex items-center justify-between px-4 flex-shrink-0 bg-surface-1">
        <span className="text-xs font-mono uppercase tracking-wider text-text-secondary">
          Preview
        </span>
        <div className="flex items-center gap-2">
          <div
            className={`w-2 h-2 rounded-full ${
              status === "streaming"
                ? "bg-success animate-pulse"
                : status === "connecting"
                ? "bg-warning animate-pulse"
                : status === "error"
                ? "bg-error"
                : "bg-text-secondary/40"
            }`}
          />
          <span className="text-xs text-text-secondary capitalize">{status}</span>
        </div>
      </div>

      <div className="flex-1 min-h-0 overflow-y-auto flex items-center justify-center p-4">
        <div className="relative w-full max-w-[280px]">
          <div className="relative bg-surface-4 rounded-[36px] p-2">
            <div className="relative bg-surface-2 rounded-[28px] overflow-hidden aspect-[9/19.5]">
              <div className="h-10 flex items-center justify-between px-5 pt-1">
                <span className="text-[9px] font-mono text-text-secondary">9:41</span>
                <div className="flex gap-0.5">
                  <div className="w-2.5 h-1.5 bg-text-secondary rounded-sm" />
                  <div className="w-2.5 h-1.5 bg-text-secondary rounded-sm" />
                  <div className="w-3 h-1.5 bg-text-secondary rounded-sm" />
                </div>
              </div>

              <div className="absolute inset-0 top-10 bottom-12 flex items-center justify-center">
                {status === "idle" && (
                  <div className="text-center px-6">
                    <div className="w-12 h-12 rounded-xl bg-surface-3 flex items-center justify-center mx-auto mb-3">
                      <Mobile size="24" className="text-text-secondary" variant="Linear" />
                    </div>
                    <p className="text-[10px] text-text-secondary">
                      Start a session to preview your app
                    </p>
                  </div>
                )}

                {status === "connecting" && (
                  <div className="text-center">
                    <div className="w-8 h-8 border-2 border-accent border-t-transparent rounded-full animate-spin mx-auto mb-2" />
                    <p className="text-[10px] text-text-secondary">Connecting...</p>
                  </div>
                )}

                {status === "streaming" && (
                  <div className="w-full h-full bg-black/10" />
                )}

                {status === "error" && (
                  <div className="text-center px-6">
                    <div className="w-12 h-12 rounded-xl bg-error/20 flex items-center justify-center mx-auto mb-3">
                      <Warning2 size="24" className="text-error" variant="Linear" />
                    </div>
                    <p className="text-[10px] text-error">Connection failed</p>
                  </div>
                )}
              </div>

              <div className="absolute bottom-0 left-0 right-0 h-12 flex items-center justify-around px-5 bg-surface-2">
                <div className="w-5 h-5 rounded-full bg-accent/20" />
                <div className="w-5 h-5 rounded-full bg-text-primary/10" />
                <div className="w-5 h-5 rounded-full bg-text-primary/10" />
              </div>
            </div>
          </div>
        </div>
      </div>
    </div>
  );
}
