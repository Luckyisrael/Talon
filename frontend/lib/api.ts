import { clearToken, getRefreshToken, getToken, setRefreshToken, setToken } from "./auth";
import type {
  ArchitectureDecision,
  AuthResponse,
  Checkout,
  Conversation,
  ExportJob,
  FileEntry,
  GitPushResult,
  GitRemote,
  GitStatus,
  MeProfile,
  Project,
  ProjectAsset,
  PlanTask,
  ProjectMemory,
  PromptResponse,
  Session,
  TriageReport,
  Usage,
} from "./types";

// The API base. In a browser the default is the same host the page is on —
// the common deployment shape (both behind one host) and the LAN dev shape
// (phone or second machine browsing to the dev box) both work without any
// env file. NEXT_PUBLIC_API_URL overrides for split-origin deployments.
const BASE =
  process.env.NEXT_PUBLIC_API_URL ||
  (typeof window !== "undefined" ? `${window.location.protocol}//${window.location.hostname}:8080` : "http://localhost:8080");

/** A Metro event off the project's bundler tap (DV1): bundle progress, hot
 *  reloads, and redbox errors for the preview badge. Mirrors metro.Event on
 *  the backend — kind is the vocabulary, progress 0 when the line carried
 *  no percentage. */
export interface MetroEvent {
  kind:
    | "connected"
    | "bundle_start"
    | "bundle_progress"
    | "bundle_done"
    | "reloaded"
    | "error"
    | "unavailable";
  text: string;
  progress: number;
}

/** Open the project's metro SSE stream. Returns a close handle; the caller
 *  owns the lifetime (browser close, device stop). */
/** The phone-test surface (DV2): the deep link a phone's dev client opens,
 *  and the dev-client APK download for first-time phones. */
export const testInfo = {
  async get(
    projectId: number,
  ): Promise<{
    available: boolean;
    reason?: string;
    deep_link?: string;
    metro_url?: string;
    dev_client_url?: string;
  }> {
    return request(`/v1/projects/${projectId}/test-info`);
  },
};

export function openMetroStream(
  projectId: number,
  token: string,
  onEvent: (ev: MetroEvent) => void,
): () => void {
  const es = new EventSource(
    `${BASE}/v1/projects/${projectId}/metro?token=${encodeURIComponent(token)}`,
  );
  es.addEventListener("metro", (e) => {
    try {
      onEvent(JSON.parse((e as MessageEvent).data) as MetroEvent);
    } catch {
      // a malformed frame is dropped; the badge keeps its last known state
    }
  });
  return () => es.close();
}

export class APIError extends Error {
  constructor(
    public status: number,
    message: string,
  ) {
    super(message);
    this.name = "APIError";
  }
}

// A token that has expired makes every request fail with a 401, and the app
// used to sit there showing empty panels and generic errors forever. Dropping
// the dead token and returning to sign-in is the only recoverable response.
function handleExpiredSession() {
  if (typeof window === "undefined") return;
  clearToken();
  const here = window.location.pathname;
  if (here === "/login" || here === "/auth/callback") return;
  // A hard navigation on purpose, and not reachable by the alternatives the lint
  // rule suggests: this is a module-level function, so there is no `useRouter`,
  // and `redirect()` is server-side. A client-side push would also *keep* the
  // Zustand store — leaving the signed-out user's projects and unsaved drafts in
  // memory across the boundary. Reloading the document is what discards them.
  // eslint-disable-next-line @next/next/no-location-assign-relative-destination
  window.location.assign(`/login?next=${encodeURIComponent(here)}`);
}

async function request<T>(path: string, options: RequestInit = {}): Promise<T> {
  const token = getToken();
  const headers: Record<string, string> = {
    "Content-Type": "application/json",
    ...(options.headers as Record<string, string>),
  };
  if (token) {
    headers.Authorization = `Bearer ${token}`;
  }

  const res = await fetch(`${BASE}${path}`, { ...options, headers });

  // An expired access token fails once with a 401 — if a refresh token
  // exists and this is not itself an auth call, rotate silently and retry
  // once before treating the session as dead. Only a dead session signs
  // out; any other status returns as-is, even after a refresh.
  if (res.status === 401 && token && !path.startsWith("/v1/auth/")) {
    let fresh: string | null = null;
    try {
      fresh = await refreshAccessToken();
    } catch {
      fresh = null;
    }
    if (fresh) {
      headers.Authorization = `Bearer ${fresh}`;
      const retry = await fetch(`${BASE}${path}`, { ...options, headers });
      if (retry.ok || retry.status !== 401) return parseResponse<T>(retry);
    }
    handleExpiredSession();
    const body = await res.json().catch(() => ({ error: `HTTP ${res.status}` }));
    throw new APIError(res.status, body.error || `Request failed (${res.status})`);
  }

  return parseResponse<T>(res);
}

