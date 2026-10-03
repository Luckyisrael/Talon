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

// ── L51 / FM-THINKa: thinkingActive is the "is it thinking right now" signal ──
//
// The ThinkingRow's label must distinguish a live burst from a finished one.
// The reducer is the only place that knows which frame arrived last, so the
// signal lives there: thinking.delta sets it, any content frame other than
// another thinking delta (or a phase frame — that names the round, not
// output) clears it. Pre-fix the field does not exist: the label would have
// to guess from unrelated state and be wrong in both directions.

test('L51a — thinking.delta marks thinking as the active activity', () => {
  const st = reduceEvent(seed, ev('thinking.delta', { text: 'first inspect the wallet kit' }))
  expect(st.thinking).toContain('first inspect the wallet kit')
  expect(st.thinkingActive).toBe(true)
})

test('L51b — a content frame ends the burst: thinkingActive clears', () => {
  const thinking = reduceEvent(seed, ev('thinking.delta', { text: 'planning the tip flow' }))
  expect(thinking.thinkingActive).toBe(true)

  const afterText = reduceEvent(thinking, ev('text.delta', { text: 'Here is the plan.' }))
  expect(afterText.thinkingActive).toBe(false)

  const afterTool = reduceEvent(thinking, ev('tool_call', { name: 'write_file', status: 'running' }))
  expect(afterTool.thinkingActive).toBe(false)
})

test('L51c — phase frames do NOT end the burst (they name the round, not output)', () => {
  const thinking = reduceEvent(seed, ev('thinking.delta', { text: 'weighing two designs' }))
  const phased = reduceEvent(thinking, ev('phase', { phase: 'GENERATE', prompt_id: 7 }))
  expect(phased.thinkingActive).toBe(true)
  expect(phased.promptId).toBe(7)
})

// ── L52 / FM-TLKa: one chronological timeline, appended in arrival order ────
//
// The fixed-bucket reducer could not interleave: reasoning pinned above the
// trace no matter when it arrived, paired running/completed frames appended
// twice (phantom spinners), narration held apart from the tools it narrates.
// `timeline` is the ordered render source; the legacy thinking/toolCalls/text
// fields keep updating exactly as the L51/P-series tests pin them.

test('L52a — thinking after a tool call opens a NEW segment at the arrival position', () => {
  const t1 = reduceEvent(seed, ev('thinking.delta', { text: 'now let me look at how x is structured' }))
  const withTool = reduceEvent(t1, ev('tool_call', { id: 'call_1', name: 'read_file', status: 'running' }))
  const t2 = reduceEvent(withTool, ev('thinking.delta', { text: 'the file is fine' }))

  expect(t2.timeline?.map((i) => i.kind)).toEqual(['thinking', 'tool', 'thinking'])
  // The tool frame settles the first segment; the new one is live.
  expect(t2.timeline?.[0]).toMatchObject({ streaming: false })
  expect(t2.timeline?.[2]).toMatchObject({ text: 'the file is fine', streaming: true })
})

test('L52b — paired running/completed frames settle ONE row in place', () => {
  const running = reduceEvent(seed, ev('tool_call', { id: 'call_9', name: 'run_command', status: 'running' }))
  const settled = reduceEvent(running, ev('tool_call', { id: 'call_9', name: 'run_command', status: 'completed' }))

  const tools = settled.timeline?.filter((i) => i.kind === 'tool')
  expect(tools).toHaveLength(1)
  expect(tools?.[0]).toMatchObject({ status: 'completed' })
  // An id-less frame never collides with a keyed one: it appends.
  const second = reduceEvent(settled, ev('tool_call', { name: 'read_skill', status: 'completed' }))
  expect(second.timeline?.filter((i) => i.kind === 'tool')).toHaveLength(2)
})

