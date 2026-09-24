import React from 'react'
import { render, screen, waitFor } from '@testing-library/react-native'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { RestoredTranscript, type RestoredTurn } from '@/components/build/Transcript'
import type { TurnState } from '@/hooks'

// Row 46 — mobile verify evidence (TEST_RULES: failure modes enumerated first).
//
// M1 restore drops evidence: the marker's `verify` payload already arrives on
//    the wire (blocks pass through JSON.parse untouched) but nothing types or
//    renders it — a reload loses files/policy/skips/rounds the turn proved.
// M2 legacy over-claims: a marker WITHOUT the verify payload must render what
//    the marker knows (rounds) and claim nothing about checks in either
//    direction — `verified`'s PRESENCE is the flag.
// M3 live done drops fields: the done frame carries files_touched / verified /
//    verify_policy / skipped_gates / rounds / remaining_tasks / cost_usd and
//    a message; the reducer keeps only `phase`, so the live footer is blank
//    until (unless) the history refetch lands.
// M4 union missing: a marker-less newest turn (running/waiting/cancelled/
//    failed/legacy) has no footer to rebuild — the prompt row's files_touched
//    union (row 42, GET /v1/prompts/:id) is the only durable record of its
//    writes. The list endpoint is metadata-only (PromptIndexEntry), so the
//    single-row get is the seed path, mirroring web F8/F10/F11.
// M5 badge dishonesty: no verify-state client exists — either no badge ever,
//    or worse, a claim without a 200. 404/error → NO claim; only a passing
//    sweep (verified | best-effort) claims "Build verified · {ago}".
// M6 no runner: zero tests could exist — jest-expo lands as infra so RED here
//    is assertion failures, not module-not-found (the backend's route-first
//    precedent).
//
// Vocabulary is copied from the web's TurnFooter / verifyBadge (frontend/
// lib/api.ts, AIChat.tsx): "verified · skipped: a, b", "verified (best
// effort)", "checks did not pass", "N file(s)", "N round(s)", "N left",
// "verify policy: {p}", "did not run: {g}", "Build verified · {ago}".
//
// Harness notes (both learned the hard way, kept for the next reader):
//  · RTL v14 render() is async — every render is awaited or screen never wires.
//  · the test script runs node with --experimental-vm-modules — without it the
//    VM rejects request()'s internal `import('./auth')` (lib/api.ts:13) and
//    every fetch-path test fails for runner reasons; mod() itself uses
//    require() because it must survive modules that are not written yet.

// Native-module seams: the suite exercises request()/hooks imports without a
// device keystore or AsyncStorage. Self-contained factories (jest hoists them
// above imports).
jest.mock('expo-secure-store', () => ({
  getItemAsync: jest.fn(async () => null),
  setItemAsync: jest.fn(async () => undefined),
  deleteItemAsync: jest.fn(async () => undefined),
}))
jest.mock('@react-native-async-storage/async-storage', () => ({
  default: {
    getItem: async () => null,
    setItem: async () => undefined,
    removeItem: async () => undefined,
    getAllKeys: async () => [],
  },
  getItem: async () => null,
  setItem: async () => undefined,
  removeItem: async () => undefined,
  getAllKeys: async () => [],
}))
// Reanimated boots native Worklets at import time (TraceRow → Transcript), and
// even its official mock transitively re-requires src/index (→ worklets boot).
// A self-contained factory breaks the chain: chainable layout animations, RN
// primitives for Animated.View, no-op hooks. Enough surface for the app's
// usages (Animated.View, FadeInDown.duration, useSharedValue-style hooks).
jest.mock('react-native-reanimated', () => {
  // jest hoists mock factories above all imports, so `require()` is the only
  // way out of the factory: a static import would be re-ordered, an
  // outer-scope reference is forbidden by jest's hoisting transform.
  // eslint-disable-next-line @typescript-eslint/no-require-imports
  const RN = require('react-native')
  const chain: any = new Proxy(
    function () {
      return chain
    },
    { get: () => () => chain, apply: () => chain },
  )
  const Animated = {
    View: RN.View,
    Text: RN.Text,
    Image: RN.Image,
    ScrollView: RN.ScrollView,
    createAnimatedComponent: (c: unknown) => c,
  }
  return {
    __esModule: true,
    default: Animated,
    FadeIn: chain,
    FadeInDown: chain,
    FadeInUp: chain,
    FadeOut: chain,
    FadeOutUp: chain,
    FadeOutDown: chain,
    Layout: chain,
    SlideInRight: chain,
    SlideOutRight: chain,
    useSharedValue: (v: unknown) => ({ value: v }),
    useAnimatedStyle: (fn: () => unknown) => {
      try {
        return fn()
      } catch {
        return {}
      }
    },
    withTiming: (v: unknown) => v,
    withSpring: (v: unknown) => v,
    useReducedMotion: () => false,
    runOnJS: (fn: unknown) => fn,
    runOnUI: (fn: unknown) => fn,
  }
})