async function parseResponse<T>(res: Response): Promise<T> {
  if (!res.ok) {
    const body = await res.json().catch(() => ({ error: `HTTP ${res.status}` }));
    throw new APIError(res.status, body.error || `Request failed (${res.status})`);
  }

  // 204 and empty 200 bodies are valid answers — PUT/DELETE return them. The
  // previous unconditional res.json() threw "Unexpected end of JSON input" on
  // every successful write, so saving a file looked like it had failed.
  if (res.status === 204) return undefined as T;
  const text = await res.text();
  if (!text) return undefined as T;
  return JSON.parse(text) as T;
}

// refreshAccessToken spends the stored refresh token for a new pair.
// Single-flight: concurrent 401s share one rotation instead of racing it
// (the loser of a rotation race fails closed server-side).
let refreshInflight: Promise<string> | null = null;

export function refreshAccessToken(): Promise<string> {
  if (!refreshInflight) {
    refreshInflight = (async () => {
      const refresh = getRefreshToken();
      if (!refresh) throw new Error("no refresh token");
      const res = await fetch(`${BASE}/v1/auth/refresh`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ refresh_token: refresh }),
      });
      if (!res.ok) throw new Error(`refresh failed (${res.status})`);
      const body = await res.json();
      setToken(body.token);
      setRefreshToken(body.refresh_token);
      return body.token as string;
    })().finally(() => {
      refreshInflight = null;
    });
  }
  return refreshInflight;
}

// ── Auth ──────────────────────────────────────────────
// Sign-in is OAuth (GitHub + Google); email/password is gone. The browser is
// sent to the provider's consent screen and lands on /auth/callback with a
// single-use code, which this client exchanges for the JWT — the token never
// rides a URL.

export const auth = {
  /** Which providers are configured on this server — the honest list. */
  providers() {
    return request<{ providers: { id: string; name: string }[] }>("/v1/auth/providers");
  },

  /** The single-use code from the callback becomes the session JWT. */
  exchange(code: string) {
    return request<AuthResponse>("/v1/auth/exchange", {
      method: "POST",
      body: JSON.stringify({ code }),
    });
  },

  /** Spends the refresh token for a new pair. Used by the request layer,
   *  not called directly by screens. */
  refresh() {
    return refreshAccessToken();
  },

  /** Ends one device session server-side. Best-effort: sign-out clears
   *  local tokens regardless. */
  async revoke(): Promise<void> {
    const refresh = getRefreshToken();
    if (!refresh) return;
    try {
      await fetch(`${BASE}/v1/auth/logout`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ refresh_token: refresh }),
      });
    } catch {
      // Offline sign-out still signs out.
    }
  },

  /** Where the browser goes to start a provider flow. Server derives origin. */
  startURL(provider: string, redirect: string) {
    return `${BASE}/v1/auth/${provider}/start?redirect=${encodeURIComponent(redirect)}`;
  },
};

// ── Projects ──────────────────────────────────────────

export const projects = {
  list() {
    return request<Project[]>("/v1/projects");
  },

  get(id: number) {
    return request<Project>(`/v1/projects/${id}`);
  },

  create(name: string) {
    return request<Project>("/v1/projects", {
      method: "POST",
      body: JSON.stringify({ name }),
    });
  },

  getFiles(id: number, includeScaffold = true) {
    const query = includeScaffold ? "" : "?includeScaffold=false";
    return request<FileEntry[]>(`/v1/projects/${id}/files${query}`);
  },

  writeFiles(id: number, files: { path: string; content: string }[]) {
    return request<void>(`/v1/projects/${id}/files`, {
      method: "PUT",
      body: JSON.stringify({ files }),
    });
  },

  deleteFile(id: number, filePath: string) {
    return request<void>(
      `/v1/projects/${id}/files?path=${encodeURIComponent(filePath)}`,
      { method: "DELETE" },
    );
  },

  delete(id: number) {
    return request<void>(`/v1/projects/${id}`, { method: "DELETE" });
  },
};

// ── Conversation ──────────────────────────────────────

/**
 * The turn_end marker's verify evidence — the durable twin of the build
 * stream's done frame (row 44): same outcome fields, written at the same
 * instant, so a reload restores exactly what streamed. ABSENT (not empty)
 * on legacy markers and crash paths: absence means "no evidence recorded",
 * which the client must render as a claim-free footer rather than as a
 * failed check.
 */
export interface TurnVerifyEvidence {
  files_touched?: string[];
  verified?: boolean;
  verified_best_effort?: boolean;
  verify_policy?: string;
  skipped_gates?: string[];
}

