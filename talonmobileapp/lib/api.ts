// API client mirroring the web IDE surface (frontend/lib/api.ts). Same
// routes, same shapes, the mobile app speaks the identical backend.
// EXPO_PUBLIC_API_URL points at the backend (dev: your LAN IP, never
// localhost, localhost on-device is the phone itself).
//
// Sign-in is OAuth (GitHub/Google): expo-web-browser opens the provider's
// start URL with the talon:// redirect, the backend lands the browser
// back on the app with a single-use code, and this client exchanges it,
// the JWT never rides a URL.
import { DeviceEventEmitter } from 'react-native'

const BASE = process.env.EXPO_PUBLIC_API_URL ?? 'http://localhost:8080'

// FM-SESSa/L67 — token death announces itself, once per session. The
// transport keeps its whole job (refresh → retry → clear) and this is the
// last step: the root layout listens for `auth:expired` and lands the app
// on /auth from whatever screen it was on — the thing clearing the token
// could never do (FM-SESSa(i): nothing observed it, so the app stayed on
// a screen whose every request had begun to fail). Keyed to the token
// that died: N concurrent 401s all clear (idempotent) but the root hears
// one death, and a later sign-in — a different token — is a different
// session that announces its own death when it ends.
let announcedDeathOf: string | null = null

function announceSessionExpired(diedToken: string): void {
  if (announcedDeathOf === diedToken) return
  announcedDeathOf = diedToken
  DeviceEventEmitter.emit('auth:expired')
}

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
    // …and the last step of the chain: refresh died (or the retry came
    // back 401 anyway) — tell the root, once, so the whole app can land
    // on sign-in instead of the current screen failing forever.
    announceSessionExpired(token)
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
    // Attach the HTTP status: callers must be able to tell "missing" (404 →
    // verify-state reads as no-row, no claim) from "broken" (anything else →
    // rethrow, the badge hides). Additive — the message is unchanged.
    //
    // The whole body rides alongside it under `.detail`, because a refusal is
    // often the only place the server states its offer: a 402's code /
    // spare_credits / skr_amount_per_pack / credits_per_pack are what the
    // unlock sheet renders, and a 422's `reason` is the verdict the payment
    // screen has to show. Keeping only `.message` threw all of that away at
    // the client's front door.
    const error = new Error(body.error || `request failed (${res.status})`) as Error & {
      status?: number
      detail?: Record<string, any>
    }
    error.status = res.status
    error.detail = body
    throw error
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

/** The turn_end marker's verify evidence — the durable twin of the build
 *  stream's done frame (rows 44/45): same outcome fields, written at the same
 *  instant, so a reload restores exactly what streamed. Field-for-field the
 *  web client's TurnVerifyEvidence (frontend/lib/api.ts) — both clients read
 *  one payload and must name the same claims the same way. ABSENT (not empty)
 *  on legacy markers and crash paths: absence means "no evidence recorded",
 *  which renders as a claim-free row, never as a failed check; present-and-
 *  empty means "finished, nothing to verify". */
export interface TurnVerifyEvidence {
  files_touched?: string[]
  verified?: boolean
  verified_best_effort?: boolean
  verify_policy?: string
  skipped_gates?: string[]
}

/** Rendered evidence: the marker/done-frame payload after mapping — the shape
 *  VerifyEvidenceRow draws from. An ABSENT key claims nothing in either
 *  direction (the web TurnFooter's hasEvidence gate, mirrored). */
export interface TurnEvidence {
  filesTouched?: string[]
  verified?: boolean
  verifiedBestEffort?: boolean
  verifyPolicy?: string
  skippedGates?: string[]
  rounds?: number
  remainingTasks?: string[]
  costUsd?: number
}

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
  /** turn_end: the terminal marker a turn writes when it settles. Says how the
   *  turn ended (completed | failed | cancelled | interrupted) and, on failure,
   *  the provider message. Without it a dead turn is indistinguishable from one
   *  still in flight — DSH models this as turn/end with a reason. */
  status?: string
  error?: string
  rounds?: number
  /** turn_end's verify evidence (row 44 payload): the durable twin of the done
   *  frame, so a reload restores what the turn changed and how it was checked
   *  exactly as it streamed. ABSENT on legacy markers and crash paths (no
   *  outcome) — render "no evidence", never a claim. */
  verify?: TurnVerifyEvidence
  /** context: machine guidance the orchestrator injects (a skill body, or the
   *  repair note after a verify gate fails). The model needs it verbatim, but
   *  the user never typed it — rendering it as a chat bubble is what put a
   *  400-line skill document in the middle of the conversation. title is the
   *  one line the transcript shows; body stays collapsed until asked for. */
  title?: string
  body?: string
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

