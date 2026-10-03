/**
 * Row 45 — wiring the verify evidence into the web client (TEST_RULES-first).
 *
 * The gaps this file pins, each as one subtest (rule 1):
 *
 * F1. A restored build turn must rebuild its footer from the turn_end
 *     marker's `verify` payload (row 44): amber best-effort chip with the
 *     skipped gates, policy in the tooltip, files/rounds parts. Pre-fix
 *     TurnFooter returned null for every restored entry — reload wiped
 *     all evidence.              [restored_marker_evidence_rebuilds_footer]
 *
 * F2. A legacy marker (no verify payload — the backend predates row 44)
 *     must render only what it knows (rounds) and NEVER claim anything
 *     about checks: no "verified", no "checks did not pass". Evidence
 *     presence is the signal.       [legacy_marker_claims_nothing]
 *
 * F3. The live `done` frame must map verify_policy, verified_best_effort,
 *     remaining_tasks and cost_usd onto the footer, and adopt the frame's
 *     `message` as narration when the turn streamed no words (restarted
 *     stream / tools-only rounds). Pre-fix: every one of those was
 *     dropped on the floor.                      [live_done_maps_verify_fields]
 *
 * F4. The workspace transcript must render `end` and `context` trace rows
 *     (AgentTrace has EndRow/ContextRow; TranscriptRow fell through to
 *     default:null — a failed turn's tail and every skill/repair note
 *     vanished in the center pane).
 *                            [workspace_renders_end_and_context_rows]
 *
 * F5. A live `stalled` done must read as terminal: an end row saying so,
 *     no Resume affordance, distinct from the generic DONE it used to
 *     collapse into.                          [live_stalled_is_terminal]
 *
 * F6. The Topbar badge rule in one pure function: only a passing sweep
 *     (verified | best-effort) claims "Build verified · ago"; refused and
 *     the absent row (404 → null) claim nothing.
 *                                        [verify_state_badge_rule]
 *
 * F7. `stalled` joins the LatestPrompt status union as a terminal state
 *     and is deliberately NOT resumable — isResumable stays false and no
 *     Resume banner appears. Polarity: the whitelist already excluded it,
 *     so this guards the union addition against reopening the affordance.
 *                                            [stalled_is_not_resumable]
 *
 * F8. A reload must not forget what the last turn changed: the changed-
 *     files panel is seeded from the restored marker's files_touched; a
 *     mid-flight turn (paused/waiting — no marker by design) reads the
 *     prompt row via GET /v1/prompts/{id} instead.
 *                                        [restore_seeds_changed_files]
 *
 * F9. The seed must REACH the dock panel. IDELayout's handleFilesTouched —
 *     the receiver of onFilesTouched — has to write liveTouchedFiles: the
 *     panel renders diff ∩ filesTouched, and pre-fix the handler only
 *     refetched the tree and opened the file (both of which DID happen),
 *     so a restored turn's panel showed every diff file instead of the
 *     turn's set. Found by the row-45 live browser E2E; F8 alone could not
 *     see it because it pins only the callback contract, not the wiring
 *     on the other side of it.
 *                                        [seed_reaches_changed_files_panel]
 *
 * F10. A reload DURING an active turn must seed the panel too. The live
 *      E2E reloaded mid-build: the final assistant entry has no marker
 *      (turn_end never landed) and `running` was not in the whitelist
 *      that consults the prompt row, so the panel fell back to every
 *      diff file. The row's union (row 42) knows what the legs wrote.
 *                            [running_turn_seeds_from_prompt_union]
 *
 * F11. Same for an ABORTED turn: the E2E's own reload cancelled the
 *      stream (context canceled → prompts row `cancelled`), and
 *      `cancelled` was equally outside the whitelist — a refresh after an
 *      interrupted build forgot its files the same way. The marker-less
 *      family (running/paused/waiting/cancelled/failed/legacy-completed)
 *      all reads the prompt row; an empty union seeds nothing (the
 *      all-diff view is the honest superset) rather than misattributing
 *      an older turn's files.
 *                           [aborted_turn_seeds_from_prompt_union]
 *
 * TEST_RULES rule 2 — artifacts: e2e/artifacts/verify-evidence-frontend
 * .red.log / .green.log (the `npm test` transcript for this file).
 *
 * RED (pre-fix, from frontend/):
 *
 *	npm test -- verify-evidence
 *
 * GREEN gate: same command passes; lint + next build after.
 *
 * API surface is mocked at the module boundary (the house pattern — see
 * AIChat.steer.test.tsx); no network, deterministic streams driven by
 * capturing the streamPrompt callback.
 */