/** One stored turn, in the provider-neutral block shape the backend persists. */
export interface ConversationBlock {
  type:
    | "text"
    | "thinking"
    | "tool_use"
    | "tool_result"
    | "plan"
    | "image"
    | "doc"
    | "turn_end"
    | "context";
  text?: string;
  id?: string;
  name?: string;
  input?: Record<string, unknown>;
  tool_use_id?: string;
  content?: string;
  is_error?: boolean;
  // turn_end: the terminal marker — completed | failed | cancelled | interrupted
  // | stalled — written once when the turn settles. error carries the provider
  // message on a failure, rounds how far it got.
  status?: string;
  error?: string;
  rounds?: number;
  // turn_end's verify evidence (row 44): the durable twin of the done frame,
  // so a reload restores what the turn changed and how it was checked exactly
  // as it streamed. ABSENT on legacy markers and crash paths (no outcome) —
  // the client must then render "no evidence", never a claim; present-and-
  // empty means "finished, nothing to verify".
  verify?: TurnVerifyEvidence;
  // context: machine guidance the orchestrator injected (a skill body, or the
  // repair note after a verify gate fails). title is the one line the transcript
  // shows; body stays collapsed. kind is "skill" | "verify".
  title?: string;
  body?: string;
  kind?: string;
  // Image blocks reference a prompt attachment by id — the bytes live on the
  // server and resolve at send time, so the transcript never holds pixels.
  // Steer deliveries persist as marker blocks carrying the redirect text.
  // Serializers drop them (the model consumed the words live); the chat
  // restores the dimmed redirect bubble from the marker.
  attachment_id?: number;
  mime?: string;
  // Plan-mode turns carry the structured plan here so the chat can rebuild its
  // plan card on reload. Present only on assistant "plan" blocks.
  plan?: {
    screens?: { name: string; description: string }[];
    features?: string[];
    navigation?: string;
    packages?: string[];
  } | null;
}

export interface ConversationTurn {
  role: "user" | "assistant";
  blocks: ConversationBlock[];
  at: string;
  /** Owning turn and message sequence: scroll mapping for the history
   *  drawer. Absent only on legacy rows predating the fields. */
  prompt_id?: number;
  seq?: number;
}

/** One row of the turn index: metadata only, never prompt text. */
export interface TurnIndexEntry {
  id: number;
  mode: string;
  title: string;
  status: string;
  round: number;
  created_at: string;
}

/**
 * The most recent prompt's run state, returned alongside the transcript so the
 * chat can tell a clean finish from an interrupted turn on load. A turn left
 * `running` (the server went down mid-turn), `cancelled` (Stop, or a dropped
 * connection), `failed`, or `paused` (it reached its round budget while still
 * working) can be resumed; `completed` and `abandoned` cannot. `waiting` is a
 * turn paused on an ask_user question — it resumes by answering the question
 * (POST /answer), not by the resume button, so isResumable omits it.
 * `stalled` is terminal and deliberately not resumable either: the turn
 * settled without finishing and the server persisted it as-is, so a fresh
 * prompt — not a replay — is the way forward.
 */
export interface LatestPrompt {
  id: number;
  mode: "plan" | "build";
  status:
    | "running"
    | "waiting"
    | "paused"
    | "completed"
    | "cancelled"
    | "failed"
    | "abandoned"
    | "stalled";
  round: number;
}

/** A resumable turn is a build turn whose last run did not finish cleanly.
 *  `stalled` is absent on purpose — terminal, never resumable (row 45). */
export function isResumable(latest: LatestPrompt | null): boolean {
  return (
    latest !== null &&
    latest.mode === "build" &&
    (latest.status === "running" ||
      latest.status === "cancelled" ||
      latest.status === "failed" ||
      latest.status === "paused")
  );
}

export const conversation = {
  async get(
    projectId: number,
    opts?: { beforeSeq?: number; limit?: number; conversationId?: number },
  ): Promise<{
    turns: ConversationTurn[];
    latest: LatestPrompt | null;
    latest_seq?: number;
    usage?: { in: number; out: number; cache_read: number };
    planTasks?: PlanTask[];
  }> {
    const params = new URLSearchParams();
    if (opts?.beforeSeq) params.set("before_seq", String(opts.beforeSeq));
    if (opts?.limit) params.set("limit", String(opts.limit));
    if (opts?.conversationId)
      params.set("conversation_id", String(opts.conversationId));
    const query = params.toString();
    const res = await request<{
      turns: ConversationTurn[] | null;
      latest?: LatestPrompt;
      latest_seq?: number;
      usage?: { in: number; out: number; cache_read: number };
      planTasks?: {
        id: string;
        description: string;
        status: string;
        result?: string;
      }[];
    }>(`/v1/projects/${projectId}/conversation${query ? `?${query}` : ""}`);
    return {
      turns: res?.turns ?? [],
      latest: res?.latest ?? null,
      latest_seq: res?.latest_seq,
      usage: res?.usage,
      // The durable plan backlog: every plan: task's persisted status, so the
      // checklist survives a reload instead of waiting for the next SSE frame.
      planTasks: res?.planTasks?.map((t) => ({
        id: t.id,
        label: t.description || t.id,
        status:
          t.status === "completed"
            ? "completed"
            : t.status === "in_progress" || t.status === "running"
              ? "in_progress"
              : t.status === "failed"
                ? "failed"
                : "pending",
        note: t.result || undefined,
      })),
    };
  },

  // §7 diff surface: the workspace's working-tree changes against the
  // provision-time template baseline. Empty diff + note = no baseline.
  diff(projectId: number) {
    return request<{ diff: string; note?: string }>(
      `/v1/projects/${projectId}/diff`,
    );
  },

  /** Turn index, newest first, cursor-paged. Metadata only. */
  promptsIndex(projectId: number, opts?: { before?: number; limit?: number }) {
    const params = new URLSearchParams();
    if (opts?.before) params.set("before", String(opts.before));
    if (opts?.limit) params.set("limit", String(opts.limit));
    const query = params.toString();
    return request<TurnIndexEntry[]>(
      `/v1/projects/${projectId}/prompts${query ? `?${query}` : ""}`,
    );
  },

  /** Substring search over stored turns, newest first. Whole turns with
   *  prompt ids, so results scroll somewhere instead of stranding. */
  search(projectId: number, q: string, limit = 20) {
    const params = new URLSearchParams({ q, limit: String(limit) });
    return request<{ turns: ConversationTurn[] }>(
      `/v1/projects/${projectId}/conversation/search?${params.toString()}`,
    );
  },

  clear(projectId: number) {
    return request<{ status: string }>(`/v1/projects/${projectId}/conversation`, {
      method: "DELETE",
    });
  },

  // Rate one assistant message. The rating lives outside the session log, so
  // the model never replays a human judgement of its own output; a re-vote
  // replaces the prior rating in place.
  rate(projectId: number, messageId: string, rating: number, comment?: string) {
    return request<{ messageId: string; rating: number }>(
      `/v1/projects/${projectId}/feedback`,
      {
        method: "POST",
        body: JSON.stringify({ messageId, rating, comment: comment ?? "" }),
      },
    );
  },

  // Every rated message in the project, oldest first. The tuning view: what
  // the person actually thought of the output here. Unrated is an empty list.
  feedback(projectId: number) {
    return request<{ items: { messageId: string; rating: number; comment: string; at: string }[] }>(
      `/v1/projects/${projectId}/feedback`,
    );
  },
};

