// API client mirroring the web IDE surface (frontend/lib/api.ts). Same
// routes, same shapes — the mobile app speaks the identical backend.
// EXPO_PUBLIC_API_URL points at the backend (dev: your LAN IP, never
// localhost — localhost on-device is the phone itself).
//
// Sign-in is OAuth (GitHub/Google): expo-web-browser opens the provider's
// start URL with the expokit:// redirect, the backend lands the browser
// back on the app with a single-use code, and this client exchanges it —
// the JWT never rides a URL.
const BASE = process.env.EXPO_PUBLIC_API_URL ?? 'http://localhost:8080';

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const { getToken } = await import('./auth');
  const token = await getToken();
  const res = await fetch(`${BASE}${path}`, {
    ...init,
    headers: {
      'Content-Type': 'application/json',
      ...(token ? { Authorization: `Bearer ${token}` } : {}),
      ...init?.headers,
    },
  });
  if (!res.ok) {
    if (res.status === 401) {
      try {
        const { clearToken } = await import('./auth');
        await clearToken();
      } catch {}
    }
    const body = await res.json().catch(() => ({}));
    throw new Error(body.error || `request failed (${res.status})`);
  }
  if (res.status === 204) return undefined as T;
  const text = await res.text();
  if (!text) return undefined as T;
  return JSON.parse(text) as T;
}

// ── Shapes (mirrored from the web client; the backend is the contract) ──

export interface Project {
  id: number;
  user_id: number;
  name: string;
  status: string;
  android_package?: string;
  created_at?: string;
  updated_at?: string;
}

export interface FileEntry {
  path: string;
  content: string;
}

export interface Session {
  id: number;
  project_id: number;
  state: string;
  started_at?: string;
  ended_at?: string;
  error?: string;
}

export interface MetroEvent {
  kind:
    | 'connected'
    | 'bundle_start'
    | 'bundle_progress'
    | 'bundle_done'
    | 'reloaded'
    | 'error'
    | 'unavailable';
  text: string;
  progress: number;
}

export interface PlanTask {
  id: string;
  description: string;
  status: string;
  result?: string;
}

export interface Usage {
  used_cents: number;
  limit_cents: number;
  period?: string;
}

export interface TriageReport {
  decision: string;
  cutover_date: string;
  exposure_cents: number;
  notes: string;
}

export interface ExportJob {
  id: number;
  project_id: number;
  status: 'queued' | 'running' | 'done' | 'failed';
  error?: string;
}

export interface NotificationRow {
  id: number;
  kind: string;
  payload: string;
  sent_at: string;
  read_at?: string | null;
}

export interface User {
  id: number;
  email: string;
  name?: string;
  avatar_url?: string;
  tier?: string;
  onesignal_id?: string;
}

/** One streamed event off a plan/build/answer SSE. kind is the vocabulary. */
export interface StreamEvent {
  kind: string;
  data: string;
}

// ── Auth (OAuth only — email/password is gone) ──────────

export const authApi = {
  /** Which providers this server honestly has configured. */
  providers() {
    return request<{ providers: { id: string; name: string }[] }>(
      '/v1/auth/providers',
    );
  },

  /** The single-use code from the deep link becomes the session JWT. */
  exchange(code: string) {
    return request<{ token: string; user: User }>('/v1/auth/exchange', {
      method: 'POST',
      body: JSON.stringify({ code }),
    });
  },

  /** Where expo-web-browser goes to start a provider flow. */
  startURL(provider: string, redirect: string) {
    return `${BASE}/v1/auth/${provider}/start?redirect=${encodeURIComponent(redirect)}`;
  },
};

// ── Projects ───────────────────────────────────────────

export const projectsApi = {
  list() {
    return request<Project[]>('/v1/projects');
  },
  get(id: number) {
    return request<Project>(`/v1/projects/${id}`);
  },
  create(name: string) {
    return request<Project>('/v1/projects', {
      method: 'POST',
      body: JSON.stringify({ name }),
    });
  },
  getFiles(id: number) {
    return request<FileEntry[]>(`/v1/projects/${id}/files`);
  },
  writeFiles(id: number, files: FileEntry[]) {
    return request<void>(`/v1/projects/${id}/files`, {
      method: 'PUT',
      body: JSON.stringify({ files }),
    });
  },
  deleteFile(id: number, path: string) {
    return request<void>(`/v1/projects/${id}/files?path=${encodeURIComponent(path)}`, {
      method: 'DELETE',
    });
  },
  delete(id: number) {
    return request<void>(`/v1/projects/${id}`, { method: 'DELETE' });
  },
};

