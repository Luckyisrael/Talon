import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";
import AgentTrace, {
  PlanProgress,
  getToolLabel,
  isReadOnlyTool,
} from "./AgentTrace";
import type { TraceRow } from "@/lib/types";

describe("getToolLabel — spec §11 verbs, never raw tool names", () => {
  const files = new Set<string>();
  it("names reads, creates, and edits distinctly", () => {
    expect(getToolLabel("read_file", { path: "a.tsx" }, files).label).toBe("Read");
    expect(getToolLabel("write_file", { path: "new.tsx" }, files).label).toBe("Created");
    expect(getToolLabel("write_file", { path: "seen.tsx" }, new Set(["seen.tsx"])).label).toBe(
      "Edited",
    );
    expect(getToolLabel("edit_file", { path: "a.tsx" }, files).label).toBe("Edited");
  });

  it("carries the target path for file tools", () => {
    expect(getToolLabel("read_file", { path: "app/x.tsx" }, files).target).toBe(
      "app/x.tsx",
    );
  });
});

describe("isReadOnlyTool — what may collapse into a group", () => {
  it("treats reads as collapsible and writes as always visible", () => {
    expect(isReadOnlyTool("read_file")).toBe(true);
    expect(isReadOnlyTool("grep_codebase")).toBe(true);
    expect(isReadOnlyTool("write_file")).toBe(false);
    expect(isReadOnlyTool("install_package")).toBe(false);
    expect(isReadOnlyTool("run_typecheck")).toBe(false);
  });
});

describe("AgentTrace rendering", () => {
  it("renders narration and thinking rows", () => {
    const rows: TraceRow[] = [
      { id: "n1", kind: "narration", text: "Planning the wallet screen" },
      { id: "t1", kind: "thinking", text: "considering MWA authorize against reauthorize" },
    ];
    render(<AgentTrace rows={rows} />);
    expect(screen.getByText("Planning the wallet screen")).toBeInTheDocument();
    expect(
      screen.getByText("considering MWA authorize against reauthorize"),
    ).toBeInTheDocument();
  });

  it("renders nothing for an empty trace", () => {
    const { container } = render(<AgentTrace rows={[]} />);
    expect(container).toBeEmptyDOMElement();
  });

  it("collapses a run of read-only calls into one group row", () => {
    const rows: TraceRow[] = [
      { id: "r1", kind: "tool", tool: "read_file", label: "Read", target: "a.tsx", status: "done" },
      { id: "r2", kind: "tool", tool: "read_file", label: "Read", target: "b.tsx", status: "done" },
      { id: "r3", kind: "tool", tool: "list_files", label: "Listed", target: "app", status: "done" },
    ];
    render(<AgentTrace rows={rows} />);
    // Grouped: individual targets collapse behind the group summary.
    expect(screen.queryByText("a.tsx")).not.toBeInTheDocument();
    expect(screen.getByText(/3 calls/i)).toBeInTheDocument();
  });

  it("keeps mutating calls individually visible", () => {
    const rows: TraceRow[] = [
      { id: "w1", kind: "tool", tool: "write_file", label: "Created", target: "app/x.tsx", status: "done" },
    ];
    render(<AgentTrace rows={rows} />);
    expect(screen.getByText("Created")).toBeInTheDocument();
    expect(screen.getByText("app/x.tsx")).toBeInTheDocument();
  });

  it("marks failed rows", () => {
    const rows: TraceRow[] = [
      { id: "f1", kind: "tool", tool: "run_typecheck", label: "Checked types", status: "fail", summary: "TS2322" },
    ];
    render(<AgentTrace rows={rows} />);
    expect(screen.getByText("TS2322")).toBeInTheDocument();
  });
});

describe("PlanProgress checklist", () => {
  it("renders nothing without tasks", () => {
    const { container } = render(<PlanProgress tasks={[]} />);
    expect(container).toBeEmptyDOMElement();
  });

  it("checks off completed tasks and counts them", () => {
    render(
      <PlanProgress
        tasks={[
          { id: "1", label: "Connect screen", status: "completed" },
          { id: "2", label: "Balance screen", status: "in_progress" },
          { id: "3", label: "Settings", status: "pending" },
        ]}
      />,
    );
    expect(screen.getByText("Connect screen")).toBeInTheDocument();
    expect(screen.getByText("1/3 done")).toBeInTheDocument();
  });
});
