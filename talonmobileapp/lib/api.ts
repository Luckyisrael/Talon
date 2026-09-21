// API client mirroring the web IDE surface (frontend/lib/api.ts). Same
// routes, same shapes, the mobile app speaks the identical backend.
// EXPO_PUBLIC_API_URL points at the backend (dev: your LAN IP, never
// localhost, localhost on-device is the phone itself).
//
// Sign-in is OAuth (GitHub/Google): expo-web-browser opens the provider's
// start URL with the talon:// redirect, the backend lands the browser
// back on the app with a single-use code, and this client exchanges it,
// the JWT never rides a URL.
const BASE = process.env.EXPO_PUBLIC_API_URL ?? 'http://localhost:8080'

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const { getToken } = await import('./auth')
  const token = await getToken()
  const headers: Record<string, string> = {
    'Content-Type': 'application/json',
    ...(init?.headers as Record<string, string>),
  }
  if (token) {
    headers.Authorization = `Bearer ${token}`
  }
  const res = await fetch(`${BASE}${path}`, { ...init, headers })

  // An expired access token fails once with a 401. If a refresh token
  // exists and this is not itself an auth call, rotate silently and retry
  // once before treating the session as dead.
  if (res.status === 401 && token && !path.startsWith('/v1/auth/')) {
    const fresh = await refreshAccessToken().catch(() => null)
    if (fresh) {
      headers.Authorization = `Bearer ${fresh}`
      const retry = await fetch(`${BASE}${path}`, { ...init, headers })
      if (retry.ok || retry.status !== 401) return parseResponse<T>(retry)
    }
    const { clearToken } = await import('./auth')
    await clearToken()
  }
  return parseResponse<T>(res)
}

async function parseResponse<T>(res: Response): Promise<T> {
  if (!res.ok) {
    if (res.status === 401) {
      try {
        const { clearToken } = await import('./auth')
        await clearToken()
      } catch {}
    }
    const body = await res.json().catch(() => ({}))
    throw new Error(body.error || `request failed (${res.status})`)
  }
  if (res.status === 204) return undefined as T
  const text = await res.text()
  if (!text) return undefined as T
  return JSON.parse(text) as T
}

// refreshAccessToken spends the stored refresh token for a new pair.
// Single-flight: concurrent 401s share one rotation instead of racing it.
let refreshInflight: Promise<string> | null = null

export async function refreshAccessToken(): Promise<string> {
  if (!refreshInflight) {
    refreshInflight = (async () => {
      const { getRefreshToken, setToken, setRefreshToken } = await import('./auth')
      const refresh = await getRefreshToken()
      if (!refresh) throw new Error('no refresh token')
      const res = await fetch(`${BASE}/v1/auth/refresh`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ refresh_token: refresh }),
      })
      if (!res.ok) throw new Error(`refresh failed (${res.status})`)
      const body = await res.json()
      await setToken(body.token)
      await setRefreshToken(body.refresh_token)
      return body.token as string
    })().finally(() => {
      refreshInflight = null
    })
  }
  return refreshInflight
}

// ── Shapes (mirrored from the web client; the backend is the contract) ──

/** One stored turn block. Image and doc blocks reference prompt attachments
 *  by id; bytes resolve through attachmentsApi, never inline. */
export interface ConversationBlock {
  kind: string
  text?: string
  plan?: unknown
  attachment_id?: number
  name?: string
  mime?: string
  id?: string
  input?: Record<string, unknown>
  tool_use_id?: string
  content?: string
  is_error?: boolean
}

export interface Project {
  id: number
  user_id: number
  name: string
  status: string
  android_package?: string
  created_at?: string
  updated_at?: string
}

export interface FileEntry {
  path: string
  content: string
}

export interface Session {
  id: number
  project_id: number
  state: string
  started_at?: string
  ended_at?: string
  error?: string
}

export interface MetroEvent {
  kind: 'connected' | 'bundle_start' | 'bundle_progress' | 'bundle_done' | 'reloaded' | 'error' | 'unavailable'
  text: string
  progress: number
}

export interface PlanTask {
  id: string
  description: string
  status: string
  result?: string
}

export interface Usage {
  prompts_used: number
  prompts_limit: number
  device_minutes: number
  device_limit: number
  cost_usd?: number
  tier: string
  pacing_seconds?: number
}

export interface TriageReport {
  decision: string
  cutover_date: string
  exposure_cents: number
  notes: string
}

export interface ExportJob {
  id: number
  project_id: number
  status: 'queued' | 'running' | 'done' | 'failed'
  error?: string
}

export interface NotificationRow {
  id: number
  kind: string
  payload: string
  sent_at: string
  read_at?: string | null
}

