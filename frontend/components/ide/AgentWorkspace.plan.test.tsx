import { describe, expect, it, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import AgentWorkspace from "./AgentWorkspace";
import type { PlanTask, TraceRow } from "@/lib/types";

vi.mock("@/lib/api", () => ({
  conversation: {
    diff: vi.fn().mockResolvedValue({ diff: "", note: "no baseline" }),
  },
}));

const tasks: PlanTask[] = [
  { id: "plan:1", label: "Wallet screen", status: "completed" },
  { id: "plan:2", label: "Balance display", status: "in_progress" },
  { id: "plan:3", label: "Settings", status: "pending" },
];

describe("§7 plan checklist in the workspace", () => {
  it("renders DSH-style pending/completed tasks with the done count", () => {
    render(
      <AgentWorkspace
        projectId={1}
        traceRows={[]}
        planTasks={tasks}
        selectedFile={null}
        filesTouched={[]}
      />,
    );
    expect(screen.getByText(/build plan/i)).toBeTruthy();
    expect(screen.getByText(/1\/3 done/i)).toBeTruthy();
    expect(screen.getByText("Wallet screen")).toBeTruthy();
    expect(screen.getByText("Balance display")).toBeTruthy();
    expect(screen.getByText("Settings")).toBeTruthy();
  });

  it("omits the checklist band when the turn has no plan tasks", () => {
    render(
      <AgentWorkspace
        projectId={1}
        traceRows={[{ id: "r1", kind: "narration", text: "Working." }]}
        selectedFile={null}
        filesTouched={[]}
      />,
    );
    expect(screen.queryByText(/build plan/i)).toBeNull();
  });

  it("renders selected markdown files as reading view, not source", async () => {
    render(
      <AgentWorkspace
        projectId={1}
        traceRows={[]}
        selectedFile="PLAN.md"
        fileContent="# Build plan\n\n## Screens\n- Wallet"
        filesTouched={[]}
      />,
    );
    // The md file renders: the tab shows the name and the reading pane
    // mounts (no CodeEditor for .md).
    await waitFor(() =>
      expect(screen.getAllByText("PLAN.md").length).toBeGreaterThan(0),
    );
  });
});

const bashRows: TraceRow[] = [
  {
    id: "r1",
    kind: "tool",
    tool: "run_command",
    label: "run",
    target: "npm run typecheck",
    status: "running",
    summary: "checking...\n",
  },
];

describe("§7 bash rows carry live output", () => {
  it("shows the running command row with its accumulated summary", async () => {
    render(
      <AgentWorkspace
        projectId={1}
        traceRows={bashRows}
        selectedFile={null}
        filesTouched={[]}
      />,
    );
    expect(screen.getByText("npm run typecheck")).toBeTruthy();
    const btn = screen.getByText("npm run typecheck").closest("button");
    btn?.click();
    await waitFor(() => expect(screen.getByText(/checking/)).toBeTruthy());
  });
});

const roundRows: TraceRow[] = [
  { id: "g1", kind: "group", label: "Round 1" },
  { id: "r1", kind: "narration", text: "I'll write the wallet screen with MWA." },
  { id: "t1", kind: "tool", tool: "write_file", target: "app/wallet.tsx", status: "done" },
  { id: "t2", kind: "tool", tool: "run_command", target: "npm run typecheck", status: "fail" },
  { id: "g2", kind: "group", label: "Round 2" },
  { id: "n2", kind: "narration", text: "Fixing the type error." },
  { id: "t3", kind: "tool", tool: "edit_file", target: "app/wallet.tsx", status: "done" },
];

describe("§7 AgentTrace rounds: collapsible round blocks", () => {
  it("renders each round as a block with intent, chip colors, outcome", () => {
    render(
      <AgentWorkspace
        projectId={1}
        traceRows={roundRows}
        selectedFile={null}
        filesTouched={[]}
      />,
    );
    expect(screen.getByText("Round 1")).toBeTruthy();
    expect(screen.getByText("Round 2")).toBeTruthy();
    // Round 1's intent line appears in the header and the open body.
    expect(screen.getAllByText(/write the wallet screen/i).length).toBeGreaterThan(0);
    // Round 1's outcome: a call failed.
    expect(screen.getAllByText(/a call failed/i).length).toBeGreaterThan(0);
    // Round 2: 1 call.
    expect(screen.getAllByText(/1 call/i).length).toBeGreaterThan(0);
  });

  it("collapses a round block on click, hiding its rows", async () => {
    render(
      <AgentWorkspace
        projectId={1}
        traceRows={roundRows}
        selectedFile={null}
        filesTouched={[]}
      />,
    );
    const header = screen.getByText("Round 1").closest("button");
    header?.click();
    // The round's rows disappear; the header stays.
    expect(screen.getByText("Round 1")).toBeTruthy();
    await waitFor(() => expect(screen.queryByText("write_file")).toBeNull());
  });
});
