"use client";

import { useEffect, useRef } from "react";

/**
 * Root crash boundary. If anything above the page throws during render, Next
 * swaps the whole tree for this — so it carries its own <html>/<body> and
 * reports once via sendBeacon (fire-and-forget: the page is already dying,
 * so nothing awaits a response). The report holds message + path only; tokens
 * and payloads never leave the browser in it.
 */
export default function GlobalError({
  error,
  reset,
}: {
  error: Error & { digest?: string };
  reset: () => void;
}) {
  const reported = useRef(false);

  useEffect(() => {
    if (reported.current) return;
    reported.current = true;
    try {
      const token = localStorage.getItem("token");
      const body = JSON.stringify({
        message: `${error.name}: ${error.message}`.slice(0, 500),
        path: window.location.pathname.slice(0, 200),
      });
      const headers: Record<string, string> = { "Content-Type": "application/json" };
      if (token) headers["Authorization"] = `Bearer ${token}`;
      const base = process.env.NEXT_PUBLIC_API_URL || "http://localhost:8080";
      const blob = new Blob([body], { type: "application/json" });
      // sendBeacon can't set headers — fall back to fetch when authed so the
      // report attributes to the user; beacon anonymously otherwise.
      if (token) {
        void fetch(`${base}/v1/client-errors`, { method: "POST", headers, body }).catch(() => {});
      } else {
        navigator.sendBeacon(`${base}/v1/client-errors`, blob);
      }
    } catch {
      // Reporting must never break the crash screen itself.
    }
  }, [error]);

  return (
    <html lang="en">
      <body className="min-h-screen bg-void text-mist antialiased">
        <main className="mx-auto flex min-h-screen w-full max-w-2xl flex-col items-center justify-center px-6 text-center">
          <p className="font-mono text-xs uppercase tracking-widest text-ash">
            Something broke
          </p>
          <h1 className="mt-3 text-2xl font-medium tracking-tight text-paper">
            The app hit an error it couldn&apos;t recover from.
          </h1>
          <p className="mt-2 max-w-md font-mono text-xs leading-relaxed text-fog">
            {error.message || "An unexpected render failure."}
          </p>
          <p className="mt-1 font-mono text-xs text-ash">
            The failure was reported automatically.
          </p>
          <button type="button" onClick={reset} className="btn btn-go mt-6">
            Try again
          </button>
        </main>
      </body>
    </html>
  );
}