// ── Chat threads: multiple chats per project ───────────

export const conversations = {
  list(projectId: number) {
    return request<Conversation[]>(`/v1/projects/${projectId}/conversations`);
  },

  create(projectId: number, title?: string) {
    return request<Conversation>(`/v1/projects/${projectId}/conversations`, {
      method: "POST",
      body: JSON.stringify({ title: title ?? "" }),
    });
  },
};

// ── Checkpoints ───────────────────────────────────────

export interface Checkpoint {
  id: number;
  project_id: number;
  prompt_id: number;
  label: string;
  files: { path: string; content: string; existed: boolean }[];
  created_at: string;
}

export const checkpoints = {
  async list(projectId: number) {
    const res = await request<{ checkpoints: Checkpoint[] | null }>(
      `/v1/projects/${projectId}/checkpoints`,
    );
    return res?.checkpoints ?? [];
  },

  restore(projectId: number, checkpointId: number) {
    return request<{ status: string; restored: string[] }>(
      `/v1/projects/${projectId}/checkpoints/${checkpointId}/restore`,
      { method: "POST" },
    );
  },
};

// ── Prompts ───────────────────────────────────────────

export const prompts = {
  send(projectId: number, prompt: string, mode: "plan" | "build") {
    return request<PromptResponse>("/v1/prompts", {
      method: "POST",
      body: JSON.stringify({ project_id: projectId, prompt, mode }),
    });
  },

  // Continue an interrupted build turn from its persisted state. The server
  // reconstructs the context, so there is no body; it streams the same events a
  // fresh build turn does.
  resume(
    promptId: number,
    onEvent: (event: StreamEvent) => void,
    onError?: (error: Error) => void,
  ): AbortController {
    return openEventStream(`/v1/prompts/${promptId}/resume`, undefined, onEvent, onError);
  },

  // Resume a turn paused on ask_user by feeding the user's reply back as the
  // tool_result for the pending call. The server threads it into the loop and
  // streams the same events a fresh build turn does. Keyed by prompt id (the
  // question event carried it), not the ask_user tool_use id — that rides in the
  // body server-side. Question + answer bill as a single prompt.
  answer(
    promptId: number,
    answer: string,
    onEvent: (event: StreamEvent) => void,
    onError?: (error: Error) => void,
  ): AbortController {
    return openEventStream(`/v1/prompts/${promptId}/answer`, { answer }, onEvent, onError);
  },

  // Mark an interrupted turn as abandoned so a reload stops offering to resume
  // it. Leaves the transcript and files untouched.
  discard(promptId: number) {
    return request<{ status: string }>(`/v1/prompts/${promptId}/discard`, {
      method: "POST",
    });
  },

  // The prompt row itself. Mid-flight turns (paused/waiting) deliberately have
  // no turn_end marker to restore evidence from — this row's files_touched is
  // the only durable record of the legs' writes, so the restored changed-files
  // panel reads it (row 45).
  get(promptId: number) {
    return request<{
      id: number;
      status?: string;
      round?: number;
      files_touched?: string[];
    }>(`/v1/prompts/${promptId}`);
  },

  // Deliver a message to a running build turn (H2 steer): the text lands in
  // the next model request the loop makes — a mid-build redirect without
  // aborting the turn. Class "inject" adds context without redirecting. The
  // running turn keeps streaming on its original SSE connection; the steer
  // surfaces there as a marked turn. 409 when the turn is not running (send
  // a new prompt instead).
  steer(promptId: number, message: string, klass: "steer" | "inject" = "steer") {
    return request<{ prompt_id: number; delivered: boolean; sequence: number }>(
      `/v1/prompts/${promptId}/steer`,
      { method: "POST", body: JSON.stringify({ message, class: klass }) },
    );
  },
};