/** One screen in a parsed plan. Field-for-field the web's PlanScreen
 *  (frontend/lib/types.ts): both clients render the same plan payload. */
export interface PlanScreen {
  name: string
  description: string
}

/** The parsed plan the `plan` SSE frame carries — the web's PlanData, same
 *  fields: screens/features always, navigation and packages when the model
 *  named them. This is the canonical reply of a plan turn; approve turns it
 *  into PLAN.md + a build prompt (see PlanCard / build.tsx approvePlan). */
export interface PlanData {
  screens: PlanScreen[]
  features: string[]
  navigation: string
  packages: string[]
}

/** The conversation endpoint's `latest` row — the web's LatestPrompt, same
 *  four claims. It is what the resume/discard offer reads: mode decides
 *  whether the turn can be resumed at all, status decides whether it should. */
export interface LatestPrompt {
  id: number
  mode: string
  status: string
  round?: number
}

/** A build turn whose run can be picked up again — the web's isResumable,
 *  word for word. Plan turns never resume; 'waiting' answers through
 *  /answer (the question card), not this banner; 'stalled' and terminal
 *  states are out on purpose — absence of a banner is the honest claim. */
export function isResumable(latest: LatestPrompt | null | undefined): latest is LatestPrompt {
  return (
    latest != null &&
    latest.mode === 'build' &&
    (latest.status === 'running' ||
      latest.status === 'cancelled' ||
      latest.status === 'failed' ||
      latest.status === 'paused')
  )
}

/** Normalize an untyped plan payload — the `plan` SSE frame's data.plan, or
 *  a stored history block's plan — into PlanData. One shape for the live card
 *  and the restored card, so an approve from either builds the same PLAN.md. */
export function normalizePlan(raw: unknown): PlanData {
  const p = (raw ?? {}) as Partial<PlanData>
  const asStrings = (v: unknown): string[] =>
    Array.isArray(v) ? v.filter((x): x is string => typeof x === 'string') : []
  const screens = (Array.isArray(p.screens) ? p.screens : []) as (Partial<PlanScreen> | null)[]
  return {
    screens: screens.map((s) => ({
      name: String(s?.name ?? ''),
      description: String(s?.description ?? ''),
    })),
    features: asStrings(p.features),
    navigation: typeof p.navigation === 'string' ? p.navigation : '',
    packages: asStrings(p.packages),
  }
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

/**
 * The submission bundle the server writes beside a finished APK: the README
 * verbatim, plus the manifest it recorded.
 *
 * Served as written, not re-derived — a surface shows what the server
 * committed to instead of re-authoring the steps, the package, the costs or
 * the portal address (FM-SUBa(i)/(ii)/(iii)).
 */
export interface SubmissionBundle {
  submit_md: string
  manifest: {
    android_package: string
    portal_url: string
    costs_note: string
    publish_command: string
    [key: string]: unknown
  }
}

export interface NotificationRow {
  id: number
  kind: string
  payload: string
  sent_at: string
  read_at?: string | null
  /** Wire names. The server sends title/body; normalized to payload
   *  on fetch (see notificationsApi.list). */
  title?: string
  body?: string
}

export interface User {
  id: number
  email: string
  display_name?: string
  avatar_url?: string
  tier?: string
  onesignal_id?: string
  /** L65/FM-WALLb: whether this account holds its connected wallet. */
  wallet_linked?: boolean
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
      latest?: LatestPrompt | null
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
  // The discovery path. A session started from the web IDE — or by any other
  // client — is invisible to this app otherwise, because the id was never in
  // this device's SecureStore. The server is the source of truth; asking it
  // is what keeps every client showing the same device. 204 means idle.
  // request() resolves 204 as undefined, and react-query v5 rejects
  // query functions that resolve undefined — map idle to null here so the
  // ["live-session", projectId] query never dies with "Query data cannot be
  // undefined".
  async live(projectId: number) {
    return (
      (await request<Session | null>(
        `/v1/projects/${projectId}/live-session`,
      )) ?? null
    )
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
      // L34 / FM-DEVf — the raw exp:// link above is for the QR and the dev
      // client's own scanner; this is the same link wrapped for an
      // ACTION_VIEW (`<scheme>://expo-development-client/?url=…`), which is
      // the only shape an installed dev client will load from a tap.
      dev_launcher_link?: string
      metro_url?: string
      dev_client_url?: string
    }>(`/v1/projects/${projectId}/test-info`)
  },
}

