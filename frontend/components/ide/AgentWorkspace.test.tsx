import { describe, expect, it, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import AgentWorkspace from "./AgentWorkspace";
import type { TraceRow } from "@/lib/types";
import { conversation } from "@/lib/api";

vi.mock("@/lib/api", () => ({
  conversation: {
    diff: vi.fn().mockResolvedValue({ diff: "", note: "no baseline" }),
  },
}));

const rows: TraceRow[] = [
  { id: "r1", kind: "narration", text: "I'll add the wallet screen." },
  { id: "r2", kind: "thinking", text: "The MWA flow needs transact()." },
  {
    id: "r3",
    kind: "tool",
    tool: "write_file",
    label: "write",
    target: "app/wallet.tsx",
    status: "done",
  },
  {
    id: "r4",
    kind: "tool",
    tool: "run_command",
    label: "run",
    target: "npm run typecheck",
    status: "fail",
    summary: "TS2322 exit 1",
  },
];

describe("AgentWorkspace (§7 center pane)", () => {
  it("renders the DSH-style transcript: narration, thinking, tool chips", () => {
    render(
      <AgentWorkspace
        projectId={1}
        traceRows={rows}
        selectedFile={null}
        filesTouched={[]}
      />,
    );
    expect(screen.getByText(/agent is your editor|wallet screen/i)).toBeTruthy();
    // The transcript is the default tab: thinking + both tools visible.
    expect(screen.getByText(/thinking/i)).toBeTruthy();
    expect(screen.getByText("write_file")).toBeTruthy();
    expect(screen.getByText("run_command")).toBeTruthy();
    expect(screen.getByText("app/wallet.tsx")).toBeTruthy();
  });

  it("expands a tool row to reveal its summary", async () => {
    render(
      <AgentWorkspace
        projectId={1}
        traceRows={rows}
        selectedFile={null}
        filesTouched={[]}
      />,
    );
    // Click the failed command row; its summary appears.
    const btn = screen.getByText("npm run typecheck").closest("button");
    btn?.click();
    await waitFor(() =>
      expect(screen.getByText(/TS2322/)).toBeTruthy(),
    );
  });

  it("shows the Diff tab only when the turn touched files, and loads it", async () => {
    const { rerender } = render(
      <AgentWorkspace
        projectId={1}
        traceRows={rows}
        selectedFile={null}
        filesTouched={[]}
      />,
    );
    expect(screen.queryByText(/Diff/)).toBeNull();
    rerender(
      <AgentWorkspace
        projectId={1}
        traceRows={rows}
        selectedFile={null}
        filesTouched={["app/wallet.tsx"]}
      />,
    );
    const diffTab = screen.getByText(/Diff/);
    diffTab.click();
    await waitFor(() => expect(conversation.diff).toHaveBeenCalledWith(1));
  });

  it("switches to the file view when a file is selected", async () => {
    render(
      <AgentWorkspace
        projectId={1}
        traceRows={rows}
        selectedFile="app/wallet.tsx"
        fileContent="export default function Wallet() {}"
        filesTouched={[]}
      />,
    );
    // The tab switch runs in an effect; wait for the file view. Both the
    // tab and the editor header carry the name — one is enough.
    await waitFor(() =>
      expect(screen.getAllByText("wallet.tsx").length).toBeGreaterThan(0),
    );
  });

  it("renders the empty state honestly when no turn has run", () => {
    render(
      <AgentWorkspace
        projectId={1}
        traceRows={[]}
        selectedFile={null}
        filesTouched={[]}
      />,
    );
    expect(screen.getByText(/The agent is your editor/i)).toBeTruthy();
  });
});
