import { create } from "zustand";
import type { FileEntry, FileNode, PlanState } from "./types";

function buildFileTree(entries: FileEntry[]): FileNode[] {
  const root: FileNode[] = [];

  for (const entry of entries) {
    const parts = entry.path.split("/").filter(Boolean);
    let current = root;

    for (let i = 0; i < parts.length; i++) {
      const part = parts[i];
      const isFile = i === parts.length - 1;

      const existing = current.find((n) => n.name === part);
      if (existing) {
        if (!isFile && existing.type === "file") {
          existing.type = "folder";
          existing.children = [];
        }
        if (existing.children) {
          current = existing.children;
        }
      } else {
        const node: FileNode = {
          name: part,
          type: isFile ? "file" : "folder",
          children: isFile ? undefined : [],
        };
        current.push(node);
        if (node.children) {
          current = node.children;
        }
      }
    }
  }

  return sortTree(root);
}

// Folders before files, alphabetical within each. The API returns paths in
// whatever order the walk produced, and an unsorted tree makes a project look
// like it has no structure.
function sortTree(nodes: FileNode[]): FileNode[] {
  for (const n of nodes) {
    if (n.children) n.children = sortTree(n.children);
  }
  return nodes.sort((a, b) => {
    if (a.type !== b.type) return a.type === "folder" ? -1 : 1;
    return a.name.localeCompare(b.name);
  });
}

function mergeFileTrees(existing: FileNode[], newNodes: FileNode[]): FileNode[] {
  const result = [...existing];

  for (const newNode of newNodes) {
    const idx = result.findIndex((n) => n.name === newNode.name);
    if (idx >= 0) {
      const existingNode = result[idx];
      if (
        newNode.type === "folder" &&
        existingNode.type === "folder" &&
        newNode.children &&
        existingNode.children
      ) {
        result[idx] = {
          ...existingNode,
          children: mergeFileTrees(existingNode.children, newNode.children),
        };
      } else if (newNode.type === "file") {
        result[idx] = newNode;
      }
    } else {
      result.push(newNode);
    }
  }

  return sortTree(result);
}

// removeNode deletes the node at `path` and nothing else.
//
// The previous version filtered out every node named after the *first* path
// segment at *every* depth, so removing "app/index.tsx" deleted the whole `app`
// folder — and, one level down, any sibling that happened to share a name with
// the segment below. A single file deletion could empty half the tree.
function removeNode(nodes: FileNode[], path: string): FileNode[] {
  const parts = path.split("/").filter(Boolean);
  if (parts.length === 0) return nodes;

  const [head, ...rest] = parts;
  const out: FileNode[] = [];

  for (const node of nodes) {
    if (node.name !== head) {
      out.push(node);
      continue;
    }
    if (rest.length === 0) {
      // This is the target. Drop it.
      continue;
    }
    if (node.type === "folder" && node.children) {
      const children = removeNode(node.children, rest.join("/"));
      // A folder only exists because something is in it — the API never
      // reports empty directories, so keeping a hollow one would show a
      // folder the project does not have.
      if (children.length > 0) out.push({ ...node, children });
      continue;
    }
    out.push(node);
  }

  return out;
}

interface IDEState {
  files: FileNode[];
  /** Content as last read from or written to the server. */
  fileContents: Record<string, string>;
  /** Edits made in the editor that have not been saved yet. */
  drafts: Record<string, string>;
  savingFiles: string[];
  selectedFile: string | null;
  currentPlan: PlanState | null;
  sessionId: number | null;
  /** How adb addresses the running device, e.g. "localhost:5555". */
  adbTarget: string | null;
  previewStatus: "idle" | "connecting" | "streaming" | "error";
  buildOutput: string;
  terminalOpen: boolean;
  commandPaletteOpen: boolean;
  /** Which pane the device rail shows: the live emulator, or the get-the-app
   *  QR. The QR is a setup-time action and the emulator is watch-time, so the
   *  rail tabs between them instead of stacking both. */
  deviceTab: "device" | "app";

