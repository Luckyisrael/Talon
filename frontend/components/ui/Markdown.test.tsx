/**
 * Markdown media: tables, images, and task lists render; unsafe destinations
 * and pipe-bearing prose do not become tables by accident.
 */
import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";
import Markdown from "./Markdown";

describe("Markdown tables", () => {
  it("renders a header row and body rows", () => {
    render(
      <Markdown>{`| Check | Result |
| --- | --- |
| types | pass |
| device | pass |`}</Markdown>,
    );
    expect(screen.getByText("Check").tagName).toBe("TH");
    expect(screen.getByText("Result").tagName).toBe("TH");
    const cells = screen.getAllByText("pass");
    expect(cells.length).toBeGreaterThan(0);
    for (const cell of cells) expect(cell.tagName).toBe("TD");
    expect(screen.getAllByRole("row")).toHaveLength(3);
  });

  it("accepts alignment markers and outer-pipe-less rows", () => {
    render(
      <Markdown>{`a | b
:--- | ---:
x | y`}</Markdown>,
    );
    expect(screen.getByText("a").tagName).toBe("TH");
    expect(screen.getByText("y").tagName).toBe("TD");
  });

  it("pads ragged rows to the header width", () => {
    const { container } = render(
      <Markdown>{`| a | b |
| --- | --- |
| only |`}</Markdown>,
    );
    expect(container.querySelectorAll("tbody td")).toHaveLength(2);
  });

  it("leaves pipe-bearing prose without a delimiter row alone", () => {
    render(<Markdown>{"run a | b in the shell"}</Markdown>);
    expect(screen.getByText("run a | b in the shell").tagName).toBe("P");
    expect(screen.queryByRole("table")).toBeNull();
  });

  it("renders inline code inside cells literally", () => {
    render(
      <Markdown>{`| token | value |
| --- | --- |
| \`x\` | 1 |`}</Markdown>,
    );
    expect(screen.getByText("x").tagName).toBe("CODE");
  });
});

describe("Markdown images", () => {
  it("renders a lone image line as a linked figure", () => {
    render(<Markdown>{"![Device check](https://example.com/shot.png)"}</Markdown>);
    const img = screen.getByAltText("Device check");
    expect(img.tagName).toBe("IMG");
    expect(img).toHaveAttribute("src", "https://example.com/shot.png");
    expect(img.closest("a")).toHaveAttribute("href", "https://example.com/shot.png");
    expect(img.closest("a")).toHaveAttribute("target", "_blank");
  });

  it("renders inline images inside paragraphs", () => {
    render(<Markdown>{"see this ![alt](https://example.com/i.png) here"}</Markdown>);
    expect(screen.getByAltText("alt")).toBeInTheDocument();
  });

  it("refuses javascript: destinations, showing alt text only", () => {
    render(<Markdown>{"![evil](javascript:alert(1))"}</Markdown>);
    expect(screen.queryByRole("img")).toBeNull();
    expect(screen.getByText("evil")).toBeInTheDocument();
    expect(screen.queryByRole("link")).toBeNull();
  });

  it("keeps image syntax literal inside code spans", () => {
    render(<Markdown>{"`![a](https://example.com/i.png)`"}</Markdown>);
    expect(screen.queryByRole("img")).toBeNull();
  });
});

describe("Markdown task lists", () => {
  it("renders checked and unchecked items", () => {
    render(<Markdown>{"- [x] refresh invalidates query\n- [ ] haptic on release"}</Markdown>);
    const boxes = screen.getAllByRole("checkbox");
    expect(boxes).toHaveLength(2);
    expect(boxes[0]).toBeChecked();
    expect(boxes[1]).not.toBeChecked();
    expect(screen.getByText("haptic on release")).toBeInTheDocument();
  });

  it("leaves ordinary bullets untouched", () => {
    render(<Markdown>{"- plain item"}</Markdown>);
    expect(screen.queryByRole("checkbox")).toBeNull();
    expect(screen.getByText("plain item")).toBeInTheDocument();
  });
});

describe("Markdown regressions", () => {
  it("still renders headings, code fences, and links", () => {
    render(
      <Markdown>{"### Done\n\n```tsx\nconst x = 1;\n```\n\n[docs](https://example.com)"}</Markdown>,
    );
    expect(screen.getByText("Done").tagName).toBe("H3");
    expect(screen.getByText("const x = 1;")).toBeInTheDocument();
    expect(screen.getByText("docs")).toHaveAttribute("href", "https://example.com");
  });
});
