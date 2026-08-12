"use client";

import Editor, { type Monaco } from "@monaco-editor/react";
import { useCallback, useEffect, useRef, useState } from "react";
import { projects } from "@/lib/api";
import { useIDEStore } from "@/lib/store";

interface CodeEditorProps {
  projectId: number;
  filePath: string;
  /** The content as last read from or written to the server. */
  content: string;
}

function getLanguage(filePath: string): string {
  const name = filePath.split("/").pop() ?? "";
  const ext = name.includes(".") ? name.split(".").pop()!.toLowerCase() : "";
  switch (ext) {
    case "ts":
      return "typescript";
    case "tsx":
      return "typescript";
    case "js":
    case "cjs":
    case "mjs":
    case "jsx":
      return "javascript";
    case "json":
      return "json";
    case "css":
      return "css";
    case "md":
    case "mdx":
      return "markdown";
    case "html":
      return "html";
    case "yml":
    case "yaml":
      return "yaml";
    case "sh":
      return "shell";
    case "kt":
      return "kotlin";
    case "java":
      return "java";
    case "xml":
    case "plist":
      return "xml";
    case "gradle":
      return "groovy";
    case "toml":
      return "ini";
    default:
      return "plaintext";
  }
}

// Monaco's own vs-dark is a different blue-black from everything around it, so
// the editor used to look like a screenshot pasted into the page.
const THEME = "bench";

function defineTheme(monaco: Monaco) {
  monaco.editor.defineTheme(THEME, {
    base: "vs-dark",
    inherit: true,
    rules: [
      { token: "", foreground: "e6ebf2" },
      { token: "comment", foreground: "5b667a", fontStyle: "italic" },
      { token: "string", foreground: "14f195" },
      { token: "number", foreground: "ffb020" },
      { token: "keyword", foreground: "b47bff" },
      { token: "type", foreground: "8fd6ff" },
      { token: "type.identifier", foreground: "8fd6ff" },
      { token: "delimiter", foreground: "8794a6" },
      { token: "tag", foreground: "b47bff" },
      { token: "attribute.name", foreground: "8fd6ff" },
      { token: "attribute.value", foreground: "14f195" },
    ],
    colors: {
      "editor.background": "#0b0e13",
      "editor.foreground": "#e6ebf2",
      "editorLineNumber.foreground": "#303c4d",
      "editorLineNumber.activeForeground": "#8794a6",
      "editorCursor.foreground": "#14f195",
      "editor.selectionBackground": "#232c39",
      "editor.inactiveSelectionBackground": "#181f29",
      "editor.lineHighlightBackground": "#12171f",
      "editorIndentGuide.background1": "#181f29",
      "editorIndentGuide.activeBackground1": "#303c4d",
      "editorWidget.background": "#12171f",
      "editorWidget.border": "#232c39",
      "editorSuggestWidget.background": "#12171f",
      "editorSuggestWidget.selectedBackground": "#232c39",
      "editorGutter.background": "#0b0e13",
      "scrollbarSlider.background": "#232c3999",
      "scrollbarSlider.hoverBackground": "#303c4dcc",
      "scrollbarSlider.activeBackground": "#303c4d",
    },
  });
}