// ── Project verify state ──────────────────────────────

/**
 * The project's last finished verification sweep — the durable row export
 * and release already fail closed on (row 44). `status` speaks the sweep's
 * vocabulary: verified | best-effort | refused.
 */
export interface ProjectVerifyState {
  project_id: number;
  status: string;
  policy: string;
  skipped: string[];
  remaining: string[];
  reason: string;
  updated_at: string;
}

/** Relative time for a timestamp, in the phrasing the badge already uses.
 *  (CheckpointMenu keeps its own private copy — it predates this module's
 *  claim on the phrasing; folding it in is tracked separately.) */
function ago(iso: string): string {
  const then = Date.parse(iso);
  if (Number.isNaN(then)) return "";
  const secs = Math.max(0, Math.round((Date.now() - then) / 1000));
  if (secs < 60) return "just now";
  const mins = Math.round(secs / 60);
  if (mins < 60) return `${mins}m ago`;
  const hours = Math.round(mins / 60);
  if (hours < 24) return `${hours}h ago`;
  return `${Math.round(hours / 24)}d ago`;
}

/**
 * The Topbar badge's rule in one place: only a PASSING sweep claims
 * "Build verified · ago". A refused sweep, or no recorded sweep at all
 * (the endpoint answers 404 → null), claims nothing — absence is the
 * signal, exactly what export/release already read it as.
 */
export function verifyBadge(state: ProjectVerifyState | null): string | null {
  if (!state) return null;
  if (state.status !== "verified" && state.status !== "best-effort") return null;
  return ago(state.updated_at);
}

export const verifyState = {
  /** The project's last sweep. 404 (never verified) reads as null, not as an
   *  error worth banner-ing: the badge simply does not appear. */
  async get(projectId: number): Promise<ProjectVerifyState | null> {
    try {
      return await request<ProjectVerifyState>(
        `/v1/projects/${projectId}/verify-state`,
      );
    } catch (err) {
      if (err instanceof APIError && err.status === 404) return null;
      throw err;
    }
  },
};

// ── Streaming ─────────────────────────────────────────

/**
 * The event vocabulary the server actually writes, from writeBuildEvent and the
 * plan path in internal/api/prompts.go. The previous version of this union
 * listed "tool.start", "tool.end" and "build.result" — none of which the server
 * has ever sent — and omitted "usage", "plan", "rebuild" and "task_update",
 * which it does. "command_output" carries live subprocess output from the
 * command-running tools (run_command, typecheck, lint, install_package,
 * build_apk) so the Output pane reflects what the agent's tools are printing.
 */
export type StreamEventType =
  | "thinking.start"
  | "thinking.delta"
  | "thinking.end"
  | "text.start"
  | "text.delta"
  | "text.end"
  | "phase"
  | "tool_call"
  | "verify"
  | "task_update"
  | "usage"
  | "rebuild"
  | "command_output"
  | "plan"
  | "question"
  | "decision"
  | "guardrail_reject"
  | "error"
  | "done"
  | "skills_group";

export interface StreamEventData {
  // text.delta / thinking.delta
  text?: string;
  // phase
  phase?: string;
  // tool_call, verify, guardrail_reject, task_update, rebuild, done
  tool?: string;
  path?: string;
  name?: string;
  stage?: string;
  status?: string;
  error?: string;
  detail?: string;
  id?: string;
  description?: string;
  result?: string;
  message?: string;
  // question — the agent paused on ask_user. prompt_id addresses POST /answer
  // (a fresh build turn learns its prompt id no other way); id is the pending
  // ask_user tool_use the reply resolves; choices are the suggested answers;
  // multi_select tells the sheet to render checkboxes so the user can pick
  // more than one before sending.
  question?: string;
  choices?: string[];
  multi_select?: boolean;
  prompt_id?: number;
  // skills_group — pre-flight skill names for this turn
  skills?: string[];
  data?: unknown;
  // decision — the agent recorded an architecture decision (write_adr) or a
  // durable memory (write_memory). `variant` selects the shape; the field set
  // mirrors the "What the agent knows" panel — decision+outcome for an ADR;
  // category (convention|decision|architecture|fact|warning), content, and the
  // area it applies to for a memory.
  variant?: "adr" | "memory";
  decision?: string;
  outcome?: string;
  category?: string;
  content?: string;
  metadata?: string;
  // usage
  input_tokens?: number;
  output_tokens?: number;
  cache_read_tokens?: number;
  // plan
  plan?: {
    screens?: { name: string; description: string }[];
    features?: string[];
    navigation?: string;
    packages?: string[];
  } | null;
  // done
  files_touched?: string[];
  verified?: boolean;
  skipped_gates?: string[];
  rounds?: number;
  // done — the policy the sweep ran under, whether required gates could not
  // run (best-effort), what is left to do, and (router on only) what the turn
  // cost and who served it.
  verified_best_effort?: boolean;
  verify_policy?: string;
  remaining_tasks?: string[];
  cost_usd?: number;
  routed_provider?: string;
  budget_stop?: string;
}