import { beforeEach, describe, expect, it, vi } from "vitest";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import type { ConversationTurn, LatestPrompt, StreamEvent } from "@/lib/api";

// IconRail (inside IDELayout, rendered by F9) navigates with the app router;
// there is no router mounted under vitest.
vi.mock("next/navigation", () => ({
  useRouter: () => ({
    push: vi.fn(),
    replace: vi.fn(),
    back: vi.fn(),
    prefetch: vi.fn(),
    refresh: vi.fn(),
  }),
  usePathname: () => "/project/4",
  useSearchParams: () => new URLSearchParams(),
}));

// ── Mock state, mutated per subtest (house pattern) ──────────────────

const restoredTurns: ConversationTurn[] = [];
let latest: LatestPrompt | null = null;
let promptRow: { files_touched?: string[] } | null = null;
let onStream: ((event: StreamEvent) => void) | null = null;
const streamPromptMock = vi.fn(
  (_projectId: number, _prompt: string, _mode: string, onEvent: (e: StreamEvent) => void) => {
    onStream = onEvent;
    return { abort: vi.fn() };
  },
);
const promptsGetMock = vi.fn(async () => promptRow);
let diffBody: { diff: string; note?: string } = { diff: "", note: "no baseline" };

vi.mock("@/lib/api", async (importOriginal) => {
  const orig = await importOriginal<typeof import("@/lib/api")>();
  return {
    ...orig,
    conversation: {
      ...orig.conversation,
      get: async () => ({ turns: restoredTurns, latest }),
      diff: async () => diffBody,
    },
    prompts: {
      ...orig.prompts,
      get: promptsGetMock,
    },
    streamPrompt: streamPromptMock,
    // IDELayout (F9) also mounts git/verify/files fetches — keep them off
    // the network while staying type-shaped.
    git: {
      ...orig.git,
      getProjectStatus: async () => ({
        branch: "main",
        dirty: 0,
        ahead: 0,
        behind: 0,
        last_commit: "abc1234",
      }),
    },
    // list(): IconRail's rail fetches the project list on mount — keep it off
    // the network (its real BASE points at the LAN in .env.local).
    projects: {
      ...orig.projects,
      getFiles: async () => [],
      list: async () => [],
    },
    verifyState: {
      ...orig.verifyState,
      get: async () => {
        throw new Error("no verify-state row — F9 does not need one");
      },
    },
    // Row 45 adds verifyBadge to the real module. Defining the key here keeps
    // the mock's export set stable across the fix boundary: pre-fix it is
    // undefined (F6 fails as a TypeError), post-fix it resolves to the real
    // rule through orig.
    verifyBadge: orig.verifyBadge,
  };
});

// Import after the mock so AIChat sees the mocked history/stream paths.
const { default: AIChat } = await import("./AIChat");
const { default: AgentWorkspace } = await import("./AgentWorkspace");
const { default: IDELayout } = await import("./IDELayout");
const { useIDEStore } = await import("@/lib/store");
const { verifyBadge, isResumable } = await import("@/lib/api");

// ── Builders ─────────────────────────────────────────────────────────

const NOW = new Date().toISOString();

function userTurn(text: string): ConversationTurn {
  return { role: "user", blocks: [{ type: "text", text }], at: NOW };
}

function assistantTurn(
  blocks: NonNullable<ConversationTurn["blocks"]>,
): ConversationTurn {
  return { role: "assistant", blocks, at: NOW };
}

/** Drive one scripted turn through the composer: type + Send, then hand the
 *  captured stream callback whatever frames the case needs. */
async function sendAndStream(frames: StreamEvent[]) {
  // Let the mount-time restore effect settle first: its setMessages lands as
  // a microtask and would otherwise wipe the entry send() is about to add
  // (production types long after restore completes; this is harness timing).
  await act(async () => {});
  fireEvent.change(screen.getByLabelText("Chat prompt"), {
    target: { value: "build the wallet" },
  });
  fireEvent.click(screen.getByLabelText("Send"));
  await waitFor(() => expect(streamPromptMock).toHaveBeenCalled());
  const emit = onStream;
  if (!emit) throw new Error("streamPrompt never captured a callback");
  await act(async () => {
    for (const frame of frames) emit(frame);
  });
}