export interface User {
  id: number
  email: string
  display_name?: string
  avatar_url?: string
  tier?: string
  onesignal_id?: string
}

/** One streamed event off a plan/build/answer SSE. kind is the vocabulary. */
export interface StreamEvent {
  kind: string
  data: string
}

// ── Auth (OAuth only, email/password is gone) ──────────

export const authApi = {
  /** Which providers this server honestly has configured. */
  providers() {
    return request<{ providers: { id: string; name: string }[] }>('/v1/auth/providers')
  },
  /** The single-use code from the deep link becomes the session JWT. */
  exchange(code: string) {
    return request<{ token: string; refresh_token?: string; user: User }>('/v1/auth/exchange', {
      method: 'POST',
      body: JSON.stringify({ code }),
    })
  },

  /** Ends one device session server-side. Best-effort: sign-out clears
   *  local tokens regardless. */
  async revoke(): Promise<void> {
    const { getRefreshToken } = await import('./auth')
    const refresh = await getRefreshToken()
    if (!refresh) return
    try {
      await fetch(`${BASE}/v1/auth/logout`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ refresh_token: refresh }),
      })
    } catch {
      // Offline sign-out still signs out.
    }
  },

  /** Live logins on this account, newest first. Hashes never leave the
   *  server; ids are revocation handles. */
  sessions() {
    return request<
      { id: number; created_at: string; expires_at: string; last_used_at?: string | null; device_label?: string }[]
    >('/v1/auth/sessions')
  },

  /** Revoke one login. Revoking the current one signs this device out on
   *  its next call, which lands back on sign-in through the normal flow. */
  revokeSession(id: number) {
    return request<{ status: string }>(`/v1/auth/sessions/${id}`, { method: 'DELETE' })
  },

  /** Revoke every login, current device included. The caller signs itself
   *  out right after. */
  logoutAll() {
    return request<{ status: string }>('/v1/auth/logout/all', { method: 'POST' })
  },

  /** Where expo-web-browser goes to start a provider flow. */
  startURL(provider: string, redirect: string) {
    return `${BASE}/v1/auth/${provider}/start?redirect=${encodeURIComponent(redirect)}`
  },
}

// ── Projects ───────────────────────────────────────────

export const projectsApi = {
  list() {
    return request<Project[]>('/v1/projects')
  },
  get(id: number) {
    return request<Project>(`/v1/projects/${id}`)
  },
  create(name: string) {
    return request<Project>('/v1/projects', {
      method: 'POST',
      body: JSON.stringify({ name }),
    })
  },
  getFiles(id: number) {
    return request<FileEntry[]>(`/v1/projects/${id}/files`)
  },
  writeFiles(id: number, files: FileEntry[]) {
    return request<void>(`/v1/projects/${id}/files`, {
      method: 'PUT',
      body: JSON.stringify({ files }),
    })
  },
  deleteFile(id: number, path: string) {
    return request<void>(`/v1/projects/${id}/files?path=${encodeURIComponent(path)}`, {
      method: 'DELETE',
    })
  },
  delete(id: number) {
    return request<void>(`/v1/projects/${id}`, { method: 'DELETE' })
  },
}

// ── Conversation (history + diff) ─────────────────────────
export const conversationApi = {
  async get(projectId: number, opts?: { beforeSeq?: number; limit?: number }) {
    const params = new URLSearchParams()
    if (opts?.beforeSeq) params.set('before_seq', String(opts.beforeSeq))
    if (opts?.limit) params.set('limit', String(opts.limit))
    const query = params.toString()
    const res = await request<{
      turns: { role: string; blocks: ConversationBlock[]; prompt_id?: number; seq?: number; at: string }[] | null
      latest?: { id: number; status: string } | null
      latest_seq?: number
      usage?: Usage | null
      planTasks?: PlanTask[] | null
    }>(`/v1/projects/${projectId}/conversation${query ? `?${query}` : ''}`)
    // The wire format names the block discriminator `type`; this app reads
    // `kind` everywhere. Normalize once here so every consumer (transcript,
    // snapshot cache, search) sees one shape. Without this every turn
    // filters to nothing and the Build tab shows an empty Transcript label.
    for (const t of res.turns ?? []) {
      for (const b of t.blocks ?? []) {
        if (!b.kind && typeof (b as unknown as { type?: unknown }).type === 'string') {
          b.kind = (b as unknown as { type: string }).type
        }
      }
    }
    return res
  },
  diff(projectId: number) {
    return request<{ diff: string; note?: string }>(`/v1/projects/${projectId}/diff`)
  },
  clear(projectId: number) {
    return request<void>(`/v1/projects/${projectId}/conversation`, { method: 'DELETE' })
  },
  rate(projectId: number, messageId: string, rating: number, comment?: string) {
    return request<void>(`/v1/projects/${projectId}/feedback`, {
      method: 'POST',
      body: JSON.stringify({ messageId, rating, comment }),
    })
  },
}

