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
  android_package?: string;
  created_at: string;
}

/** One chat thread within a project. A project with no conversations yet
    (predates multi-chat) still works — the default thread is created lazily
    on first use, server-side. */
export interface Conversation {
  id: number;
  project_id: number;
  title: string;
  created_at: string;
  last_active_at: string;
}

export interface Usage {
  prompts_used: number;
  prompts_limit: number;
  // These match the API exactly (backend/internal/api/usage.go): the server
  // sends `device_minutes` and `device_limit`, not the `_used`/`_limit` pair
  // this type used to declare — which resolved to `undefined` at runtime.
  device_minutes: number;
  device_limit: number;
  cost_usd?: number;
  tier: "free" | "pro";
  // The enforced minimum interval between AI gateway calls (seconds, 0 when
  // unset). A long turn is mostly this — showing it keeps a paced build from
  // reading as a hang.
  pacing_seconds?: number;
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

/** The template is guidance, not a scaffold: the plan states which of its
    parts survive into this app, which are demo scaffolding to remove, and
    what gets written fresh. Every entry is a path under the workspace. */
export interface PlanStructure {
  /** Template files that stay — system pieces the app is built on
      (theme tokens, the root layout, the entry point). */
  keep: string[];
  /** Template files to delete, with the reason. Demo scaffolding the plan
      replaces. Each is a task during the build, ticked like a screen. */
  prune: string[];
  /** Files the plan writes fresh. */
  write: string[];
  /** The folder conventions the app follows — one line, for the build agent
      and for a human reading the plan later. */
  folders: string;
}

export interface PlanData {
  screens: PlanScreen[];
  features: string[];
  navigation: string;
  packages: string[];
  structure?: PlanStructure;
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
  /** Owning turn for history-drawer scroll mapping. Restored turns only;
   *  live turns have none until they persist. */
  promptId?: number;
  /** Files sent with this turn. Images render as thumbnails; docs inlined
   *  into the prompt text, so only their chips render. previewUrl rides
   *  optimistic (just-sent) bubbles; restored turns load bytes via the API. */
  attachments?: { id: number; name: string; mime: string; previewUrl?: string }[];
  /** H2: true when this user message was delivered to a running build as a
   * mid-turn steer rather than starting a new turn. Steers render dimmed with
   * a "steer" label — honest about not being a fresh prompt. */
  steer?: boolean;
  /** H2: set only when a steer delivery failed (e.g. the turn finished between
   * capture and send). Shows on the bubble so the user knows their words never
   * reached the build. */
  steerError?: string | null;
  /** True while this message waits for an in-flight plan call to finish — a
   * plan turn is one model request with nothing on the server that can absorb
   * a mid-call message, so the text is parked visibly instead of dropped or
   * rejected. The flag clears when the message goes out as its own turn. */
  queued?: boolean;
  /** The human's rating of this assistant message, from the feedback sidecar.
   * Undefined means unrated — the common case, rendered as no thumb pressed.
   * 1 is good, -1 is bad, 0 is a bare comment. */
  rating?: number;
}

export interface AuthResponse {
  token: string;
  refresh_token?: string;
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

// ── Premium git integration (W4) ──────────────────────

/**
 * The server's honest answer to "what can this user do with GitHub right now",
 * from GET /v1/git/status. `configured` is whether the OAuth app exists on the
 * server at all — false means the integration is dark and no button should
 * pretend otherwise. `paid` is the server-enforced tier gate. `connected` and
 * `login` describe the user's linked account. The three flags together decide
 * which surface the settings card shows; the UI never infers a capability the
 * server did not grant.
 */
export interface GitStatus {
  configured: boolean;
  paid: boolean;
  connected: boolean;
  login?: string;
  connected_at?: string;
}

/**
 * The repository a project pushes to, from GET /v1/projects/{id}/git/remote.
 * Null until the project has been pushed at least once.
 */
export interface GitRemote {
  id: number;
  project_id: number;
  provider: string;
  owner: string;
  repo: string;
  clone_url: string;
  html_url: string;
  default_branch: string;
  private: boolean;
  created_at: string;
  updated_at: string;
}

/** The outcome of a push, from POST /v1/projects/{id}/git/push. `commit_made` is
    false when the tree was already clean; `nothing_todo` is true when there was
    also nothing new to push. */
export interface GitPushResult {
  repo: string;
  html_url: string;
  branch: string;
  commit_made: boolean;
  nothing_todo: boolean;
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

/**
 * A decision the agent recorded mid-build — an architecture decision (write_adr)
 * or a durable memory (write_memory) — rendered inline in the trace as a card, so
 * the user sees the choice the agent committed to rather than a bare "wrote
 * memory" row. The fields mirror what the "What the agent knows" panel shows, so
 * the same record reads the same way in both places. `variant` selects which
 * fields are meaningful: an ADR uses `decision`/`outcome`; a memory uses
 * `category` (convention|decision|architecture|fact|warning), `content`, and the
 * optional `metadata` naming the file or area it applies to.
 */
export interface DecisionCard {
  variant: "adr" | "memory";
  decision?: string;
  outcome?: string;
  category?: string;
  content?: string;
  metadata?: string;
}

export interface TraceRow {
  id: string;
  kind: "narration" | "thinking" | "tool" | "group" | "decision" | "end" | "context";
  // Narration / thinking
  text?: string;
  streaming?: boolean;
  /** Thinking rows cap at a couple of lines; this flags a row the model is
      still writing so the cap holds until the stream ends. */
  // Tool
  tool?: string;
  label?: string;
  target?: string;
  status?: "running" | "done" | "fail" | "warning";
  summary?: string;
  /** Live progress hint parsed from streaming output (gradle task lines,
      percentages) — shown while running so long builds read at a glance. */
  progress?: string;
  // Decision (write_adr / write_memory rendered as a card)
  decision?: DecisionCard;
  // End — the terminal marker a turn writes when it settles (DSH's turn-tail).
  // Says how it ended; only the non-clean ones render, so a completed turn
  // adds no noise.
  ending?: "completed" | "failed" | "cancelled" | "interrupted" | "stalled";
  rounds?: number;
  // Context — machine guidance (a skill body, a verify-gate repair note) that
  // the model needed verbatim but the user never typed. Renders as a single
  // line; body is a click away. kind is "skill" | "verify".
  title?: string;
  body?: string;
  ctxKind?: string;
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

/** One item in the build plan (a screen or feature), tracked live from the
    `task_update` stream. Only tasks whose id starts with `plan:` are plan items;
    other task_updates are ephemeral tool activity, not part of this checklist. */
export interface PlanTask {
  id: string;
  label: string;
  status: "pending" | "in_progress" | "completed" | "failed";
  note?: string;
}

export interface StreamMessage {
  id: string;
  role: "assistant";
  traceRows: TraceRow[];
  /** Owning turn for history-drawer scroll mapping. Restored turns only. */
  promptId?: number;
  /** Live build-plan progress, keyed and upserted from `task_update` events whose
      id starts with `plan:`. Rendered as a compact checklist so the developer sees
      screens and features get checked off as the agent builds them. */
  planTasks?: PlanTask[];
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
  /** From the terminal `done` event — and, for restored turns, from the
      turn_end marker's verify payload (row 44). `verified`'s PRESENCE is the
      evidence flag: a legacy marker leaves it undefined and the footer must
      claim nothing about checks. */
  filesTouched?: string[];
  verified?: boolean;
  /** Gates that could not run (missing infra) — verified && this (or
      skippedGates non-empty) is a best-effort pass, rendered amber. */
  verifiedBestEffort?: boolean;
  /** The policy the sweep ran under ("strict" | "besteffort"); carried as a
      tooltip so an honest chip never becomes unexplained color. */
  verifyPolicy?: string;
  /** Verify stages that could not run (missing infra). */
  skippedGates?: string[];
  /** What the sweep says is left to build — shown as its own chip while it
      is non-empty, so "verified" never silently means "and nothing remains". */
  remainingTasks?: string[];
  /** Turn spend (router on only); restored turns predate/don't carry it. */
  costUsd?: number;
  routedProvider?: string;
  budgetStop?: string;
  rounds?: number;
  /** True for turns restored from the stored conversation rather than streamed. */
  restored?: boolean;
  /** The human's rating of this turn, from the feedback sidecar. Undefined is
   * unrated; 1 good, -1 bad, 0 a bare comment. */
  rating?: number;
  /**
   * Set by the `question` event when the agent paused on ask_user: the turn is
   * waiting for the user. The chat renders a question card from it. `promptId`
   * addresses POST /answer; `choices` are the suggested replies, if any;
   * `multiSelect` is true when the tool call declared the question as one that
   * admits several answers at once (the sheet then renders as checkboxes);
   * `answered` holds the user's reply once sent, which locks the card.
   */
  question?: {
    promptId: number;
    text: string;
    choices: string[];
    multiSelect: boolean;
    answered?: string;
  };
}

export interface ExportJob {
  id: number;
  project_id: number;
  status: "queued" | "running" | "done" | "failed";
  artifact_path?: string;
  manifest_path?: string;
  error?: string;
}

export interface TriageReport {
  id: number;
  project_id: number;
  decision: string;
  cutover_date: string;
  exposure_cents: number;
  notes: string;
}

export interface ProjectAsset {
  id: number;
  project_id: number;
  kind: "screenshot" | "icon" | "banner";
  path: string;
  width: number;
  height: number;
}
