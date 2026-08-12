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

/**
 * A device session, exactly as /v1/sessions returns it.
 *
 * This used to declare `status` and `created_at`, neither of which the API has
 * ever sent — the real fields are `state` and `started_at`, and the adb port is
 * how you address the device.
 */
export interface Session {
  id: number;
  project_id: number;
  state: string;
  started_at: string;
  adb_port?: number;
  container_id?: string;
  container_state?: string;
  metro_port?: number;
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

/** Token spend the provider reported for a turn, from the `usage` SSE event. */
export interface TurnUsage {
  input_tokens?: number;
  output_tokens?: number;
  cache_read_tokens?: number;
}

export interface StreamMessage {
  id: string;
  role: "assistant";
  traceRows: TraceRow[];
  text?: string;
  error?: string;
  isStreaming: boolean;
  /** The phase the turn is in, from the `phase` event. */
  phase?: string;
  /** Accumulated across every `usage` event in the turn. */
  usage?: TurnUsage;
  /** Set by the `plan` event when a planning turn returns structured output. */
  plan?: PlanData | null;
  planMessage?: string;
  /** From the terminal `done` event. */
  filesTouched?: string[];
  verified?: boolean;
  rounds?: number;
  /** True for turns restored from the stored conversation rather than streamed. */
  restored?: boolean;
}