export interface StreamEvent {
  type: StreamEventType;
  data?: StreamEventData;
}

/**
 * The one place the whole app agrees on what a tool row's status word means.
 *
 * The live build stream speaks the Go emitter's vocabulary — "running",
 * "completed", "error" — while the stored transcript and the verify event speak
 * "done"/"pass"/"fail". The chat renders exactly three states, so a "completed"
 * read literally fell through to "running": every action rendered twice and its
 * spinner never resolved until the terminal sweep. Normalising once here (and
 * calling it from every handler) is what keeps that gap from reopening.
 */
export type RowStatus = "running" | "done" | "fail" | "warning";

export function rowStatus(status: string | undefined): RowStatus {
  switch (status) {
    case "done":
    case "completed":
    case "pass":
    case "success":
      return "done";
    case "fail":
    case "failed":
    case "error":
    case "rejected":
      return "fail";
    case "warning":
    case "unavailable":
      // A batch-conflict warning or a checker that could not run is neither
      // success nor failure — it is something the developer should see and the
      // agent already saw. Give it its own row tone instead of collapsing it
      // into the spinner bucket (the old default) or into failure.
      return "warning";
    default:
      return "running";
  }
}

export function streamPrompt(
  projectId: number,
  prompt: string,
  mode: "plan" | "build",
  onEvent: (event: StreamEvent) => void,
  onError?: (error: Error) => void,
  opts: { attachmentIds?: number[]; conversationId?: number } = {},
): AbortController {
  return openEventStream(
    "/v1/prompts/stream",
    {
      project_id: projectId,
      conversation_id: opts.conversationId || undefined,
      prompt,
      mode,
      attachment_ids: opts.attachmentIds ?? [],
    },
    onEvent,
    onError,
  );
}

// openEventStream POSTs to a server-sent-events endpoint and dispatches each
// parsed frame to onEvent, returning the AbortController that stops it. A fresh
// prompt and a resumed turn stream the identical event vocabulary, so they share
// this reader; only the path and request body differ. Pass `body: undefined` for
// an endpoint that takes no body (resume reconstructs its own context server
// side).
function openEventStream(
  path: string,
  body: unknown,
  onEvent: (event: StreamEvent) => void,
  onError?: (error: Error) => void,
): AbortController {
  const controller = new AbortController();
  const token = getToken();

  (async () => {
    try {
      const headers: Record<string, string> = {
        "Content-Type": "application/json",
        ...(token ? { Authorization: `Bearer ${token}` } : {}),
      };
      let res = await fetch(`${BASE}${path}`, {
        method: "POST",
        headers,
        body: body === undefined ? undefined : JSON.stringify(body),
        signal: controller.signal,
      });

      // Streams open rarely but live long: a 401 here gets the same silent
      // rotation as request(), once, before the session is treated as dead.
      if (res.status === 401 && token) {
        try {
          const fresh = await refreshAccessToken();
          res = await fetch(`${BASE}${path}`, {
            method: "POST",
            headers: { ...headers, Authorization: `Bearer ${fresh}` },
            body: body === undefined ? undefined : JSON.stringify(body),
            signal: controller.signal,
          });
        } catch {
          // Refresh failed: the handling below reports it.
        }
      }

      if (!res.ok) {
        if (res.status === 401 && token) handleExpiredSession();
        const errBody = await res.json().catch(() => ({ error: `HTTP ${res.status}` }));
        throw new APIError(res.status, errBody.error || `Request failed (${res.status})`);
      }

      const reader = res.body?.getReader();
      if (!reader) throw new Error("the server sent no response body");

      const decoder = new TextDecoder();
      let buffer = "";
      // The event type and data must persist across chunk boundaries: a frame
      // can arrive split between "event:" and "data:", and resetting per chunk
      // dropped whichever half came second.
      let eventType = "";
      const dataLines: string[] = [];

      const dispatch = () => {
        if (!eventType) return;
        // Multi-line data is joined with newlines, per the SSE spec — a code
        // block in the agent's prose arrives that way.
        const raw = dataLines.join("\n");
        dataLines.length = 0;
        const type = eventType as StreamEventType;
        eventType = "";
        if (!raw || raw === "null") {
          onEvent({ type });
          return;
        }
        try {
          onEvent({ type, data: JSON.parse(raw) });
        } catch {
          // A frame we cannot parse is not worth killing the stream over.
        }
      };

      while (true) {
        const { done, value } = await reader.read();
        if (done) break;

        buffer += decoder.decode(value, { stream: true });

        const lines = buffer.split("\n");
        buffer = lines.pop() ?? "";

        for (const rawLine of lines) {
          const line = rawLine.endsWith("\r") ? rawLine.slice(0, -1) : rawLine;
          if (line.startsWith("event:")) {
            eventType = line.slice(6).trim();
          } else if (line.startsWith("data:")) {
            dataLines.push(line.slice(5).trimStart());
          } else if (line === "") {
            dispatch();
          }
        }
      }
      dispatch();
    } catch (err) {
      if (err instanceof DOMException && err.name === "AbortError") return;
      onError?.(err instanceof Error ? err : new Error(String(err)));
    }
  })();

  return controller;
}