// ── Streams (SSE via expo/fetch — settles at headers, streams the body) ──

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
  // L49 / FM-SSEa — the stream fetch is expo/fetch, not the global one.
  // React Native's global fetch is whatwg-fetch 3.6.20: its promise settles
  // only in xhr.onload (the whole body), and an SSE body does not arrive
  // until the turn ends — so the connect deadline below fired first and the
  // app cancelled its own healthy stream (the two phone deaths in the
  // backend log: resume latency_ms=15013, stream latency_ms=14850). expo/fetch
  // resolves at response headers and streams chunks through
  // body.getReader(); the lazy require keeps its native module out of this
  // file's static graph (L39's seam rule).
  const { fetch: streamFetch } = require('expo/fetch') as typeof import('expo/fetch')
  // Connect deadline: streamFetch resolving IS the response headers
  // arriving, so this only bounds the wait for first byte — body reads stay
  // unbounded (a build turn streams for minutes). Without it a dead
  // connection parks the screen at fetch-start forever with no error to show.
  const CONNECT_TIMEOUT_MS = 15_000
  let connectTimedOut = false
  const fetchConnect = async (url: string, opts: RequestInit) => {
    let timer: ReturnType<typeof setTimeout> | undefined
    const deadline = new Promise<never>((_, reject) => {
      timer = setTimeout(() => {
        connectTimedOut = true
        reject(new Error(`connect timeout after ${CONNECT_TIMEOUT_MS}ms`))
        controller.abort()
      }, CONNECT_TIMEOUT_MS)
    })
    try {
      return await Promise.race([streamFetch(url, opts), deadline])
    } finally {
      clearTimeout(timer)
    }
  }
  ;(async () => {
    try {
      let res = await fetchConnect(`${BASE}${path}`, {
        ...init,
        signal: controller.signal,
        headers,
      })
      // Streams open rarely but live long: a 401 here gets the same silent
      // rotation as request(), once, before the session is treated as dead.
      if (res.status === 401 && token) {
        try {
          const fresh = await refreshAccessToken()
          res = await fetchConnect(`${BASE}${path}`, {
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
      if (connectTimedOut) {
        // The race can settle with either our deadline error or the abort
        // rejection — report the one truth we care about either way.
        onError?.(
          new Error(
            `The server never answered this stream (no response headers after ${CONNECT_TIMEOUT_MS}ms). Check the backend and that the app can reach it.`,
          ),
        )
      } else if (!controller.signal.aborted) {
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

  /** The single-prompt row — the union seed. The index endpoint is metadata
   *  only (PromptIndexEntry), so a marker-less turn's files_touched lives
   *  here; same route and shape as the web's prompts.get (rows 44/45). */
  get(promptId: number) {
    return request<{ id: number; status?: string; round?: number; files_touched?: string[] }>(
      `/v1/prompts/${promptId}`,
    )
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

// ── Project verify state (the build-verified badge) ──
// Field-for-field the web's ProjectVerifyState: the project's last finished
// verification sweep, vocabulary verified | best-effort | refused.

export interface ProjectVerifyState {
  project_id: number
  status: string
  policy: string
  skipped: string[]
  remaining: string[]
  reason: string
  updated_at: string
}

/** Relative time for a timestamp, in the phrasing the badge already uses —
 *  a verbatim mirror of the web's ago(). */
function ago(iso: string): string {
  const then = Date.parse(iso)
  if (Number.isNaN(then)) return ''
  const secs = Math.max(0, Math.round((Date.now() - then) / 1000))
  if (secs < 60) return 'just now'
  const mins = Math.round(secs / 60)
  if (mins < 60) return `${mins}m ago`
  const hours = Math.round(mins / 60)
  if (hours < 24) return `${hours}h ago`
  return `${Math.round(hours / 24)}d ago`
}

/** The badge's rule in one place: only a PASSING sweep claims "Build verified
 *  · ago". A refused sweep, or no recorded sweep at all (404 → null), claims
 *  nothing — absence is the signal, exactly what export/release read it as.
 *  Verbatim mirror of the web's verifyBadge: both clients must agree on when
 *  to claim, in the same words. */
export function verifyBadge(state: ProjectVerifyState | null): string | null {
  if (!state) return null
  if (state.status !== 'verified' && state.status !== 'best-effort') return null
  return ago(state.updated_at)
}

export const verifyState = {
  /** The project's last sweep. 404 (never verified) reads as null, not as an
   *  error worth banner-ing: the badge simply does not appear. parseResponse
   *  attaches the HTTP status, so "missing" is told from "broken" — a broken
   *  endpoint throws (badge hides), never claims. */
  async get(projectId: number): Promise<ProjectVerifyState | null> {
    try {
      return await request<ProjectVerifyState>(`/v1/projects/${projectId}/verify-state`)
    } catch (err) {
      if ((err as { status?: number }).status === 404) return null
      throw err
    }
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
    // Unwrap here so every screen maps an array, never an object. Title
    // plus body fold into payload, the one text the rows render.
    return request<{ notifications: NotificationRow[] }>('/v1/notifications').then((r) =>
      (r.notifications ?? []).map((n) => ({
        ...n,
        payload: n.payload ?? ([n.title, n.body].filter(Boolean).join('\n') || ''),
      })),
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
  /** Wire names. The server sends entry_type/content; normalized to
   *  kind/text on fetch (see memoryApi.get). */
  entry_type?: string
  content?: string
}

export const memoryApi = {
  get(projectId: number) {
    return request<{
      memories: MemoryEntry[]
      summary: string
      decisions: { id: number; title: string; status: string }[]
    }>(`/v1/projects/${projectId}/memory`).then((r) => ({
      ...r,
      // EntryType/content is the wire shape; the screens read kind/text.
      // Without this, kind is undefined and the More tab crashes on
      // toUpperCase the moment a project has any memory.
      memories: (r.memories ?? []).map((m) => ({
        ...m,
        kind: m.kind ?? m.entry_type ?? 'memory',
        text: m.text ?? m.content ?? '',
      })),
    }))
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
  // How to submit this release. The files live on the server's disk, so this
  // is the only way a phone can read them (FM-SUBa(i)).
  bundle(jobId: number) {
    return request<SubmissionBundle>(`/v1/exports/${jobId}/bundle`)
  },
  // The signed artifact itself. Exposed because the download rides the
  // session token, so a surface has to fetch rather than link (FM-APKa(ii)).
  downloadUrl(jobId: number) {
    return `${BASE}/v1/exports/${jobId}/download`
  },
  /**
   * Hand the signed APK to the person who paid for it.
   *
   * Three things have to be true for this to be a delivery rather than a
   * file that exists (FM-APKa): the session token rides the request, because
   * no link can carry a Bearer header; the bytes land in the app's cache and
   * are immediately handed to the system share sheet, because a file written
   * into a sandbox nobody can browse is not reachable; and the write is
   * `idempotent`, because expo-file-system's download defaults to throwing
   * "when the target file already exists" — the first export would work and
   * every one after it would throw on a name the device still holds.
   *
   * Both modules are imported lazily: they are native, and importing them at
   * module scope would pull them into every screen and every suite that
   * imports this file.
   *
   * @returns the local uri the file was written to.
   */
  async downloadApk(jobId: number, filename: string) {
    // `require`, not `import()` — two reasons, both load-bearing. They are
    // native modules that must stay OUT of this file's static graph (every
    // suite and every screen that imports `lib/api` would otherwise have to
    // load them), and jest's mock registry answers `require`: a dynamic
    // import of expo-file-system instead reaches the package's own
    // `exports` default, `src/index.ts`, which node_modules are excluded
    // from transforming — so it is parsed as CJS and dies on its `export`
    // statements.
    const { File, Paths } = require('expo-file-system') as typeof import('expo-file-system')
    // Named export, not `.default`: expo-sharing's types declare `shareAsync`
    // as a named export and no default at all, so `.default` would be
    // undefined against the real package even though a hand-written mock
    // would happily provide one.
    const { shareAsync } = require('expo-sharing') as typeof import('expo-sharing')
    // Also `require`, for the same reason: `await import('./auth')` takes
    // jest's ESM path, which does not consult CJS mock factories — the
    // registered mock for `@/lib/auth` is skipped and the loader then parses
    // auth.ts (whose line 1 is `import * as SecureStore …`) as CJS:
    // "Unexpected import statement in CJS module." `request` keeps its own
    // dynamic import, which is exercised without a mock and unaffected.
    const { getToken } = require('./auth') as typeof import('./auth')

    const token = await getToken()
    const file = await File.downloadFileAsync(
      `${BASE}/v1/exports/${jobId}/download`,
      new File(Paths.cache, filename),
      {
        headers: token ? { Authorization: `Bearer ${token}` } : {},
        idempotent: true,
      },
    )

    await shareAsync(file.uri, {
      mimeType: 'application/vnd.android.package-apk',
      dialogTitle: filename,
    })
    return file.uri
  },
}

// ── SKR ────────────────────────────────────────────────────────────────

// What the server says the pack costs: the amount, the mint and the treasury
// the phone must pay. All five fields are server-stated on purpose — a price
// or a mint baked into the binary cannot be changed without a store release,
// and a rotated treasury would strand every installed client.
export type SkrQuote = {
  skr_amount: number
  credits: number
  mint: string
  treasury: string
  spare_credits: number
}

// The backend's three-way answer about a payment: 200 verified, 200 pending
// (verified:false, reason "pending"), or 422 rejected. A rejected verdict is
// delivered on the thrown error's `.detail` rather than as a resolved value,
// so that 422 keeps its HTTP meaning — out of the success path — while still
// handing the client the reason it must display.
export type SkrVerdict = {
  verified: boolean
  reason: string
  credits_added: number
  spare_credits: number
}

export const skrApi = {
  quote() {
    // L62/FM-SKRc(i): the router registers POST — request()'s GET default
    // made every quote die 405 behind "could not read your export credits".
    return request<SkrQuote>('/v1/skr/quote', { method: 'POST' })
  },
  verify(signature: string) {
    return request<SkrVerdict>('/v1/skr/verify', {
      method: 'POST',
      body: JSON.stringify({ signature }),
    })
  },
}

// L63/FM-WALLa — the two acts of wallet ownership. The public pair below
// mints a SIGN-IN challenge (anyone may ask — the caller has no session
// yet; onboarding holds its proof until sign-in). The /v1/me pair mints a
// LINK challenge: session-bound, so the identity lands on the account
// standing in front of it — one wallet to one account, replace semantics,
// a wallet owned elsewhere refused by name (409).
export type WalletChallenge = { nonce: string; message: string }

export const walletAuthApi = {
  challenge(wallet: string) {
    return request<WalletChallenge>('/v1/auth/wallet/challenge', {
      method: 'POST',
      body: JSON.stringify({ wallet }),
    })
  },
}

export type WalletProof = { wallet: string; nonce: string; signature: string }

export const walletLinkApi = {
  challenge(wallet: string) {
    return request<WalletChallenge>('/v1/me/wallet/challenge', {
      method: 'POST',
      body: JSON.stringify({ wallet }),
    })
  },
  verify(proof: WalletProof) {
    return request<{ linked: boolean; wallet: string }>('/v1/me/wallet/verify', {
      method: 'POST',
      body: JSON.stringify(proof),
    })
  },
}
