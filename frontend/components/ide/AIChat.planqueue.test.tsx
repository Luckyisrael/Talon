import { beforeEach, describe, expect, it, vi } from "vitest";
import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import type { StreamEvent } from "@/lib/api";

// Conversation-history UX regressions, frontend half:
//
//  1. A message typed during a plan call is never silently dropped. A plan
//     turn is one in-flight model request with nothing on the server that can
//     absorb input, so the message parks as a labeled queued bubble — steer is
//     NOT attempted (there is no loop to steer) — and it goes out as its own
//     plan turn the moment the call returns, reusing its bubble.
//  2. The plan stream's phase frame drives the live strip ("Planning"), so
//     the turn is never dead air.
//  3. Plan task rows: a seeded-but-pending task renders NO trace row (the old
//     code painted every screen as a spinner the moment a build started); a
//     running task renders one, and its completed frame updates that same row
//     instead of duplicating it.

type StreamCapture = {
  prompt: string;
  mode: string;
  onEvent: (event: StreamEvent) => void;
};
const streams: StreamCapture[] = [];
const steer = vi.fn(async (...args: unknown[]) => {
  void args;
});

vi.mock("@/lib/api", async (importOriginal) => {
  const orig = await importOriginal<typeof import("@/lib/api")>();
  return {
    ...orig,
    conversation: {
      ...orig.conversation,
      get: vi.fn(async () => ({ turns: [], latest: null })),
    },
    streamPrompt: vi.fn(
      (
        _projectId: number,
        prompt: string,
        mode: "plan" | "build",
        onEvent: (event: StreamEvent) => void,
      ) => {
        streams.push({ prompt, mode, onEvent });
        return new AbortController();
      },
    ),
    prompts: { ...orig.prompts, steer },
  };
});

// Import after the mock so AIChat sees the mocked stream paths.
const { default: AIChat } = await import("./AIChat");

function typeAndSend(text: string) {
  const box = screen.getByLabelText("Chat prompt") as HTMLTextAreaElement;
  fireEvent.change(box, { target: { value: text } });
  fireEvent.keyDown(box, { key: "Enter" });
}

beforeEach(() => {
  streams.length = 0;
  steer.mockClear();
});

describe("plan stream liveness and mid-plan input", () => {
  it("shows the phase as live status and queues input instead of dropping it", async () => {
    render(<AIChat projectId={1} />);
    // Let the history restore land first: it replaces state wholesale, so
    // typing before it settles would be wiped the moment it resolves.
    expect(
      await screen.findByText("Describe the app you want."),
    ).toBeInTheDocument();
    typeAndSend("build a wallet app");
    await waitFor(() => expect(streams).toHaveLength(1));
    expect(streams[0].mode).toBe("plan");

    // The phase frame names the work: the live strip says Planning instead
    // of staring at dead air.
    act(() => {
      streams[0].onEvent({
        type: "phase",
        data: { phase: "PLAN", status: "planning", prompt_id: 77 },
      });
    });
    expect(await screen.findByText("Planning")).toBeInTheDocument();

    // Typed during the call: visible as queued, steer never attempted.
    typeAndSend("make it dark mode");
    expect(
      await screen.findByText("queued — sends when the plan lands"),
    ).toBeInTheDocument();
    expect(steer).not.toHaveBeenCalled();
    expect(streams).toHaveLength(1);

    // The call ends → the queued message goes out as its own plan turn,
    // reusing its bubble (one copy of the text, label gone).
    act(() => {
      streams[0].onEvent({ type: "done", data: { status: "success" } });
    });
    await waitFor(() => expect(streams).toHaveLength(2));
    expect(streams[1].prompt).toBe("make it dark mode");
    expect(streams[1].mode).toBe("plan");
    expect(steer).not.toHaveBeenCalled();
    await waitFor(() =>
      expect(
        screen.queryByText("queued — sends when the plan lands"),
      ).toBeNull(),
    );
    // Exactly one bubble carries the text: the delivery reused the queued
    // bubble instead of appending a second copy. (The history prompt-picker
    // button echoes every prompt — that is a separate surface.)
    const bubbles = Array.from(document.querySelectorAll(".bubble")).filter(
      (el) => el.textContent === "make it dark mode",
    );
    expect(bubbles).toHaveLength(1);
  });

  it("shows no trace row for a pending plan seed, then one row updated in place", async () => {
    render(<AIChat projectId={1} />);
    // Same restore-settle wait as above — its setMessages replaces state.
    expect(
      await screen.findByText("Describe the app you want."),
    ).toBeInTheDocument();
    typeAndSend("build it");
    await waitFor(() => expect(streams).toHaveLength(1));

    const task = {
      id: "plan:screen:home",
      description: "Screen: Home — Balance",
    };

    // Seeded pending: the checklist knows, the trace stays quiet — no wall
    // of spinners the moment the build starts.
    act(() => {
      streams[0].onEvent({
        type: "task_update",
        data: { ...task, status: "pending" },
      });
    });
    expect(screen.queryByText(task.description)).toBeNull();

    // It starts: the row appears.
    act(() => {
      streams[0].onEvent({
        type: "task_update",
        data: { ...task, status: "running" },
      });
    });
    expect(await screen.findByText(task.description)).toBeInTheDocument();

    // It finishes: the SAME row updates — not a second copy with a colliding
    // key.
    act(() => {
      streams[0].onEvent({
        type: "task_update",
        data: { ...task, status: "completed", result: "route written" },
      });
    });
    await waitFor(() =>
      expect(screen.getAllByText(task.description)).toHaveLength(1),
    );
  });
});