// ── Sessions ──────────────────────────────────────────

export const sessions = {
  create(projectId: number) {
    return request<Session>("/v1/sessions", {
      method: "POST",
      body: JSON.stringify({ project_id: projectId }),
    });
  },

  get(id: number) {
    return request<Session>(`/v1/sessions/${id}`);
  },

  /** The project's live session, or null when none exists (204 → undefined
   *  from request()'s empty-body handling). This is the refresh lifeline: a
   *  page reload re-attaches to the device the user already has instead of
   *  silently starting a new one. */
  async liveFor(projectId: number): Promise<Session | null> {
    return (await request<Session | undefined>(`/v1/projects/${projectId}/live-session`)) ?? null;
  },

  destroy(id: number) {
    return request<void>(`/v1/sessions/${id}`, { method: "DELETE" });
  },

  /** Un-pause a paused session — the container keeps its state; the stream
   *  re-attaches on the next viewer join. */
  resume(id: number) {
    return request<Session>(`/v1/sessions/${id}/resume`, { method: "POST" });
  },

  // One PNG frame for download or store-asset use. Fetched as a blob because
  // the endpoint needs the Bearer token and a plain link cannot send one.
  async screenshotBlob(id: number) {
    const token = getToken();
    const res = await fetch(`${BASE}/v1/sessions/${id}/screenshot`, {
      headers: token ? { Authorization: `Bearer ${token}` } : {},
    });
    if (!res.ok) throw new Error(`screenshot failed (${res.status})`);
    return res.blob();
  },
};

// ── Usage ─────────────────────────────────────────────

export const usage = {
  get() {
    return request<Usage>("/v1/usage");
  },
};

// ── Profile & billing (Phase 9) ───────────────────────

/**
 * The user's own profile and billing state. `me.get` is the single source of
 * truth the settings page reads real identity, tier, and history from — it
 * replaces the old localStorage-email guess. `me.update` changes only the
 * display name; email is the login identity and tier is provider-driven, so
 * neither is settable from here (the server ignores anything else in the body).
 */
export const me = {
  get() {
    return request<MeProfile>("/v1/me");
  },

  update(displayName: string) {
    return request<MeProfile>("/v1/me", {
      method: "PUT",
      body: JSON.stringify({ display_name: displayName }),
    });
  },
};

/**
 * Payment flows, all test-mode in this build. `upgrade` starts a hosted checkout
 * and returns where to send the user — the card is entered on that page, never
 * here (rule 2.1). `testComplete` exists only while the server runs the mock
 * provider: it simulates finishing checkout so the whole verified-webhook path
 * can be exercised without a real processor, and 404s under a real provider.
 * `cancel` ends the subscription — at period end by default (a downgrade that
 * keeps access until the term runs out), immediately when asked (rule 2.7).
 */
export const billing = {
  upgrade(tier: string = "pro") {
    return request<Checkout>("/v1/billing/upgrade", {
      method: "POST",
      body: JSON.stringify({ tier }),
    });
  },

  testComplete(tier: string = "pro") {
    return request<{ ok: boolean }>("/v1/billing/test/complete", {
      method: "POST",
      body: JSON.stringify({ tier }),
    });
  },

  cancel(immediate = false) {
    return request<{ ok: boolean }>("/v1/billing/cancel", {
      method: "POST",
      body: JSON.stringify({ immediate }),
    });
  },
};

// ── Premium git integration (W4) ──────────────────────

/**
 * The GitHub integration. `status` is the one call the UI trusts to decide what
 * to show — it reports whether the server has the OAuth app configured at all,
 * whether the user's tier unlocks the feature, and whether they've connected an
 * account. `connect` mirrors billing.upgrade: it returns a URL for the client to
 * navigate to (the Bearer token can't ride a top-level navigation), and the
 * server finishes the flow on its public callback. `push` commits and pushes a
 * project's workspace, creating the repo on first push. All gating is enforced
 * server-side; these methods just surface it.
 */
export const git = {
  status() {
    return request<GitStatus>("/v1/git/status");
  },

  connect() {
    return request<{ url: string }>("/v1/git/github/connect", { method: "POST" });
  },

  disconnect() {
    return request<{ disconnected: boolean }>("/v1/git/github", { method: "DELETE" });
  },

  async getRemote(projectId: number) {
    const res = await request<{ remote: GitRemote | null }>(
      `/v1/projects/${projectId}/git/remote`,
    );
    return res?.remote ?? null;
  },

  getProjectStatus(projectId: number) {
    return request<{ branch: string; dirty: number; ahead: number; behind: number; last_commit: string }>(
      `/v1/projects/${projectId}/git/status`,
    );
  },

  getBranches(projectId: number) {
    return request<{ branches: string[] }>(`/v1/projects/${projectId}/git/branches`);
  },

  getLog(projectId: number) {
    return request<{ log: string }>(`/v1/projects/${projectId}/git/log`);
  },

  push(projectId: number, opts: { message?: string; private?: boolean } = {}) {
    return request<GitPushResult>(`/v1/projects/${projectId}/git/push`, {
      method: "POST",
      body: JSON.stringify(opts),
    });
  },
};

