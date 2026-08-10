export interface User {
  id: number;
  email: string;
  tier: "free" | "pro";
  created_at: string;
}

export interface Project {
  id: number;
  user_id: number;
  name: string;
  status: string;
  plan?: string;
  created_at: string;
}

export interface Usage {
  prompts_used: number;
  prompts_limit: number;
  device_minutes_used: number;
  device_minutes_limit: number;
  tier: string;
}

export interface FileEntry {
  path: string;
  content: string;
  scaffold?: boolean;
}

export interface FileNode {
  name: string;
  type: "file" | "folder";
  children?: FileNode[];
}

export interface PlanScreen {
  name: string;
  description: string;
}

export interface PlanData {
  screens: PlanScreen[];
  features: string[];
  navigation: string;
  packages: string[];
}

export interface PlanState {
  message: string;
  plan: PlanData;
}

export interface PromptResponse {
  id: number;
  mode: string;
  response: {
    message: string;
    files?: FileEntry[];
    plan?: PlanData;
    build_result?: unknown;
    commands?: string[];
  };
}

export interface Session {
  id: number;
  project_id: number;
  status: string;
  created_at: string;
}

export interface ChatMessage {
  id: string;
  role: "user" | "assistant";
  content: string;
  mode?: "plan" | "build";
  plan?: PlanData | null;
  files?: FileEntry[];
  buildResult?: unknown;
  timestamp: number;
}

export interface AuthResponse {
  token: string;
  user: User;
}

// ── Agent Trace (§11) ─────────────────────────────────

export interface TraceRow {
  id: string;
  kind: "narration" | "tool" | "group";
  // Narration
  text?: string;
  streaming?: boolean;
  // Tool
  tool?: string;
  label?: string;
  target?: string;
  status?: "running" | "done" | "fail";
  summary?: string;
  // Group
  children?: TraceRow[];
  collapsed?: boolean;
}

export interface StreamMessage {
  id: string;
  role: "assistant";
  traceRows: TraceRow[];
  text?: string;
  error?: string;
  isStreaming: boolean;
}
