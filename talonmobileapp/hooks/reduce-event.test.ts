import { reduceEvent, type TurnState } from '@/hooks'
import { isResumable, normalizePlan, type StreamEvent } from '@/lib/api'

// Mobile ↔ web parity: the frames the reducer used to drop or mislabel.
//
// P1 plan event dropped (default case): plan mode streamed the model's raw
//    narration — often the JSON envelope itself — and never the parsed plan,
//    so there was nothing to read and nothing to approve. The reducer must
//    adopt plan + message and REPLACE the narration (the web's canonical-
//    reply rule): text becomes the message, thinking is cleared.
// P2 question choices dropped: the live card guessed ['Yes, proceed','No,
//    stop'] while the model actually asked MWA-vs-email. Choices ride the
//    frame and must reach the card; absence falls back to [] (the card's
//    default pair), never a crash.
// P3 done{waiting} flipped phase to 'done': the frame that closes the SSE
//    stream unmounted the live question card the moment it landed. The pause
//    IS the turn's state — waiting must survive so the answer continues THIS
//    turn (the web keeps its running prompt identity the same way).
// P4 done{failed} landed as phase 'done': the header read "turn finished"
//    over a turn that died, and nothing downstream offered Resume. Failed is
//    an error frame; a prior friendly `error` message wins over the raw one.
// P5 guardrail_reject dropped: the model was blocked and the screen said
//    nothing at all. A visible "Blocked" row carries the refusal's first line.
// P6 write_adr / write_memory tool rows doubled the decision card (and the
//    decision itself was dropped): one recorded decision renders once, with
//    its title.
// P7 isResumable is the banner gate: four build statuses admit; plan mode,
//    waiting (answers via /answer), terminal states, and absence do not.
// P8 normalizePlan gives the live card and the restored card one shape from
//    an untyped payload (SSE JSON, or a stored history block after reload).
// P9 task_update upserts by id: the seed blast fills the checklist, later
//    updates rewrite the same row — never a duplicate, never a spinner.
//
// Harness notes: reduceEvent parses ev.data as a JSON STRING (openStream's
// wire shape), and @/hooks transitively imports expo-secure-store +
// AsyncStorage — both mocked below so the suite runs without a keystore.

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

const seed: TurnState = { phase: 'streaming', text: '', thinking: '', output: '', tasks: [], toolCalls: [] }

function ev(kind: string, data: Record<string, unknown>): StreamEvent {
  return { kind, data: JSON.stringify(data) }
}

// ── P1: the plan frame is the canonical reply ────────────────────────────────

test('P1a — plan event: adopts the parsed plan and replaces narration with the message', () => {
  const st = reduceEvent(
    { ...seed, thinking: 'raw model reasoning...', text: '{"thinking":"..."' },
    ev('plan', {
      message: 'Two screens: Home and Wallet.',
      plan: {
        screens: [{ name: 'Home', description: 'Entry point' }],
        features: ['send tokens'],
        navigation: 'bottom tabs',
        packages: ['@solana/kit'],
      },
    }),
  )
  expect(st.plan).toEqual({
    screens: [{ name: 'Home', description: 'Entry point' }],
    features: ['send tokens'],
    navigation: 'bottom tabs',
    packages: ['@solana/kit'],
  })
  expect(st.planMessage).toBe('Two screens: Home and Wallet.')
  expect(st.text).toBe('Two screens: Home and Wallet.')
  expect(st.thinking).toBe('')
})

test('P1b — plan event without a message still answers honestly', () => {
  const st = reduceEvent(seed, ev('plan', { plan: { screens: [], features: [] } }))
  expect(st.planMessage).toBe('Plan ready.')
  expect(st.text).toBe('Plan ready.')
})

// ── P2/P3: the question pause survives its own closing frame ─────────────────

test('P2 — question frame: choices ride along; absence falls back to []', () => {
  const asked = reduceEvent(
    seed,
    ev('question', { question: 'MWA or email?', prompt_id: 9, choices: ['MWA', 'Email link'] }),
  )
  expect(asked.phase).toBe('waiting')
  expect(asked.question).toBe('MWA or email?')
  expect(asked.questionChoices).toEqual(['MWA', 'Email link'])

  const silent = reduceEvent(seed, ev('question', { question: 'Go?', prompt_id: 9 }))
  expect(silent.questionChoices).toEqual([])
})

test('P3 — done{waiting}: the pause survives the frame that closes the stream', () => {
  const asked = reduceEvent(seed, ev('question', { question: 'Ship it?', prompt_id: 9, choices: ['Yes'] }))
  const closed = reduceEvent(asked, ev('done', { status: 'waiting' }))
  expect(closed.phase).toBe('waiting')
  expect(closed.question).toBe('Ship it?')
  expect(closed.questionChoices).toEqual(['Yes'])
})

