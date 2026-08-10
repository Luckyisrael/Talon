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

  return root;
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

  return result;
}

function removeNode(nodes: FileNode[], path: string): FileNode[] {
  const parts = path.split("/").filter(Boolean);
  return nodes
    .filter((n) => n.name !== parts[0])
    .map((n) => {
      if (n.type === "folder" && n.children && parts.length > 1 && n.name === parts[0]) {
        return { ...n, children: removeNode(n.children, parts.slice(1).join("/")) };
      }
      return n;
    });
}

interface IDEState {
  files: FileNode[];
  fileContents: Record<string, string>;
  selectedFile: string | null;
  currentPlan: PlanState | null;
  sessionId: number | null;
  previewStatus: "idle" | "connecting" | "streaming" | "error";
  buildOutput: string;
  terminalOpen: boolean;
  commandPaletteOpen: boolean;

  setFilesFromEntries: (entries: FileEntry[]) => void;
  addFiles: (entries: FileEntry[]) => void;
  removeFileByPath: (path: string) => void;
  selectFile: (path: string | null) => void;
  setCurrentPlan: (plan: PlanState | null) => void;
  setSessionId: (id: number | null) => void;
  setPreviewStatus: (status: IDEState["previewStatus"]) => void;
  appendBuildOutput: (text: string) => void;
  setTerminalOpen: (open: boolean) => void;
  setCommandPaletteOpen: (open: boolean) => void;
}

export const useIDEStore = create<IDEState>((set) => ({
  files: [],
  fileContents: {},
  selectedFile: null,
  currentPlan: null,
  sessionId: null,
  previewStatus: "idle",
  buildOutput: "",
  terminalOpen: false,
  commandPaletteOpen: false,

  setFilesFromEntries: (entries) =>
    set({
      files: buildFileTree(entries),
      fileContents: Object.fromEntries(entries.map((e) => [e.path, e.content])),
    }),

  addFiles: (entries) =>
    set((state) => {
      const newTree = buildFileTree(entries);
      const newContents = { ...state.fileContents };
      for (const e of entries) {
        newContents[e.path] = e.content;
      }
      return {
        files: mergeFileTrees(state.files, newTree),
        fileContents: newContents,
      };
    }),

  removeFileByPath: (path) =>
    set((state) => {
      const newContents = { ...state.fileContents };
      delete newContents[path];
      return {
        files: removeNode(state.files, path),
        fileContents: newContents,
        selectedFile: state.selectedFile === path ? null : state.selectedFile,
      };
    }),

  selectFile: (path) => set({ selectedFile: path }),

  setCurrentPlan: (plan) => set({ currentPlan: plan }),

  setSessionId: (id) => set({ sessionId: id }),

  setPreviewStatus: (status) => set({ previewStatus: status }),

  appendBuildOutput: (text) =>
    set((state) => ({ buildOutput: state.buildOutput ? `${state.buildOutput}\n${text}` : text })),

  setTerminalOpen: (open) => set({ terminalOpen: open }),

  setCommandPaletteOpen: (open) => set({ commandPaletteOpen: open }),
}));
