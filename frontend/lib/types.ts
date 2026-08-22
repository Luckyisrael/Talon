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
  // These match the API exactly (backend/internal/api/usage.go): the server
  // sends `device_minutes` and `device_limit`, not the `_used`/`_limit` pair
  // this type used to declare — which resolved to `undefined` at runtime.
  device_minutes: number;
  device_limit: number;
  tier: "free" | "pro";
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

// ── Profile & billing (Phase 9) ───────────────────────

/**
 * A user's paid state, exactly as GET /v1/me embeds it (store.Subscription).
 * Present only once a user has ever subscribed — a free user who never upgraded
 * has no subscription block at all. Written server-side only from verified
 * provider webhooks, never a client claim (rule 2.3), so this is the one honest
 * record of renewal date, card last-4, and a pending end-of-period cancel.
 */
export interface Subscription {
  user_id: number;
  provider_subscription_id: string;
  status: "active" | "canceled" | "past_due" | "inactive";
  tier: string;
  current_period_end?: string;
  cancel_at_period_end: boolean;
  payment_last4?: string;
  created_at: string;
  updated_at: string;
}

/** One entry in the profile's billing history: what happened and when. The raw
    provider payload stays in the server-side audit log, out of the UI. */
export interface BillingEventView {
  type: string;
  created_at: string;
}

/**
 * The authenticated user's full profile, from GET /v1/me. `tier` is the enforced
 * tier (users.tier) — the same value the quota layer reads — so the profile can
 * never claim a plan the system does not actually grant. It is a plain string,
 * not a "free"|"pro" union: higher tiers (team/enterprise) exist server-side and
 * a type that hid them would be lying.
 */
export interface MeProfile {
  id: number;
  email: string;
  display_name: string;
  tier: string;
  created_at: string;
  subscription?: Subscription;
  history: BillingEventView[];
}

/** Where to send the user to complete an upgrade, from POST /v1/billing/upgrade. */
export interface Checkout {
  session_id: string;
  url: string;
}

// ── Agent memory ──────────────────────────────────────

/**
 * One durable fact the agent chose to remember about a project, exactly as
 * GET /v1/projects/{id}/memory returns it. `entry_type` is one of the values
 * the write_memory tool allows — convention | decision | architecture | fact |
 * warning — and `metadata`, when present, names the file or area it applies to.
 */
export interface ProjectMemory {
  id: number;
  project_id: number;
  entry_type: string;
  content: string;
  metadata?: string;
  created_at: string;
}

/** An architecture decision record the agent has accepted for a project. */
export interface ArchitectureDecision {
  id: number;
  project_id: number;
  decision: string;
  context: string;
  options?: string;
  outcome: string;
  status: string;
  created_at: string;
  updated_at: string;
}

/** The full "what the agent knows" snapshot for a project. */
export interface MemorySnapshot {
  memories: ProjectMemory[];
  summary: string;
  decisions: ArchitectureDecision[];
}

// ── Agent Trace (§11) ─────────────────────────────────

export interface TraceRow {
  id: string;
  kind: "narration" | "thinking" | "tool" | "group";
  // Narration / thinking
  text?: string;
  streaming?: boolean;
  /** Thinking rows cap at a couple of lines; this flags a row the model is
      still writing so the cap holds until the stream ends. */
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
  /** Current build round, extracted from the phase event status. */
  round?: number;
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
