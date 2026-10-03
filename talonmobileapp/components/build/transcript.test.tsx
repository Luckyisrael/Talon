import React from 'react'
import { render, screen } from '@testing-library/react-native'
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
