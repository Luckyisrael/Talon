// API client mirroring the web IDE surface (frontend/lib/api.ts). Same
// routes, same shapes — the mobile app speaks the identical backend.
// EXPO_PUBLIC_API_URL points at the backend (dev: your LAN IP, never
// localhost — localhost on-device is the phone itself).
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
    const body = await res.json().catch(() => ({}));
    throw new Error(body.error || `request failed (${res.status})`);
  }
  return res.json() as Promise<T>;
}

export interface Project {
  id: number;
  user_id: number;
  name: string;
  status: string;
  android_package?: string;
  created_at: string;
}

export interface TriageReport {
  id: number;
  project_id: number;
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

export const authApi = {
  login(email: string, password: string) {
    return request<{ token: string }>('/v1/auth/login', {
      method: 'POST',
      body: JSON.stringify({ email, password }),
    });
  },
  register(email: string, password: string) {
    return request<{ token: string }>('/v1/auth/register', {
      method: 'POST',
      body: JSON.stringify({ email, password }),
    });
  },
};

export const projectsApi = {
  list() {
    return request<Project[]>('/v1/projects');
  },
  get(id: number) {
    return request<Project>(`/v1/projects/${id}`);
  },
};

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
