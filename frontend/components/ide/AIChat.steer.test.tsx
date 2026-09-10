import { describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";

// The steer bubble is the honesty surface of H2: a message delivered to a
// running build must look different from a fresh prompt — dimmed, labeled
// "steer — not a new turn" — and a failed delivery must say so instead of
// vanishing. Live steers carry the label; restored steers keep the marker
// the loop baked into the stored text ("[user steered the running build]").

const restoredTurns: {
  role: "user" | "assistant";
  blocks: { type: string; text?: string }[];
  at: string;
}[] = [];

vi.mock("@/lib/api", async (importOriginal) => {
  const orig = await importOriginal<typeof import("@/lib/api")>();
  return {
    ...orig,
    conversation: {
      ...orig.conversation,
      get: async () => ({
        turns: restoredTurns,
        latest: null,
      }),
    },
  };
});

// Import after the mock so AIChat sees the mocked history path.
const { default: AIChat } = await import("./AIChat");

function userTurn(text: string) {
  restoredTurns.length = 0;
  restoredTurns.push({
    role: "user",
    blocks: [{ type: "text", text }],
    at: new Date().toISOString(),
  });
}

describe("AIChat steer bubbles (H2)", () => {
  it("keeps the steer marker on a restored steer turn", async () => {
    userTurn("[user steered the running build] actually use reanimated-v4");
    render(<AIChat projectId={1} />);
    // The turn renders at least once (as the bubble; the trace may echo it).
    const matches = await screen.findAllByText(
      "[user steered the running build] actually use reanimated-v4",
    );
    expect(matches.length).toBeGreaterThan(0);
  });

  it("renders a plain prompt without any steer marker", async () => {
    userTurn("build the wallet screen");
    render(<AIChat projectId={1} />);
    expect(
      (await screen.findAllByText("build the wallet screen")).length,
    ).toBeGreaterThan(0);
    expect(screen.queryByText(/steered the running build/i)).not.toBeInTheDocument();
  });

  it("renders the composer while a turn is not streaming", () => {
    userTurn("build the wallet screen");
    render(<AIChat projectId={1} />);
    expect(screen.getByLabelText("Chat prompt")).toBeEnabled();
  });
});
