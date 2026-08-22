"use client";

import TopBar from "./TopBar";

interface AppShellProps {
  children: React.ReactNode;
  tier?: "free" | "pro";
}

export default function AppShell({ children, tier = "free" }: AppShellProps) {
  return (
    <div className="flex flex-col h-full bg-ink">
      <TopBar tier={tier} />
      <main className="flex-1 overflow-auto">{children}</main>
    </div>
  );
}