// ── Memory ────────────────────────────────────────────
/** "What the agent knows" about a project: curated memory + accepted ADRs. */
export const memory = {
  async get(projectId: number) {
    const res = await request<{
      memories: ProjectMemory[] | null;
      summary: string | null;
      decisions: ArchitectureDecision[] | null;
    }>(`/v1/projects/${projectId}/memory`);
    // The server sends `null` for an empty list; normalise so callers can map
    // without a guard.
    return {
      memories: res?.memories ?? [],
      summary: res?.summary ?? "",
      decisions: res?.decisions ?? [],
    };
  },
};

// ── Release export ─────────────────────────────────────
// Signed release APKs + dApp Store submission bundles. Pro-gated
// server-side; the UI mirrors the gate but never enforces it.
export const exportApi = {
  async start(projectId: number, whatsNew: string) {
    return request<{ job_id: number; project_id: number; status: string }>(
      `/v1/projects/${projectId}/export`,
      { method: "POST", body: JSON.stringify({ whats_new: whatsNew }) },
    );
  },
  async get(jobId: number) {
    return request<ExportJob>(`/v1/exports/${jobId}`);
  },
  downloadUrl(jobId: number) {
    return `${BASE}/v1/exports/${jobId}/download`;
  },
  async uploadAsset(projectId: number, kind: "screenshot" | "icon" | "banner", file: File) {
    const token = getToken();
    const form = new FormData();
    form.append("kind", kind);
    form.append("file", file);
    const res = await fetch(`${BASE}/v1/projects/${projectId}/assets`, {
      method: "POST",
      headers: token ? { Authorization: `Bearer ${token}` } : {},
      body: form,
    });
    if (!res.ok) {
      const body = await res.json().catch(() => ({}));
      throw new Error(body.error || `upload failed (${res.status})`);
    }
    return res.json();
  },
  async listAssets(projectId: number) {
    return request<ProjectAsset[]>(`/v1/projects/${projectId}/assets`);
  },
};

// ── Prompt attachments ─────────────────────────────
// Images and markdown docs a turn carries. Uploaded unclaimed, claimed once
// by the send that names their ids.

export interface Attachment {
  id: number;
  project_id: number;
  prompt_id?: number;
  kind: "image" | "doc";
  name: string;
  mime: string;
  size: number;
}

export const attachments = {
  async upload(projectId: number, file: File): Promise<Attachment> {
    const token = getToken();
    const form = new FormData();
    form.append("file", file);
    const res = await fetch(`${BASE}/v1/projects/${projectId}/attachments`, {
      method: "POST",
      headers: token ? { Authorization: `Bearer ${token}` } : {},
      body: form,
    });
    if (!res.ok) {
      const body = await res.json().catch(() => ({}));
      throw new Error(body.error || `upload failed (${res.status})`);
    }
    return res.json();
  },
  /** load fetches bytes with the bearer token and mints an object URL.
   *  <img> tags cannot set headers, so thumbnails go through here. The
   *  caller revokes the URL when it unmounts. */
  async load(projectId: number, id: number): Promise<string> {
    const token = getToken();
    const res = await fetch(`${BASE}/v1/projects/${projectId}/attachments/${id}`, {
      headers: token ? { Authorization: `Bearer ${token}` } : {},
    });
    if (!res.ok) throw new Error(`attachment ${id} unavailable (${res.status})`);
    return URL.createObjectURL(await res.blob());
  },
  /** url is the unauthenticated shape — kept for parity with exportApi.
   *  Attachment reads require auth, so prefer load() in components. */
  url(projectId: number, id: number) {
    return `${BASE}/v1/projects/${projectId}/attachments/${id}`;
  },
};

// ── Play Billing cutover triage ──────────────────────
// One structured record per project. Dollars travel as integer cents.
export const triageApi = {
  async get(projectId: number) {
    return request<TriageReport | null>(`/v1/projects/${projectId}/triage`);
  },
  async save(
    projectId: number,
    input: { decision: string; cutover_date: string; exposure_cents: number; notes: string },
  ) {
    return request<TriageReport>(`/v1/projects/${projectId}/triage`, {
      method: "PUT",
      body: JSON.stringify(input),
    });
  },
};

// ── WebSocket ─────────────────────────────────────────

export function createAuthWebSocket(path: string): WebSocket | null {
  const token = getToken();
  if (!token) return null;

  const wsBase = BASE.replace(/^http/, "ws");
  return new WebSocket(`${wsBase}${path}?token=${encodeURIComponent(token)}`);
}