// Module-level state is shared across subtests: reset before each one.
beforeEach(() => {
  restoredTurns.length = 0;
  latest = null;
  promptRow = null;
  diffBody = { diff: "", note: "no baseline" };
  onStream = null;
  streamPromptMock.mockClear();
  promptsGetMock.mockClear();
});

describe("row 45 — verify evidence wired into the web client", () => {
  it("F1: restores footer evidence from the turn_end marker", async () => {
    restoredTurns.push(
      userTurn("add the ledger"),
      assistantTurn([
        { type: "text", text: "Ledger written and checked." },
        {
          type: "turn_end",
          status: "completed",
          rounds: 2,
          verify: {
            files_touched: ["features/ledger.json"],
            verified: true,
            verified_best_effort: true,
            verify_policy: "strict",
            skipped_gates: ["boot", "vision"],
          },
        },
      ]),
    );
    render(<AIChat projectId={1} />);
    // The chip carries the gates it did not run, in text (not hover-only).
    await screen.findByText(/verified · skipped: boot, vision/);
    expect(screen.getByTitle(/verify policy: strict/)).toBeTruthy();
    expect(screen.getByText("1 file")).toBeTruthy();
    expect(screen.getByText("2 rounds")).toBeTruthy();
    expect(screen.queryByText(/checks did not pass/)).toBeNull();
  });

  it("F2: a legacy marker claims nothing about checks", async () => {
    restoredTurns.push(
      userTurn("ship it"),
      assistantTurn([
        { type: "text", text: "Turn finished." },
        { type: "turn_end", status: "completed", rounds: 1 },
      ]),
    );
    render(<AIChat projectId={1} />);
    // What the marker KNOWS still renders — the footer is not simply hidden.
    await screen.findByText("1 round");
    expect(screen.queryByText(/verified/)).toBeNull();
    expect(screen.queryByText(/checks did not pass/)).toBeNull();
  });

  it("F3: maps live done verify fields and adopts the final message", async () => {
    render(<AIChat projectId={1} />);
    await sendAndStream([
      {
        type: "done",
        data: {
          status: "success",
          files_touched: ["app/wallet.tsx"],
          verified: false,
          verify_policy: "strict",
          skipped_gates: ["typecheck"],
          remaining_tasks: ["Run unit tests"],
          rounds: 3,
          cost_usd: 0.02,
          message: "All finished cleanly.",
        },
      },
    ]);
    await screen.findByText(/checks did not pass/);
    expect(screen.getByTitle(/verify policy: strict/)).toBeTruthy();
    expect(screen.getByText("1 left")).toBeTruthy();
    expect(screen.getByText("$0.020")).toBeTruthy();
    // No text frame streamed — the frame's message becomes the narration.
    expect(await screen.findByText(/All finished cleanly\./)).toBeTruthy();
    expect(screen.getByText("1 file")).toBeTruthy();
    expect(screen.getByText("3 rounds")).toBeTruthy();
  });

  it("L54c: a live done with no verdict and nothing to claim renders no footer", async () => {
    // The user-reported shape: a turn that completed with nothing to
    // change. Pre-fix the footer still mounted on `rounds` alone —
    // "1 round" under a red "checks did not pass" the backend never
    // made. Absence of a verdict + no files/remaining/cost = no row.
    render(<AIChat projectId={1} />);
    await sendAndStream([
      {
        type: "done",
        data: {
          status: "success",
          rounds: 1,
          files_touched: [],
          message: "Nothing needed changing.",
        },
      },
    ]);
    // The frame landed — its message became the narration.
    await screen.findByText(/Nothing needed changing\./);
    expect(screen.queryByText(/1 round/)).toBeNull();
    expect(screen.queryByText(/checks did not pass/)).toBeNull();
  });

  it("L54c2: a restored marker whose payload carries no verdict claims nothing", async () => {
    // The new backend writes an empty-but-present payload ("finished,
    // nothing to verify") — no verdict, no work, so the footer stays
    // down. F2's payload-LESS legacy marker still reports its rounds.
    restoredTurns.push(
      userTurn("just checking"),
      assistantTurn([
        { type: "text", text: "Nothing needed changing." },
        {
          type: "turn_end",
          status: "completed",
          rounds: 1,
          verify: { files_touched: [], verify_policy: "", skipped_gates: [] },
        },
      ]),
    );
    render(<AIChat projectId={1} />);
    await screen.findByText(/Nothing needed changing\./);
    expect(screen.queryByText(/1 round/)).toBeNull();
    expect(screen.queryByText(/checks did not pass/)).toBeNull();
  });

  it("F4: the workspace transcript renders end and context rows", async () => {
    render(
      <AgentWorkspace
        projectId={1}
        traceRows={[
          { id: "r1", kind: "narration", text: "Working on the ledger." },
          { id: "r2", kind: "end", ending: "stalled", rounds: 2 },
          {
            id: "r3",
            kind: "context",
            title: "Applied skill: forms",
            body: "Always gate form submits behind the schema validator.",
            ctxKind: "skill",
          },
        ]}
        selectedFile={null}
        filesTouched={[]}
      />,
    );
    await screen.findByText(/Turn stalled · after 2 rounds/);
    // Context: the one-line title shows, the body stays a click away.
    expect(screen.getByText("Applied skill: forms")).toBeTruthy();
    expect(screen.queryByText(/schema validator/)).toBeNull();
    fireEvent.click(screen.getByText("Applied skill: forms"));
    expect(await screen.findByText(/schema validator/)).toBeTruthy();
  });

  it("F5: a live stalled turn reads as terminal", async () => {
    render(<AIChat projectId={1} />);
    await sendAndStream([
      {
        type: "done",
        data: {
          status: "stalled",
          rounds: 2,
          files_touched: [],
          verified: false,
          verify_policy: "",
          skipped_gates: [],
          message: "",
        },
      },
    ]);
    expect(await screen.findByText(/Turn stalled/)).toBeTruthy();
    expect(screen.getByText(/after 2 rounds/)).toBeTruthy();
    // Terminal by design: no resume affordance for a stalled turn.
    expect(screen.queryByText("Resume")).toBeNull();
  });

  it("F6: only a passing sweep claims the Topbar badge", async () => {
    expect(verifyBadge(null)).toBeNull();
    expect(
      verifyBadge({
        project_id: 1,
        status: "refused",
        policy: "strict",
        skipped: ["typecheck"],
        remaining: [],
        reason: "typecheck never ran",
        updated_at: NOW,
      }),
    ).toBeNull();
    const passing = verifyBadge({
      project_id: 1,
      status: "verified",
      policy: "strict",
      skipped: [],
      remaining: [],
      reason: "",
      updated_at: NOW,
    });
    expect(passing).toMatch(/just now|m ago/);
    expect(
      verifyBadge({
        project_id: 1,
        status: "best-effort",
        policy: "strict",
        skipped: ["boot"],
        remaining: [],
        reason: "",
        updated_at: NOW,
      }),
    ).toBeTruthy();
  });

  it("F7: stalled is terminal — not resumable, no banner", async () => {
    expect(
      isResumable({ id: 7, mode: "build", status: "stalled", round: 2 }),
    ).toBe(false);

    latest = { id: 7, mode: "build", status: "stalled", round: 2 };
    restoredTurns.push(
      userTurn("keep going"),
      assistantTurn([{ type: "text", text: "It stalled out." }]),
      assistantTurn([
        { type: "turn_end", status: "stalled", rounds: 2 },
      ]),
    );
    render(<AIChat projectId={1} />);
    await screen.findByText(/It stalled out\./);
    expect(screen.queryByText("Resume")).toBeNull();
    // The restored tail still says the turn stalled.
    expect(await screen.findByText(/Turn stalled/)).toBeTruthy();
  });

  it("F8: restores seed the changed-files panel", async () => {
    latest = null;
    restoredTurns.push(
      userTurn("add the ledger"),
      assistantTurn([
        { type: "text", text: "Done." },
        {
          type: "turn_end",
          status: "completed",
          rounds: 2,
          verify: {
            files_touched: ["features/ledger.json"],
            verified: true,
            verified_best_effort: false,
            verify_policy: "strict",
            skipped_gates: [],
          },
        },
      ]),
    );
    const seeded = vi.fn();
    render(<AIChat projectId={1} onFilesTouched={seeded} />);
    await waitFor(() =>
      expect(seeded).toHaveBeenCalledWith(["features/ledger.json"]),
    );
  });

  it("F8b: a paused mid-flight turn reads its files from the prompt row", async () => {
    latest = { id: 9, mode: "build", status: "paused", round: 2 };
    promptRow = { files_touched: ["app/new.tsx"] };
    restoredTurns.push(
      userTurn("keep building"),
      assistantTurn([
        { type: "tool_use", id: "c1", name: "write_file", input: { path: "app/new.tsx" } },
      ]),
    );
    const seeded = vi.fn();
    render(<AIChat projectId={1} onFilesTouched={seeded} />);
    await waitFor(() =>
      expect(promptsGetMock).toHaveBeenCalledWith(9),
    );
    await waitFor(() =>
      expect(seeded).toHaveBeenCalledWith(["app/new.tsx"]),
    );
  });

  it("F10: a reload during an ACTIVE turn reads the union from the prompt row", async () => {
    // Live-E2E twin: refresh mid-build. No turn_end yet, so the final
    // assistant entry carries no marker — the row's union is the only
    // thing that knows what the legs wrote.
    latest = { id: 10, mode: "build", status: "running", round: 3 };
    promptRow = { files_touched: ["ROW45_LIVE.md"] };
    restoredTurns.push(
      userTurn("make ROW45_LIVE.md"),
      assistantTurn([
        { type: "tool_use", id: "c1", name: "write_file", input: { path: "ROW45_LIVE.md" } },
      ]),
    );
    const seeded = vi.fn();
    render(<AIChat projectId={1} onFilesTouched={seeded} />);
    await waitFor(() => expect(promptsGetMock).toHaveBeenCalledWith(10));
    await waitFor(() =>
      expect(seeded).toHaveBeenCalledWith(["ROW45_LIVE.md"]),
    );
  });

  it("F11: an ABORTED turn (cancelled) seeds its files from the prompt row", async () => {
    // The exact state the live E2E landed in: the script's reload cut the
    // SSE stream, the backend marked the row `cancelled`, and the marker
    // never carried verify — yet the legs had already written the file.
    latest = { id: 11, mode: "build", status: "cancelled", round: 8 };
    promptRow = { files_touched: ["ROW45_LIVE.md"] };
    restoredTurns.push(
      userTurn("make ROW45_LIVE.md"),
      assistantTurn([
        { type: "text", text: "Writing the file." },
        { type: "tool_use", id: "c1", name: "write_file", input: { path: "ROW45_LIVE.md" } },
        { type: "turn_end", status: "cancelled", rounds: 8, error: "context canceled" },
      ]),
    );
    const seeded = vi.fn();
    render(<AIChat projectId={1} onFilesTouched={seeded} />);
    await waitFor(() => expect(promptsGetMock).toHaveBeenCalledWith(11));
    await waitFor(() =>
      expect(seeded).toHaveBeenCalledWith(["ROW45_LIVE.md"]),
    );
  });

  // IDELayout mounts restore + files + git fetches; give this integration
  // test headroom — the full suite's parallel workers routinely push it
  // past the 5s default (it needs ~2.4s alone).
  it("F9: the restore seed reaches the dock's changed-files panel", async () => {
    // The seam the row-45 live browser E2E exposed: seedRestoredFiles fires
    // onFilesTouched with the marker's files, and IDELayout's handler DID
    // refetch the tree and open the file — but it never wrote
    // liveTouchedFiles, the state the dock panel renders from
    // (filesTouched={liveTouchedFiles}). A restored turn's panel therefore
    // showed every diff file instead of the turn's own set.
    restoredTurns.push(
      userTurn("create the readme"),
      assistantTurn([
        { type: "text", text: "Created." },
        {
          type: "turn_end",
          status: "completed",
          rounds: 1,
          verify: {
            files_touched: ["ROW45_LIVE.md"],
            verified: true,
            verified_best_effort: true,
            verify_policy: "strict",
            skipped_gates: ["boot"],
          },
        },
      ]),
    );
    // The diff carries a DIFFERENT file (a provision-time modification of a
    // tracked one). Rows are diff ∩ touched: with the seed landing, neither
    // that row nor the all-diff view may render — the panel stands in the
    // touched set's pending state instead.
    diffBody = {
      diff:
        'diff --git a/app.json b/app.json\n+++ b/app.json\n@@ -1 +1 @@\n-"name": "template"\n+"name": "Row45"\n',
    };
    useIDEStore.setState({ selectedFile: null });
    render(
      <IDELayout projectId={4} projectName="Row45 Live E2E" onExit={() => {}} />,
    );
    // The seed ran: the marker's file opened in the center editor...
    await screen.findByRole("button", { name: "← Back to chat" });
    expect(useIDEStore.getState().selectedFile).toBe("ROW45_LIVE.md");
    // ...and the dock panel must see the seeded set, not the whole diff.
    fireEvent.click(screen.getByRole("button", { name: "Files" }));
    await screen.findByRole("heading", { name: "Changed files" });
    expect(screen.queryByText("app.json")).toBeNull();
    await screen.findByText(/not in the diff yet/);
  }, 15_000);
});