/**
 * Loads a module under test, surviving a module that does not exist YET: RED
 * for a missing export/component must be a clean per-test assertion, never a
 * resolution error that poisons the whole file. A failure that is NOT
 * "this exact module is missing" is infra and must surface, never be masked
 * as an empty object (a swallowed import once faked five red assertions).
 */
function mod(path: string): Record<string, any> {
  try {
    return jest.requireActual(path) as Record<string, any>
  } catch (e) {
    // jest-resolve phrases a mapper miss ("Could not locate module X mapped
    // as…") differently from a plain miss ("Cannot find module X"); both mean
    // "not written yet" only when the message names THIS path — an inner
    // module's failure names itself and must surface.
    const msg = String(e)
    const missingThis =
      (msg.includes('Cannot find module') || msg.includes('Could not locate module')) && msg.includes(path)
    if (missingThis) return {}
    throw e
  }
}

const realFetch = globalThis.fetch

function ok(body: unknown) {
  return {
    ok: true,
    status: 200,
    text: async () => JSON.stringify(body),
    json: async () => body,
  }
}

function fail(status: number, body: unknown = { error: `failed ${status}` }) {
  return {
    ok: false,
    status,
    text: async () => JSON.stringify(body),
    json: async () => body,
  }
}

function assistantTurn(blocks: RestoredTurn['blocks'], promptId = 7): RestoredTurn {
  return { role: 'assistant', prompt_id: promptId, blocks }
}

const seed: TurnState = { phase: 'streaming', text: '', thinking: '', output: '', tasks: [], toolCalls: [] }

const doneFrame = {
  kind: 'done',
  data: JSON.stringify({
    status: 'success',
    message: 'Rewrote the screens.',
    files_touched: ['src/a.ts', 'src/b.ts'],
    verified: true,
    verified_best_effort: false,
    verify_policy: 'strict',
    skipped_gates: ['boot'],
    rounds: 3,
    remaining_tasks: ['vision run'],
    cost_usd: 0,
  }),
}

afterEach(() => {
  globalThis.fetch = realFetch
})

// ── M1: restored marker rebuilds the evidence footer ────────────────────────

test('F1 — restore: marker verify payload renders verified/files/rounds/policy/skips', async () => {
  const marker = {
    kind: 'turn_end',
    status: 'completed',
    rounds: 4,
    verify: {
      files_touched: ['src/App.tsx'],
      verified: true,
      verified_best_effort: false,
      verify_policy: 'strict',
      skipped_gates: ['boot', 'vision'],
    },
  }
  await render(
    <RestoredTranscript
      turns={[assistantTurn([{ kind: 'text', text: 'Done.' }, marker])]}
      projectId={1}
      onAnswer={() => {}}
    />,
  )
  expect(screen.getByText(/verified · skipped: boot, vision/)).toBeTruthy()
  expect(screen.getByText(/1 file/)).toBeTruthy()
  expect(screen.getByText(/4 rounds/)).toBeTruthy()
  expect(screen.getByText(/verify policy: strict/)).toBeTruthy()
  expect(screen.queryByText(/checks did not pass/)).toBeNull()
})

test('F2 — restore: legacy marker (no verify payload) reports rounds and claims nothing', async () => {
  await render(
    <RestoredTranscript
      turns={[
        assistantTurn([
          { kind: 'text', text: 'Legacy reply.' },
          { kind: 'turn_end', status: 'completed', rounds: 2 },
        ]),
      ]}
      projectId={1}
      onAnswer={() => {}}
    />,
  )
  expect(screen.getByText(/2 rounds/)).toBeTruthy()
  expect(screen.queryByText(/verified/)).toBeNull()
  expect(screen.queryByText(/checks did not pass/)).toBeNull()
  expect(screen.queryByText(/file/)).toBeNull()
})

