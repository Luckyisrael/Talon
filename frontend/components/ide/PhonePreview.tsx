"use client";

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
                      <svg className="w-6 h-6 text-text-secondary" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                        <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={1.5} d="M12 18h.01M8 21h8a2 2 0 002-2V5a2 2 0 00-2-2H8a2 2 0 00-2 2v14a2 2 0 002 2z" />
                      </svg>
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
                      <svg className="w-6 h-6 text-error" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                        <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M12 9v2m0 4h.01m-6.938 4h13.856c1.54 0 2.502-1.667 1.732-3L13.732 4c-.77-1.333-2.694-1.333-3.464 0L3.34 16c-.77 1.333.192 3 1.732 3z" />
                      </svg>
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