  setFilesFromEntries: (entries: FileEntry[]) => void;
  addFiles: (entries: FileEntry[]) => void;
  removeFileByPath: (path: string) => void;
  selectFile: (path: string | null) => void;
  setDraft: (path: string, content: string) => void;
  discardDraft: (path: string) => void;
  markSaving: (path: string, saving: boolean) => void;
  commitDraft: (path: string) => void;
  setCurrentPlan: (plan: PlanState | null) => void;
  setSessionId: (id: number | null) => void;
  setAdbTarget: (target: string | null) => void;
  setPreviewStatus: (status: IDEState["previewStatus"]) => void;
  appendBuildOutput: (text: string) => void;
  clearBuildOutput: () => void;
  setTerminalOpen: (open: boolean) => void;
  setCommandPaletteOpen: (open: boolean) => void;
  setDeviceTab: (tab: IDEState["deviceTab"]) => void;
}

export const useIDEStore = create<IDEState>((set) => ({
  files: [],
  fileContents: {},
  drafts: {},
  savingFiles: [],
  selectedFile: null,
  currentPlan: null,
  sessionId: null,
  adbTarget: null,
  previewStatus: "idle",
  buildOutput: "",
  terminalOpen: false,
  commandPaletteOpen: false,
  deviceTab: "device",

  setFilesFromEntries: (entries) =>
    set({
      files: buildFileTree(entries),
      fileContents: Object.fromEntries(entries.map((e) => [e.path, e.content])),
    }),

  addFiles: (entries) =>
    set((state) => {
      const newTree = buildFileTree(entries);
      const newContents = { ...state.fileContents };
      const drafts = { ...state.drafts };
      for (const e of entries) {
        newContents[e.path] = e.content;
        // The agent just rewrote this file. An unsaved draft of the old version
        // would silently overwrite that work on the next save, so it goes.
        delete drafts[e.path];
      }
      return {
        files: mergeFileTrees(state.files, newTree),
        fileContents: newContents,
        drafts,
      };
    }),

  removeFileByPath: (path) =>
    set((state) => {
      const newContents = { ...state.fileContents };
      const drafts = { ...state.drafts };
      delete newContents[path];
      delete drafts[path];
      return {
        files: removeNode(state.files, path),
        fileContents: newContents,
        drafts,
        selectedFile: state.selectedFile === path ? null : state.selectedFile,
      };
    }),

  selectFile: (path) => set({ selectedFile: path }),

  setDraft: (path, content) =>
    set((state) => {
      // Typing back to the saved content is not a change. Dropping the draft
      // here is what makes the unsaved marker disappear on undo.
      if (state.fileContents[path] === content) {
        if (!(path in state.drafts)) return state;
        const drafts = { ...state.drafts };
        delete drafts[path];
        return { drafts };
      }
      return { drafts: { ...state.drafts, [path]: content } };
    }),

  discardDraft: (path) =>
    set((state) => {
      if (!(path in state.drafts)) return state;
      const drafts = { ...state.drafts };
      delete drafts[path];
      return { drafts };
    }),

  markSaving: (path, saving) =>
    set((state) => ({
      savingFiles: saving
        ? state.savingFiles.includes(path)
          ? state.savingFiles
          : [...state.savingFiles, path]
        : state.savingFiles.filter((p) => p !== path),
    })),

  commitDraft: (path) =>
    set((state) => {
      const draft = state.drafts[path];
      if (draft === undefined) return state;
      const drafts = { ...state.drafts };
      delete drafts[path];
      return {
        drafts,
        fileContents: { ...state.fileContents, [path]: draft },
      };
    }),

  setCurrentPlan: (plan) => set({ currentPlan: plan }),

  setSessionId: (id) => set({ sessionId: id }),

  setAdbTarget: (target) => set({ adbTarget: target }),

  setPreviewStatus: (status) => set({ previewStatus: status }),

  appendBuildOutput: (text) =>
    set((state) => ({ buildOutput: state.buildOutput ? `${state.buildOutput}\n${text}` : text })),

  clearBuildOutput: () => set({ buildOutput: "" }),

  setTerminalOpen: (open) => set({ terminalOpen: open }),

  setCommandPaletteOpen: (open) => set({ commandPaletteOpen: open }),

  setDeviceTab: (tab) => set({ deviceTab: tab }),
}));