export const checkpointsApi = {
  list(projectId: number) {
    return request<{ checkpoints: { id: string; created_at: string }[] | null }>(
      `/v1/projects/${projectId}/checkpoints`,
    )
  },
  restore(projectId: number, checkpointId: string) {
    return request<{ status: string; restored: string[] }>(
      `/v1/projects/${projectId}/checkpoints/${checkpointId}/restore`,
      { method: 'POST' },
    )
  },
}

// ── Device sessions ────────────────────────────────────

export const sessionsApi = {
  create(projectId: number) {
    return request<Session>('/v1/sessions', {
      method: 'POST',
      body: JSON.stringify({ project_id: projectId }),
    })
  },
  get(id: number) {
    return request<Session>(`/v1/sessions/${id}`)
  },
  destroy(id: number) {
    return request<void>(`/v1/sessions/${id}`, { method: 'DELETE' })
  },
}

export const testInfoApi = {
  get(projectId: number) {
    return request<{
      available: boolean
      reason?: string
      deep_link?: string
      metro_url?: string
      dev_client_url?: string
    }>(`/v1/projects/${projectId}/test-info`)
  },
}

// ── Streams (SSE via fetch, Expo's fetch keeps the connection open) ──

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
  const { getToken } = await import('./auth')
  const token = await getToken()
  const controller = new AbortController()
  const headers: Record<string, string> = {
    'Content-Type': 'application/json',
    ...(token ? { Authorization: `Bearer ${token}` } : {}),
    ...(init?.headers as Record<string, string>),
  }
  ;(async () => {
    try {
      let res = await fetch(`${BASE}${path}`, {
        ...init,
        signal: controller.signal,
        headers,
      })
      // Streams open rarely but live long: a 401 here gets the same silent
      // rotation as request(), once, before the session is treated as dead.
      if (res.status === 401 && token) {
        try {
          const fresh = await refreshAccessToken()
          res = await fetch(`${BASE}${path}`, {
            ...init,
            signal: controller.signal,
            headers: { ...headers, Authorization: `Bearer ${fresh}` },
          })
        } catch {
          // Refresh failed: the handling below reports it.
        }
      }
      if (!res.ok || !res.body) {
        throw new Error(`stream failed (${res.status})`)
      }
      const reader = res.body.getReader()
      const decoder = new TextDecoder()
      let buf = ''
      let kind = ''
      for (;;) {
        const { done, value } = await reader.read()
        if (done) break
        buf += decoder.decode(value, { stream: true })
        let nl: number
        while ((nl = buf.indexOf('\n')) >= 0) {
          const line = buf.slice(0, nl).trim()
          buf = buf.slice(nl + 1)
          if (line.startsWith('event:')) {
            kind = line.slice(6).trim()
          } else if (line.startsWith('data:') && kind) {
            onEvent({ kind, data: line.slice(5).trim() })
            kind = ''
          }
        }
      }
    } catch (err) {
      if (!controller.signal.aborted) {
        onError?.(err instanceof Error ? err : new Error(String(err)))
      }
    }
  })()
  return () => controller.abort()
}

export const promptsApi = {
  /** Stream a plan or build turn. attachmentIds claim uploaded files to it. */
  stream(
    projectId: number,
    mode: 'plan' | 'build',
    prompt: string,
    onEvent: (event: StreamEvent) => void,
    onError?: (error: Error) => void,
    attachmentIds: number[] = [],
  ) {
    return openStream(
      '/v1/prompts/stream',
      {
        method: 'POST',
        body: JSON.stringify({ project_id: projectId, mode, prompt, attachment_ids: attachmentIds }),
      },
      onEvent,
      onError,
    )
  },

  /** Answer an ask_user pause; streams like a fresh build turn. */
  answer(promptId: number, answer: string, onEvent: (event: StreamEvent) => void, onError?: (error: Error) => void) {
    return openStream(
      `/v1/prompts/${promptId}/answer`,
      { method: 'POST', body: JSON.stringify({ answer }) },
      onEvent,
      onError,
    )
  },

  resume(promptId: number, onEvent: (event: StreamEvent) => void, onError?: (error: Error) => void) {
    return openStream(`/v1/prompts/${promptId}/resume`, { method: 'POST' }, onEvent, onError)
  },

  steer(promptId: number, message: string, klass: 'steer' | 'inject' = 'steer') {
    return request<{ prompt_id: number; delivered: boolean; sequence: number }>(`/v1/prompts/${promptId}/steer`, {
      method: 'POST',
      body: JSON.stringify({ message, class: klass }),
    })
  },

  discard(promptId: number) {
    return request<{ status: string }>(`/v1/prompts/${promptId}/discard`, { method: 'POST' })
  },

  list(projectId: number) {
    return request<
      { id: number; mode: string; title: string; status: string; round: number; created_at: string }[]
    >(`/v1/projects/${projectId}/prompts`)
  },
}

