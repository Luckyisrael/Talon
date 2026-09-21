import { describe, expect, it } from "vitest";
import { classifyFile, describeSize, stripInlineDocs } from "./attachments";

function file(name: string, type: string, size: number): File {
  const bytes = new Uint8Array(size);
  return new File([bytes], name, { type });
}

describe("classifyFile", () => {
  it("accepts png, jpeg, webp by mime", () => {
    for (const [name, type] of [
      ["shot.png", "image/png"],
      ["photo.jpg", "image/jpeg"],
      ["pic.webp", "image/webp"],
    ]) {
      expect(classifyFile(file(name, type, 100))).toEqual({ kind: "image" });
    }
  });

  it("accepts images by extension when the mime is missing", () => {
    expect(classifyFile(file("shot.png", "", 100))).toEqual({ kind: "image" });
  });

  it("accepts markdown docs", () => {
    expect(classifyFile(file("NOTES.md", "text/markdown", 100))).toEqual({ kind: "doc" });
    expect(classifyFile(file("notes.md", "", 100))).toEqual({ kind: "doc" });
  });

  it("rejects executables, archives, and text files", () => {
    for (const [name, type] of [
      ["run.exe", "application/octet-stream"],
      ["app.zip", "application/zip"],
      ["notes.txt", "text/plain"],
    ]) {
      const verdict = classifyFile(file(name, type, 100));
      expect("error" in verdict, `${name} should be rejected`).toBe(true);
    }
  });

  it("rejects empty and oversized files with the reason", () => {
    expect(classifyFile(file("e.png", "image/png", 0))).toEqual({
      error: expect.stringContaining("empty"),
    });
    const big = classifyFile(file("big.png", "image/png", 5 * 1024 * 1024 + 1));
    expect("error" in big && big.error).toMatch(/5MB/);
  });
});

describe("describeSize", () => {
  it("formats bytes, kb, and mb", () => {
    expect(describeSize(512)).toBe("512b");
    expect(describeSize(2048)).toBe("2.0kb");
    expect(describeSize(3 * 1024 * 1024)).toBe("3.0mb");
  });
});

describe("stripInlineDocs", () => {
  it("leaves plain prompts untouched", () => {
    expect(stripInlineDocs("wire the button")).toBe("wire the button");
  });

  it("collapses doc sections and the images marker to the prompt", () => {
    const stored =
      "wire the button\n\n[Attached file notes.md]\n# tokens\n\nlime only\n\n[Attached images: shot.png]";
    expect(stripInlineDocs(stored)).toBe("wire the button");
  });

  it("collapses multiple docs", () => {
    const stored =
      "do it\n\n[Attached file a.md]\nAAA\n\n[Attached file b.md]\nBBB";
    expect(stripInlineDocs(stored)).toBe("do it");
  });

  it("leaves prose that merely mentions attachments alone", () => {
    expect(stripInlineDocs("attach the file manually")).toBe("attach the file manually");
  });
});