test('L52c — narration folds into its segment; a decision lands after it', () => {
  const a = reduceEvent(seed, ev('text.delta', { text: 'Hello ' }))
  const b = reduceEvent(a, ev('text.delta', { text: 'world' }))
  expect(b.timeline).toHaveLength(1)
  expect(b.timeline?.[0]).toMatchObject({ kind: 'narration', text: 'Hello world' })

  const c = reduceEvent(b, ev('decision', { variant: 'adr', decision: 'Use Solana Kit for accounts' }))
  expect(c.timeline?.map((i) => i.kind)).toEqual(['narration', 'decision'])
  expect(c.timeline?.[1]).toMatchObject({
    name: 'Recorded ADR',
    target: 'Use Solana Kit for accounts',
    summary: 'Use Solana Kit for accounts',
  })
})

test('L52d — command output attaches behind the running tool and extends in place', () => {
  const tool = reduceEvent(seed, ev('tool_call', { id: 'c1', name: 'run_command', status: 'running' }))
  const out = reduceEvent(tool, ev('command_output', { text: 'compiling…' }))
  expect(out.timeline?.map((i) => i.kind)).toEqual(['tool', 'output'])

  const more = reduceEvent(out, ev('command_output', { text: '\ndone' }))
  expect(more.timeline).toHaveLength(2)
  expect(more.timeline?.[1]).toMatchObject({ text: expect.stringContaining('done') })
  // Legacy output keeps its bounded-tail contract.
  expect(more.output).toContain('done')
})

test('L52e — suppressed write_adr/write_memory stay suppressed; skills_group lands as a chip row', () => {
  const suppressed = reduceEvent(seed, ev('tool_call', { name: 'write_adr', status: 'completed' }))
  expect(suppressed.timeline?.some((i) => i.kind === 'tool')).toBeFalsy()

  // The wire carries the group as a bare JSON array — same parse path as every frame.
  const skilled = reduceEvent(seed, ev('skills_group', ['expo-router', 'expo-secure-store'] as unknown as Record<string, unknown>))
  expect(skilled.timeline?.[0]).toMatchObject({ kind: 'skill', title: 'expo-router, expo-secure-store' })
})

// ── L54 / FM-VRFYa: a done frame with no verdict builds no claim ────────────
//
// Every emitter shipped `verified:false` whenever no check ran — the frame
// the phone rendered as "checks did not pass · 1 round" on EVERY turn.
// Absence is the no-claim state: no verdict, no files, nothing else worth
// reporting → evidence stays undefined and the footer row never mounts.

test('L54b — a no-checks done frame builds NO evidence (the false claim itself)', () => {
  const st = reduceEvent(
    seed,
    ev('done', { status: 'success', rounds: 1, files_touched: [], verify_policy: '', message: 'Nothing to change.' }),
  )
  expect(st.evidence).toBeUndefined()
})

test('L54b2 — a genuine failure keeps its claim: files + policy ride the verdict', () => {
  const st = reduceEvent(
    seed,
    ev('done', { status: 'success', rounds: 2, files_touched: ['src/a.ts'], verified: false, verify_policy: 'strict' }),
  )
  expect(st.evidence?.verified).toBe(false)
  expect(st.evidence?.filesTouched).toEqual(['src/a.ts'])
  expect(st.evidence?.rounds).toBe(2)
})

test('L54b3 — a paused turn reports files but claims no verdict (checks never finished)', () => {
  const st = reduceEvent(seed, ev('done', { status: 'paused', rounds: 3, files_touched: ['src/a.ts'] }))
  expect(st.evidence?.verified).toBeUndefined()
  expect(st.evidence?.filesTouched).toEqual(['src/a.ts'])
})

// ── L56c / FM-ANSQa: a plan-less plan frame is a QUESTION reply ─────────────
//
// The plan path emits its `plan` event with `message` and NO `plan` when the
// model asks instead of settling (planAsksQuestion). normalizePlan(absence)
// produced an empty-but-truthy PlanData, so an empty PlanCard with "Approve &
// build" rendered over the question — approve would ship an empty PLAN.md.
// The web guards d.plan ? … : null; the card may only exist when a plan does.

test('L56c — plan event without a plan key adopts no plan (question reply)', () => {
  const st = reduceEvent(seed, ev('plan', { message: 'Whose wallets are we tracking?' }))
  expect(st.plan).toBeUndefined()
  // The message still lands as the canonical reply — the question shows.
  expect(st.text).toBe('Whose wallets are we tracking?')
})
