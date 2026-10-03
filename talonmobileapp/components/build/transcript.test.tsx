import React from 'react'
import { act, fireEvent, render, screen } from '@testing-library/react-native'
import { RestoredTranscript, type RestoredTurn } from '@/components/build/Transcript'

// L52e / FM-TLKa — fails-first: the restore walks stored blocks in stored
// order, thinking included.
//
// The gaps, all on camera:
//   · thinking IS persisted (271 rows in the live DB carry
//     type:"thinking") but the restore filtered only text/tool_use — a
//     reload dropped every thought the turn ever had;
//   · narration was hoisted above the tools it narrates and the plan was
//     pinned between them — fixed slots again, the same chronology loss
//     the live tail had;
//   · a stale "Turn cancelled" marker from an interrupted attempt outlived
//     the completed marker that superseded it — two endings for one turn.
//
// Harness notes (from verify-evidence.test.tsx, which renders the same
// tree): RTL v14 render() is async; reanimated boots native Worklets at
// import time through TraceRow — the shared seam in test/native-seams is
// the only mock that does not break the chain.

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
jest.mock('react-native-reanimated', () => require('@/test/native-seams').reanimated())

function renderTurns(turns: RestoredTurn[]) {
  return render(<RestoredTranscript turns={turns} projectId={1} onAnswer={() => {}} />)
}

function assistant(blocks: RestoredTurn['blocks'], promptId?: number): RestoredTurn {
  return { role: 'assistant', prompt_id: promptId, blocks }
}

test('L52i — a stored thinking block restores as a collapsed Thought row', async () => {
  await renderTurns([assistant([{ kind: 'thinking', text: 'let me look at how x is structured' }])])

  expect(screen.getByText(/Thought for/)).toBeTruthy()
  // Collapsed by default — the restore never walls the screen either.
  expect(screen.queryByText(/how x is structured/)).toBeNull()
})

test('L52j — stored order: a tool row before its narration renders before it', async () => {
  await renderTurns([
    assistant([
      { kind: 'tool_use', id: 't1', name: 'write_file', input: { path: 'app/wallet.tsx' } },
      { kind: 'text', text: 'Wallet written.' },
    ]),
  ])

  const flat = JSON.stringify(screen.toJSON())
  const toolAt = flat.indexOf('Created') // write_file's restored label
  const wordsAt = flat.indexOf('Wallet written.')
  expect(toolAt).toBeGreaterThanOrEqual(0)
  expect(wordsAt).toBeGreaterThanOrEqual(0)
  // Pre-fix the narration was hoisted above the tool — the order inverted.
  expect(toolAt).toBeLessThan(wordsAt)
})

test('L52k — only the newest marker per prompt renders: a stale cancelled row loses', async () => {
  await renderTurns([
    assistant(
      [
        { kind: 'text', text: 'partial work.' },
        { kind: 'turn_end', status: 'cancelled', rounds: 1 },
      ],
      5,
    ),
    assistant(
      [
        { kind: 'text', text: 'resumed and finished.' },
        {
          kind: 'turn_end',
          status: 'completed',
          rounds: 2,
          verify: {
            files_touched: ['src/a.ts'],
            verified: true,
            verify_policy: 'strict',
            skipped_gates: [],
          },
        },
      ],
      5,
    ),
  ])

  expect(screen.queryByText(/Turn cancelled/)).toBeNull()
  expect(screen.getByText(/resumed and finished\./)).toBeTruthy()
  expect(screen.getByText(/verified/)).toBeTruthy()
})

test('L52k2 — a prompt with a single marker keeps its ending (polarity)', async () => {
  await renderTurns([
    assistant(
      [
        { kind: 'text', text: 'stopped early.' },
        { kind: 'turn_end', status: 'cancelled', rounds: 1 },
      ],
      6,
    ),
  ])
  expect(screen.getByText(/Turn cancelled/)).toBeTruthy()
})

// L56a / FM-ANSQa — fails-first: a plan-mode question waits as PLAIN TEXT.
// The plan path emits done{status:"waiting"} with no `question` frame and
// stores the exchange text-only (persistPlanExchange + planJSON(nil)), so
// history has no ask_user block — while the composer gate blocks every send
// with "answer the question in the transcript first". Without this card
// there is nowhere to answer: the exact stranded state behind prompt 84
// (mode=plan, status=waiting, blocks = user|text + assistant|text).
test('L56a — a waiting plan-question turn (no ask_user block) renders its answer card', async () => {
  const onAnswer = jest.fn()
  await render(
    <RestoredTranscript
      turns={[
        { role: 'user', prompt_id: 84, blocks: [{ kind: 'text', text: 'Build me a wallet tracking app' }] },
        assistant(
          [{ kind: 'text', text: 'Whose wallets are we tracking? Just yours, or a team?' }],
          84,
        ),
      ]}
      projectId={1}
      waitingPromptId={84}
      onAnswer={onAnswer}
    />,
  )

  // RED today: the card does not exist — no answer surface at all.
  // RTL v14 seam: each event runs INSIDE act — a changeText left to a later
  // empty-act flush unmounts the next test's render (bisected on a two-test
  // ladder), and an unact'd reply() leaks the same way. act-wrapped, the
  // controlled Field carries the new value into submitEditing's closure.
  await act(async () => {
    fireEvent.changeText(screen.getByPlaceholderText('Or type your own answer…'), 'Just mine')
  })
  await act(async () => {
    fireEvent(screen.getByPlaceholderText('Or type your own answer…'), 'submitEditing')
  })
  expect(onAnswer).toHaveBeenCalledWith(84, 'Just mine')
})

// L56b / FM-ANSQa — fails-first: the restored card used to fire on ANY
// turn's ask_user while ANY prompt waited — an old, long-answered question
// re-opened as a live answer box keyed to the newest waiting prompt. The
// web pins this to the last turn (isLastTurn); the wait owns exactly one
// surface, on the turn that carries it.
test('L56b — an older turn\u2019s ask_user does not re-open while a newer prompt waits', async () => {
  const onAnswer = jest.fn()
  await render(
    <RestoredTranscript
      turns={[
        { role: 'user', prompt_id: 5, blocks: [{ kind: 'text', text: 'Start with the skeleton' }] },
        assistant(
          [
            { kind: 'text', text: 'One thing first.' },
            {
              kind: 'tool_use',
              id: 't1',
              name: 'ask_user',
              input: { question: 'Ship the old design?', choices: ['Yes'] },
            },
            { kind: 'tool_result', tool_use_id: 't1', content: 'Yes' },
          ],
          5,
        ),
        { role: 'user', prompt_id: 84, blocks: [{ kind: 'text', text: 'Build me a wallet tracker' }] },
        assistant([{ kind: 'text', text: 'Which wallets are we tracking?' }], 84),
      ]}
      projectId={1}
      waitingPromptId={84}
      onAnswer={onAnswer}
    />,
  )

  // RED today: the answered question re-renders as a live card.
  expect(screen.queryByText('Ship the old design?')).toBeNull()
  // Exactly one answer surface — the waiting turn's own.
  expect(screen.getAllByPlaceholderText('Or type your own answer…')).toHaveLength(1)
})
