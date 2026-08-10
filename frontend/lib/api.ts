import { getToken } from "./auth";
import type {
  AuthResponse,
  FileEntry,
  Project,
  PromptResponse,
  Session,
  Usage,
} from "./types";

const BASE = process.env.NEXT_PUBLIC_API_URL || "http://localhost:8080";

class APIError extends Error {
  constructor(
    public status: number,
    message: string,
  ) {
    super(message);
    this.name = "APIError";
  }
}

async function request<T>(
  path: string,
  options: RequestInit = {},
): Promise<T> {
  const token = getToken();
  const headers: Record<string, string> = {
    "Content-Type": "application/json",
    ...(options.headers as Record<string, string>),
  };
  if (token) {
    headers.Authorization = `Bearer ${token}`;
  }

  const res = await fetch(`${BASE}${path}`, {
    ...options,
    headers,
  });

  if (!res.ok) {
    const body = await res.json().catch(() => ({ error: `HTTP ${res.status}` }));
    throw new APIError(res.status, body.error || `Request failed (${res.status})`);
  }

  return res.json();
}

// ── Auth ──────────────────────────────────────────────

export const auth = {
  login(email: string, password: string) {
    return request<AuthResponse>("/v1/auth/login", {
      method: "POST",
      body: JSON.stringify({ email, password }),
    });
  },

  register(email: string, password: string, name: string) {
    return request<AuthResponse>("/v1/auth/register", {
      method: "POST",
      body: JSON.stringify({ email, password, name }),
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

export interface StreamEvent {
  type:
    | "thinking.start"
    | "thinking.delta"
    | "thinking.end"
    | "text.start"
    | "text.delta"
    | "text.end"
    | "tool.start"
    | "tool.end"
    | "build.result"
    | "phase"
    | "tool_call"
    | "verify"
    | "guardrail_reject"
    | "error"
    | "done";
  data?: {
    text?: string;
    name?: string;
    input?: { path?: string; name?: string; description?: string };
    result?: string;
    output?: string;
    fixed?: boolean;
    message?: string;
    phase?: string;
    status?: string;
    tool?: string;
    path?: string;
    stage?: string;
    error?: string;
  };
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
        const body = await res.json().catch(() => ({ error: `HTTP ${res.status}` }));
        throw new APIError(res.status, body.error || `Request failed (${res.status})`);
      }

      const reader = res.body?.getReader();
      if (!reader) throw new Error("No response body");

      const decoder = new TextDecoder();
      let buffer = "";

      while (true) {
        const { done, value } = await reader.read();
        if (done) break;

        buffer += decoder.decode(value, { stream: true });

        // Parse SSE events from buffer
        const lines = buffer.split("\n");
        buffer = lines.pop() || "";

        let eventType = "";
        let eventData = "";

        for (const line of lines) {
          if (line.startsWith("event:")) {
            eventType = line.slice(6).trim();
          } else if (line.startsWith("data:")) {
            eventData = line.slice(5).trim();
          } else if (line === "" && eventType && eventData) {
            // Empty line = end of event
            try {
              const parsed = JSON.parse(eventData);
              onEvent({ type: eventType as StreamEvent["type"], data: parsed });
            } catch {
              // Skip malformed JSON
            }
            eventType = "";
            eventData = "";
          }
        }
      }
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
  return new WebSocket(`${wsBase}${path}?token=${token}`);
}
