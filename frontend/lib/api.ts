import { clearToken, getToken } from "./auth";
import type {
  AuthResponse,
  FileEntry,
  Project,
  PromptResponse,
  Session,
  Usage,
} from "./types";

const BASE = process.env.NEXT_PUBLIC_API_URL || "http://localhost:8080";

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
  if (here === "/login" || here === "/register") return;
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

  if (!res.ok) {
    if (res.status === 401 && token) handleExpiredSession();
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

// ── Auth ──────────────────────────────────────────────

export const auth = {
  login(email: string, password: string) {
    return request<AuthResponse>("/v1/auth/login", {
      method: "POST",
      body: JSON.stringify({ email, password }),
    });
  },

  // The API takes an address and a password. It has never stored a name, and
  // sending one made the sign-up form ask for something nothing would use.
  register(email: string, password: string) {
    return request<AuthResponse>("/v1/auth/register", {
      method: "POST",
      body: JSON.stringify({ email, password }),
    });
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

/** One stored turn, in the provider-neutral block shape the backend persists. */
export interface ConversationBlock {
  type: "text" | "thinking" | "tool_use" | "tool_result";
  text?: string;
  id?: string;
  name?: string;
  input?: Record<string, unknown>;
  tool_use_id?: string;
  content?: string;
  is_error?: boolean;
}

export interface ConversationTurn {
  role: "user" | "assistant";
  blocks: ConversationBlock[];
  at: string;
}

export const conversation = {
  async get(projectId: number) {
    const res = await request<{ turns: ConversationTurn[] | null }>(
      `/v1/projects/${projectId}/conversation`,
    );
    return res?.turns ?? [];
  },

  clear(projectId: number) {
    return request<{ status: string }>(`/v1/projects/${projectId}/conversation`, {
      method: "DELETE",
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
  | "guardrail_reject"
  | "error"
  | "done";

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
  rounds?: number;
}

export interface StreamEvent {
  type: StreamEventType;
  data?: StreamEventData;
}

export function streamPrompt(
  projectId: number,
  prompt: string,
  mode: "plan" | "build",
  onEvent: (event: StreamEvent) => void,
  onError?: (error: Error) => void,
): AbortController {
  const controller = new AbortController();
  const token = getToken();

  (async () => {
    try {
      const res = await fetch(`${BASE}/v1/prompts/stream`, {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          ...(token ? { Authorization: `Bearer ${token}` } : {}),
        },
        body: JSON.stringify({ project_id: projectId, prompt, mode }),
        signal: controller.signal,
      });

      if (!res.ok) {
        if (res.status === 401 && token) handleExpiredSession();
        const body = await res.json().catch(() => ({ error: `HTTP ${res.status}` }));
        throw new APIError(res.status, body.error || `Request failed (${res.status})`);
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

  destroy(id: number) {
    return request<void>(`/v1/sessions/${id}`, { method: "DELETE" });
  },
};

// ── Usage ─────────────────────────────────────────────

export const usage = {
  get() {
    return request<Usage>("/v1/usage");
  },
};

// ── WebSocket ─────────────────────────────────────────

export function createAuthWebSocket(path: string): WebSocket | null {
  const token = getToken();
  if (!token) return null;

  const wsBase = BASE.replace(/^http/, "ws");
  return new WebSocket(`${wsBase}${path}?token=${encodeURIComponent(token)}`);
}