// ── Conversation (history + diff) ─────────────────────────
export const conversationApi = {
  get(projectId: number) {
    return request<{
      turns: { role: string; blocks: { kind: string; text?: string; plan?: unknown }[] }[] | null;
      latest?: { id: number; status: string } | null;
      usage?: Usage | null;
      planTasks?: PlanTask[] | null;
    }>(`/v1/projects/${projectId}/conversation`);
  },
  diff(projectId: number) {
    return request<{ diff: string; note?: string }>(`/v1/projects/${projectId}/diff`);
  },
  clear(projectId: number) {
    return request<void>(`/v1/projects/${projectId}/conversation`, { method: 'DELETE' });
  },
  rate(projectId: number, messageId: string, rating: number, comment?: string) {
    return request<void>(`/v1/projects/${projectId}/feedback`, {
      method: 'POST',
      body: JSON.stringify({ messageId, rating, comment }),
    });
  },
};

export const checkpointsApi = {
  list(projectId: number) {
    return request<{ checkpoints: { id: string; created_at: string }[] | null }>(
      `/v1/projects/${projectId}/checkpoints`,
    );
  },
  restore(projectId: number, checkpointId: string) {
    return request<{ status: string; restored: string[] }>(
      `/v1/projects/${projectId}/checkpoints/${checkpointId}/restore`,
      { method: 'POST' },
    );
  },
};

// ── Device sessions ────────────────────────────────────

export const sessionsApi = {
  create(projectId: number) {
    return request<Session>('/v1/sessions', {
      method: 'POST',
      body: JSON.stringify({ project_id: projectId }),
    });
  },
  get(id: number) {
    return request<Session>(`/v1/sessions/${id}`);
  },
  destroy(id: number) {
    return request<void>(`/v1/sessions/${id}`, { method: 'DELETE' });
  },
};

export const testInfoApi = {
  get(projectId: number) {
    return request<{
      available: boolean;
      reason?: string;
      deep_link?: string;
      metro_url?: string;
      dev_client_url?: string;
    }>(`/v1/projects/${projectId}/test-info`);
  },
};

// ── Streams (SSE via fetch — Expo's fetch keeps the connection open) ──

/**
 * Open a backend SSE endpoint with the auth token. Returns (events, close):
 * events resolve as they arrive; close() ends the reader.
 */
export async function openStream(
  path: string,
  init: RequestInit | undefined,
  onEvent: (event: StreamEvent) => void,
  onError?: (error: Error) => void,
): Promise<() => void> {
  const { getToken } = await import('./auth');
  const token = await getToken();
  const controller = new AbortController();
  (async () => {
    try {
      const res = await fetch(`${BASE}${path}`, {
        ...init,
        signal: controller.signal,
        headers: {
          'Content-Type': 'application/json',
          ...(token ? { Authorization: `Bearer ${token}` } : {}),
          ...init?.headers,
        },
      });
      if (!res.ok || !res.body) {
        throw new Error(`stream failed (${res.status})`);
      }
      const reader = res.body.getReader();
      const decoder = new TextDecoder();
      let buf = '';
      let kind = '';
      for (;;) {
        const { done, value } = await reader.read();
        if (done) break;
        buf += decoder.decode(value, { stream: true });
        let nl: number;
        while ((nl = buf.indexOf('\n')) >= 0) {
          const line = buf.slice(0, nl).trim();
          buf = buf.slice(nl + 1);
          if (line.startsWith('event:')) {
            kind = line.slice(6).trim();
          } else if (line.startsWith('data:') && kind) {
            onEvent({ kind, data: line.slice(5).trim() });
            kind = '';
          }
        }
      }
    } catch (err) {
      if (!controller.signal.aborted) {
        onError?.(err instanceof Error ? err : new Error(String(err)));
      }
    }
  })();
  return () => controller.abort();
}