export default function CodeEditor({ projectId, filePath, content }: CodeEditorProps) {
  const draft = useIDEStore((s) => s.drafts[filePath]);
  const saving = useIDEStore((s) => s.savingFiles.includes(filePath));
  const setDraft = useIDEStore((s) => s.setDraft);
  const discardDraft = useIDEStore((s) => s.discardDraft);
  const markSaving = useIDEStore((s) => s.markSaving);
  const commitDraft = useIDEStore((s) => s.commitDraft);

  // Both are tagged with the file they describe, so switching files drops them
  // without an effect writing null back — a save error belonging to another file
  // must never appear in this file's header, not even for one render.
  const [saved, setSaved] = useState<{ path: string; at: number } | null>(null);
  const [failure, setFailure] = useState<{ path: string; message: string } | null>(null);
  const language = getLanguage(filePath);
  const dirty = draft !== undefined;
  const value = draft ?? content;

  const savedAt = saved?.path === filePath ? saved.at : null;
  const error = failure?.path === filePath ? failure.message : null;

  const save = useCallback(async () => {
    if (!dirty || saving) return;

    const body = useIDEStore.getState().drafts[filePath];
    if (body === undefined) return;

    setFailure(null);
    markSaving(filePath, true);
    try {
      await projects.writeFiles(projectId, [{ path: filePath, content: body }]);
      commitDraft(filePath);
      setSaved({ path: filePath, at: Date.now() });
    } catch (err) {
      // The draft stays in the store — losing someone's edit because the
      // network blinked is not an acceptable outcome of pressing Save.
      setFailure({
        path: filePath,
        message: err instanceof Error ? err.message : "Could not save this file.",
      });
    } finally {
      markSaving(filePath, false);
    }
  }, [commitDraft, markSaving, projectId, filePath, dirty, saving]);

  // Monaco registers its keybinding once per mount, so it has to reach the
  // current `save` through a ref rather than capturing the first one. Assigned
  // in an effect: mutating a ref while rendering is not safe under concurrent
  // rendering, which is what makes it a lint error rather than a style note.
  const saveRef = useRef(save);
  useEffect(() => {
    saveRef.current = save;
  }, [save]);

  useEffect(() => {
    if (savedAt === null) return;
    const t = window.setTimeout(() => setSaved(null), 2000);
    return () => window.clearTimeout(t);
  }, [savedAt]);

  // Ctrl/Cmd+S, at the document level so it works whether or not the editor has
  // focus — and so the browser's own save dialog never appears.
  useEffect(() => {
    function onKeyDown(e: KeyboardEvent) {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "s") {
        e.preventDefault();
        void saveRef.current();
      }
    }
    document.addEventListener("keydown", onKeyDown);
    return () => document.removeEventListener("keydown", onKeyDown);
  }, []);

  const dir = filePath.includes("/") ? filePath.slice(0, filePath.lastIndexOf("/") + 1) : "";
  const base = filePath.slice(dir.length);

  return (
    <div className="flex h-full min-h-0 flex-col bg-ink">
      <header className="bay-head justify-between gap-3">
        {/* The path is the machine's name for this thing, so it is set in mono,
            with the filename carrying the weight. */}
        <p className="min-w-0 truncate font-mono text-tiny" title={filePath}>
          <span className="text-dust">{dir}</span>
          <span className="text-paper">{base}</span>
          {dirty && (
            <span className="ml-1.5 text-filament" title="Unsaved changes">
              ●
            </span>
          )}
        </p>

        <div className="flex flex-shrink-0 items-center gap-1.5">
          {error && (
            <span className="max-w-[16rem] truncate text-tiny text-fault" title={error}>
              {error}
            </span>
          )}
          {!error && savedAt !== null && !dirty && <span className="gauge">Saved</span>}
          {!error && savedAt === null && !dirty && <span className="gauge">{language}</span>}

          {dirty && (
            <>
              <button
                type="button"
                onClick={() => discardDraft(filePath)}
                disabled={saving}
                className="btn btn-ghost text-tiny"
              >
                Revert
              </button>
              <button
                type="button"
                onClick={() => void save()}
                disabled={saving}
                className="btn btn-go text-tiny"
              >
                {saving ? "Saving" : "Save"}
              </button>
            </>
          )}
        </div>
      </header>

      <div className="min-h-0 flex-1">
        <Editor
          // Monaco keeps one model per path; keying on it means switching files
          // does not carry the previous file's undo stack over.
          path={filePath}
          language={language}
          value={value}
          theme={THEME}
          beforeMount={defineTheme}
          onMount={(editor, monaco) => {
            editor.addCommand(monaco.KeyMod.CtrlCmd | monaco.KeyCode.KeyS, () => {
              void saveRef.current();
            });
          }}
          onChange={(next) => setDraft(filePath, next ?? "")}
          options={{
            minimap: { enabled: false },
            fontSize: 13,
            lineHeight: 20,
            fontFamily: "var(--font-plex-mono), ui-monospace, monospace",
            fontLigatures: false,
            lineNumbers: "on",
            scrollBeyondLastLine: false,
            padding: { top: 10, bottom: 24 },
            renderLineHighlight: "line",
            overviewRulerLanes: 0,
            hideCursorInOverviewRuler: true,
            overviewRulerBorder: false,
            smoothScrolling: true,
            cursorBlinking: "smooth",
            tabSize: 2,
            wordWrap: "off",
            bracketPairColorization: { enabled: false },
            guides: { indentation: true },
            scrollbar: {
              vertical: "auto",
              horizontal: "auto",
              verticalScrollbarSize: 9,
              horizontalScrollbarSize: 9,
            },
          }}
          loading={
            <div className="flex h-full items-center justify-center">
              <p className="gauge">Loading the editor</p>
            </div>
          }
        />
      </div>
    </div>
  );
}