test('F3 — restore: failed marker with verified:false claims the failure honestly', async () => {
  const marker = {
    kind: 'turn_end',
    status: 'failed',
    error: 'provider exploded',
    rounds: 2,
    verify: { files_touched: [], verified: false, verify_policy: 'strict', skipped_gates: [] },
  }
  await render(
    <RestoredTranscript
      turns={[assistantTurn([{ kind: 'text', text: 'Started.' }, marker])]}
      projectId={1}
      onAnswer={() => {}}
    />,
  )
  expect(screen.getByText('Turn failed · after 2 rounds')).toBeTruthy()
  expect(screen.getByText(/checks did not pass/)).toBeTruthy()
  expect(screen.getByText(/verify policy: strict/)).toBeTruthy()
})

// ── M3: live done frame maps into state and renders ─────────────────────────

test('F4a — done frame: reducer maps every verify field onto turn.evidence', async () => {
  const hooks = mod('@/hooks')
  expect(typeof hooks.reduceEvent).toBe('function')
  const st = hooks.reduceEvent(seed, doneFrame)
  expect(st.evidence).toBeDefined()
  expect(st.evidence?.filesTouched).toEqual(['src/a.ts', 'src/b.ts'])
  expect(st.evidence?.verified).toBe(true)
  expect(st.evidence?.verifiedBestEffort).toBe(false)
  expect(st.evidence?.verifyPolicy).toBe('strict')
  expect(st.evidence?.skippedGates).toEqual(['boot'])
  expect(st.evidence?.rounds).toBe(3)
  expect(st.evidence?.remainingTasks).toEqual(['vision run'])
  expect(st.phase).toBe('done')
})

test('F4b — done frame: adopts the frame message when the turn streamed no words', async () => {
  const hooks = mod('@/hooks')
  expect(typeof hooks.reduceEvent).toBe('function')
  const st = hooks.reduceEvent(seed, doneFrame)
  expect(st.text).toBe('Rewrote the screens.')
})

test('F5 — live: VerifyEvidenceRow renders the reducer output (stitch done → footer)', async () => {
  const hooks = mod('@/hooks')
  const comps = mod('@/components/build/VerifyEvidence')
  const Row = comps.VerifyEvidenceRow
  if (typeof Row !== 'function') {
    throw new Error(
      'F5/M3: components/build/VerifyEvidence.tsx (VerifyEvidenceRow) missing — the live done frame has no row to render into',
    )
  }
  const st = hooks.reduceEvent(seed, doneFrame)
  await render(<Row evidence={st.evidence} />)
  expect(screen.getByText(/verified · skipped: boot/)).toBeTruthy()
  expect(screen.getByText(/2 files/)).toBeTruthy()
  expect(screen.getByText(/3 rounds/)).toBeTruthy()
  expect(screen.getByText(/1 left/)).toBeTruthy()
  expect(screen.getByText(/verify policy: strict/)).toBeTruthy()
  expect(screen.getByText(/did not run: boot/)).toBeTruthy()
})

// ── M4: marker-less newest turn seeds from the prompt-row union ─────────────

test('F6a — promptsApi.get exists and carries the files_touched union', async () => {
  const api = mod('@/lib/api')
  expect(typeof api.promptsApi?.get).toBe('function')
  const fetchMock = jest.fn().mockResolvedValue(ok({ id: 9, status: 'running', round: 4, files_touched: ['src/x.ts'] }))
  globalThis.fetch = fetchMock as unknown as typeof fetch
  const row = await api.promptsApi.get(9)
  expect(row.files_touched).toEqual(['src/x.ts'])
})

test('F6b — restore: marker-less turn renders the union seed and claims no checks', async () => {
  await render(
    <RestoredTranscript
      turns={[assistantTurn([{ kind: 'text', text: 'Still working…' }], 9)]}
      projectId={1}
      onAnswer={() => {}}
      promptEvidence={{ 9: { files_touched: ['src/x.ts'] } }}
    />,
  )
  expect(screen.getByText(/1 file/)).toBeTruthy()
  expect(screen.queryByText(/verified/)).toBeNull()
  expect(screen.queryByText(/checks did not pass/)).toBeNull()
})