// ── Prompt attachments ─────────────────────────────
// Images and markdown docs a turn carries. Uploaded unclaimed, claimed once
// by the send that names their ids. React Native multipart carries the file
// as { uri, name, type }. The server sniffs the bytes, never the label.

export interface Attachment {
  id: number
  project_id: number
  prompt_id?: number
  kind: 'image' | 'doc'
  name: string
  mime: string
  size: number
}

export const attachmentsApi = {
  async upload(projectId: number, file: { uri: string; name: string; type: string }): Promise<Attachment> {
    const { getToken } = await import('./auth')
    const token = await getToken()
    const form = new FormData()
    form.append('file', file as unknown as Blob)
    const res = await fetch(`${BASE}/v1/projects/${projectId}/attachments`, {
      method: 'POST',
      headers: token ? { Authorization: `Bearer ${token}` } : {},
      body: form,
    })
    if (!res.ok) {
      const body = await res.json().catch(() => ({}))
      throw new Error(body.error || `upload failed (${res.status})`)
    }
    return res.json() as Promise<Attachment>
  },
  /** Authed image props for expo-image: <Image> cannot set headers any
   *  other way, so thumbnails go through here. */
  async imageSource(projectId: number, id: number): Promise<{ uri: string; headers: Record<string, string> }> {
    const { getToken } = await import('./auth')
    const token = await getToken()
    return {
      uri: `${BASE}/v1/projects/${projectId}/attachments/${id}`,
      headers: token ? { Authorization: `Bearer ${token}` } : {},
    }
  },
}

// ── Plan tasks ─────────────────────────────────────────

export const planApi = {
  /** Plan tasks from the project's persisted plan_json (written by plan
   *  turns; the stream carries fresher task_updates). */
  async get(projectId: number): Promise<PlanTask[]> {
    const project = await request<Project & { plan_json?: string }>(`/v1/projects/${projectId}`)
    if (!project.plan_json) return []
    try {
      const parsed = JSON.parse(project.plan_json) as { tasks?: PlanTask[] }
      return parsed.tasks ?? []
    } catch {
      return []
    }
  },
}

// ── Usage, profile, notifications ──────────────────────

export const usageApi = {
  get() {
    return request<Usage>('/v1/usage')
  },
}

export const meApi = {
  get() {
    return request<User>('/v1/me')
  },
  /** Store the OneSignal subscription id for this install. */
  setPushToken(onesignalId: string) {
    return request<User>('/v1/users/push-token', {
      method: 'POST',
      body: JSON.stringify({ onesignal_id: onesignalId }),
    })
  },
}

export const notificationsApi = {
  list() {
    // The endpoint wraps the rows: {notifications, push_configured}.
    // Unwrap here so every screen maps an array, never an object.
    return request<{ notifications: NotificationRow[] }>('/v1/notifications').then(
      r => r.notifications ?? [],
    )
  },
  markRead(id: number) {
    return request<void>(`/v1/notifications/${id}/read`, { method: 'POST' })
  },
}

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
        onEvent(JSON.parse(ev.data) as MetroEvent)
      } catch {
        /* malformed frames are dropped, not fatal */
      }
    },
    onError,
  )
}

// ── Memory (what the agent knows about this project) ───

export interface MemoryEntry {
  id: number
  kind: string
  text: string
  created_at?: string
}

export const memoryApi = {
  get(projectId: number) {
    return request<{
      memories: MemoryEntry[]
      summary: string
      decisions: { id: number; title: string; status: string }[]
    }>(`/v1/projects/${projectId}/memory`)
  },
}

// ── Triage & export (unchanged from v1) ────────────────

export const triageApi = {
  get(projectId: number) {
    return request<TriageReport | null>(`/v1/projects/${projectId}/triage`)
  },
  save(projectId: number, input: { decision: string; cutover_date: string; exposure_cents: number; notes: string }) {
    return request<TriageReport>(`/v1/projects/${projectId}/triage`, {
      method: 'PUT',
      body: JSON.stringify(input),
    })
  },
}

export const exportApi = {
  start(projectId: number, whatsNew: string) {
    return request<{ job_id: number }>(`/v1/projects/${projectId}/export`, {
      method: 'POST',
      body: JSON.stringify({ whats_new: whatsNew }),
    })
  },
  get(jobId: number) {
    return request<ExportJob>(`/v1/exports/${jobId}`)
  },
}