export const promptsApi = {
  /** Stream a plan or build turn. Returns a close handle. */
  stream(
    projectId: number,
    mode: 'plan' | 'build',
    prompt: string,
    onEvent: (event: StreamEvent) => void,
    onError?: (error: Error) => void,
  ) {
    return openStream(
      '/v1/prompts/stream',
      {
        method: 'POST',
        body: JSON.stringify({ project_id: projectId, mode, prompt }),
      },
      onEvent,
      onError,
    );
  },

  /** Answer an ask_user pause; streams like a fresh build turn. */
  answer(
    promptId: number,
    answer: string,
    onEvent: (event: StreamEvent) => void,
    onError?: (error: Error) => void,
  ) {
    return openStream(
      `/v1/prompts/${promptId}/answer`,
      { method: 'POST', body: JSON.stringify({ answer }) },
      onEvent,
      onError,
    );
  },

  resume(
    promptId: number,
    onEvent: (event: StreamEvent) => void,
    onError?: (error: Error) => void,
  ) {
    return openStream(`/v1/prompts/${promptId}/resume`, { method: 'POST' }, onEvent, onError);
  },

  steer(promptId: number, message: string, klass: 'steer' | 'inject' = 'steer') {
    return request<{ prompt_id: number; delivered: boolean; sequence: number }>(
      `/v1/prompts/${promptId}/steer`,
      { method: 'POST', body: JSON.stringify({ message, class: klass }) },
    );
  },

  discard(promptId: number) {
    return request<{ status: string }>(`/v1/prompts/${promptId}/discard`, { method: 'POST' });
  },

  list(projectId: number) {
    return request<{ id: number; mode: string; status: string }[]>(
      `/v1/projects/${projectId}/prompts`,
    );
  },
};

// ── Plan tasks ─────────────────────────────────────────

export const planApi = {
  /** Plan tasks from the project's persisted plan_json (written by plan
   *  turns; the stream carries fresher task_updates). */
  async get(projectId: number): Promise<PlanTask[]> {
    const project = await request<Project & { plan_json?: string }>(`/v1/projects/${projectId}`);
    if (!project.plan_json) return [];
    try {
      const parsed = JSON.parse(project.plan_json) as { tasks?: PlanTask[] };
      return parsed.tasks ?? [];
    } catch {
      return [];
    }
  },
};

// ── Usage, profile, notifications ──────────────────────

export const usageApi = {
  get() {
    return request<Usage>('/v1/usage');
  },
};

export const meApi = {
  get() {
    return request<User>('/v1/me');
  },
  /** Store the OneSignal subscription id for this install. */
  setPushToken(onesignalId: string) {
    return request<User>('/v1/users/push-token', {
      method: 'POST',
      body: JSON.stringify({ onesignal_id: onesignalId }),
    });
  },
};

export const notificationsApi = {
  list() {
    return request<NotificationRow[]>('/v1/notifications');
  },
  markRead(id: number) {
    return request<void>(`/v1/notifications/${id}/read`, { method: 'POST' });
  },
};

// ── Metro (project bundler events for the preview badge) ──

export function openMetroStream(
  projectId: number,
  onEvent: (event: MetroEvent) => void,
  onError?: (error: Error) => void,
): Promise<() => void> {
  return openStream(
    `/v1/projects/${projectId}/metro`,
    undefined,
    (ev) => {
      try {
        onEvent(JSON.parse(ev.data) as MetroEvent);
      } catch {
        /* malformed frames are dropped, not fatal */
      }
    },
    onError,
  );
}

// ── Memory (what the agent knows about this project) ───

export interface MemoryEntry {
  id: number;
  kind: string;
  text: string;
  created_at?: string;
}

export const memoryApi = {
  get(projectId: number) {
    return request<{
      memories: MemoryEntry[];
      summary: string;
      decisions: { id: number; title: string; status: string }[];
    }>(`/v1/projects/${projectId}/memory`);
  },
};

// ── Triage & export (unchanged from v1) ────────────────

export const triageApi = {
  get(projectId: number) {
    return request<TriageReport | null>(`/v1/projects/${projectId}/triage`);
  },
  save(
    projectId: number,
    input: { decision: string; cutover_date: string; exposure_cents: number; notes: string },
  ) {
    return request<TriageReport>(`/v1/projects/${projectId}/triage`, {
      method: 'PUT',
      body: JSON.stringify(input),
    });
  },
};

export const exportApi = {
  start(projectId: number, whatsNew: string) {
    return request<{ job_id: number }>(`/v1/projects/${projectId}/export`, {
      method: 'POST',
      body: JSON.stringify({ whats_new: whatsNew }),
    });
  },
  get(jobId: number) {
    return request<ExportJob>(`/v1/exports/${jobId}`);
  },
};