// ── P4: failure is an error, not a finish ────────────────────────────────────

test('P4a — done{failed}: phase error with the frame message', () => {
  const st = reduceEvent(seed, ev('done', { status: 'failed', message: 'provider exploded' }))
  expect(st.phase).toBe('error')
  expect(st.error).toBe('provider exploded')
})

test('P4b — done{failed} after a friendly error: the friendly line wins', () => {
  const errored = reduceEvent(seed, ev('error', { message: 'The model had a wobble — try again.' }))
  const st = reduceEvent(errored, ev('done', { status: 'failed', message: 'raw upstream 500' }))
  expect(st.phase).toBe('error')
  expect(st.error).toBe('The model had a wobble — try again.')
})

test('P4c — done{success} still finishes the turn', () => {
  const st = reduceEvent(seed, ev('done', { status: 'success', message: 'Done.' }))
  expect(st.phase).toBe('done')
  expect(st.text).toBe('Done.')
})

// ── P5/P6: refusals and decisions are visible, once each ─────────────────────

test('P5 — guardrail_reject: a Blocked row carrying the refusal line', () => {
  const st = reduceEvent(seed, ev('guardrail_reject', { tool: 'rm -rf', error: 'blocked: destructive\ncontext' }))
  expect(st.toolCalls).toEqual([{ name: 'Blocked', status: 'error', target: 'blocked: destructive' }])
})

test('P6 — write_adr / write_memory are suppressed; the decision renders once, titled', () => {
  const suppressedTool = reduceEvent(seed, ev('tool_call', { name: 'write_memory', status: 'in_progress' }))
  expect(suppressedTool).toBe(seed)

  const withAdr = reduceEvent(seed, ev('tool_call', { name: 'write_adr', status: 'completed' }))
  const st = reduceEvent(
    withAdr,
    ev('decision', { variant: 'adr', decision: 'Use Solana Kit for accounts', category: 'stack' }),
  )
  expect(st.toolCalls).toEqual([{ name: 'Recorded ADR', status: 'completed', target: 'Use Solana Kit for accounts' }])

  const memory = reduceEvent(seed, ev('decision', { variant: 'memory', content: 'User prefers dark mode\nmore' }))
  expect(memory.toolCalls).toEqual([{ name: 'Recorded memory', status: 'completed', target: 'User prefers dark mode' }])
})

// ── P7: the resume/discard banner gate ───────────────────────────────────────

test('P7 — isResumable: four build statuses admit; waiting, plan mode and absence do not', () => {
  for (const status of ['running', 'cancelled', 'failed', 'paused']) {
    expect(isResumable({ id: 3, mode: 'build', status })).toBe(true)
  }
  expect(isResumable({ id: 3, mode: 'build', status: 'waiting' })).toBe(false)
  expect(isResumable({ id: 3, mode: 'build', status: 'completed' })).toBe(false)
  expect(isResumable({ id: 3, mode: 'build', status: 'abandoned' })).toBe(false)
  expect(isResumable({ id: 3, mode: 'plan', status: 'failed' })).toBe(false)
  expect(isResumable(null)).toBe(false)
  expect(isResumable(undefined)).toBe(false)
})

// ── P8: one plan shape for the live card and the restored card ───────────────

test('P8 — normalizePlan: coerces junk into PlanData', () => {
  expect(normalizePlan(null)).toEqual({ screens: [], features: [], navigation: '', packages: [] })
  expect(
    normalizePlan({
      screens: [{ name: 'Home' }, 'junk', null],
      features: [1, 'send tokens'],
      navigation: 42,
      packages: undefined,
    }),
  ).toEqual({
    screens: [
      { name: 'Home', description: '' },
      { name: '', description: '' },
      { name: '', description: '' },
    ],
    features: ['send tokens'],
    navigation: '',
    packages: [],
  })
})

// ── P9: the seed blast fills the checklist in place ──────────────────────────

test('P9 — task_update upserts by id: one row, rewritten, never duplicated', () => {
  const seeded = reduceEvent(seed, ev('task_update', { id: 'plan:1', description: 'Wire auth', status: 'pending' }))
  expect(seeded.tasks).toHaveLength(1)
  expect(seeded.tasks[0]).toMatchObject({ id: 'plan:1', status: 'pending' })

  const flipped = reduceEvent(seeded, ev('task_update', { id: 'plan:1', status: 'completed', result: 'ok' }))
  expect(flipped.tasks).toHaveLength(1)
  expect(flipped.tasks[0]).toMatchObject({ id: 'plan:1', status: 'completed', result: 'ok' })
})