// ── M5: verify-state badge claims only what a 200 supports ──────────────────

test('F7 — verifyBadge rule: null for absence/refused, "{ago}" for passing sweeps', async () => {
  const api = mod('@/lib/api')
  expect(typeof api.verifyBadge).toBe('function')
  expect(api.verifyBadge(null)).toBeNull()
  expect(
    api.verifyBadge({
      project_id: 1,
      status: 'refused',
      policy: 'strict',
      skipped: [],
      remaining: ['boot'],
      reason: 'gates unavailable',
      updated_at: new Date().toISOString(),
    }),
  ).toBeNull()
  expect(
    api.verifyBadge({
      project_id: 1,
      status: 'verified',
      policy: 'strict',
      skipped: [],
      remaining: [],
      reason: '',
      updated_at: new Date().toISOString(),
    }),
  ).toBe('just now')
  expect(
    api.verifyBadge({
      project_id: 1,
      status: 'best-effort',
      policy: 'strict',
      skipped: ['vision'],
      remaining: [],
      reason: '',
      updated_at: new Date(Date.now() - 5 * 60_000).toISOString(),
    }),
  ).toBe('5m ago')
})

test('F8 — verifyState.get: 404 → null (no claim), 200 → row, 500 → throws', async () => {
  const api = mod('@/lib/api')
  expect(typeof api.verifyState?.get).toBe('function')
  const fetchMock = jest.fn()
  globalThis.fetch = fetchMock as unknown as typeof fetch

  fetchMock.mockResolvedValueOnce(fail(404, { error: 'never verified' }))
  expect(await api.verifyState.get(1)).toBeNull()

  fetchMock.mockResolvedValueOnce(
    ok({
      project_id: 1,
      status: 'best-effort',
      policy: 'strict',
      skipped: ['vision'],
      remaining: [],
      reason: '',
      updated_at: new Date().toISOString(),
    }),
  )
  const row = await api.verifyState.get(1)
  expect(row?.status).toBe('best-effort')

  fetchMock.mockResolvedValueOnce(fail(500))
  await expect(api.verifyState.get(1)).rejects.toThrow()
})

test('F9 — VerifyBadge: renders "Build verified · {ago}" on a 200, nothing on 404', async () => {
  const comps = mod('@/components/build/VerifyBadge')
  const BadgeComponent = comps.VerifyBadge
  if (typeof BadgeComponent !== 'function') {
    throw new Error(
      'F9/M5: components/build/VerifyBadge.tsx (VerifyBadge) missing — the project-level sweep claim has nowhere to live',
    )
  }
  // gcTime 0: the default 5-minute cache timer is an open handle that keeps
  // jest alive after the run ("did not exit one second after…"). Cleanup
  // unmounts observers; gcTime 0 drops the cache entry with them.
  const client = () => new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } })

  // 200 + passing → the claim. Scoped to this render's own result so the next
  // case's absence check cannot be poisoned by a sibling container.
  const fetch200 = jest.fn().mockResolvedValue(
    ok({
      project_id: 1,
      status: 'best-effort',
      policy: 'strict',
      skipped: ['vision'],
      remaining: [],
      reason: '',
      updated_at: new Date().toISOString(),
    }),
  )
  globalThis.fetch = fetch200 as unknown as typeof fetch
  const passing = await render(
    <QueryClientProvider client={client()}>
      <BadgeComponent projectId={1} />
    </QueryClientProvider>,
  )
  expect(await passing.findByText('Build verified · just now')).toBeTruthy()
  passing.unmount()

  // 404 → nothing claimed, and the fetch really happened (not a loading-pass).
  const fetch404 = jest.fn().mockResolvedValue(fail(404, { error: 'never verified' }))
  globalThis.fetch = fetch404 as unknown as typeof fetch
  const absent = await render(
    <QueryClientProvider client={client()}>
      <BadgeComponent projectId={2} />
    </QueryClientProvider>,
  )
  await waitFor(() => expect(fetch404).toHaveBeenCalled())
  expect(absent.queryByText(/Build verified/)).toBeNull()
})
