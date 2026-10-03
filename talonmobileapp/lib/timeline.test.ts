// L53c / FM-RSUMa — fails-first: resume picks up exactly where it stopped.
//
// The gap: resume() reset the turn to a blank tail, so the interrupted
// turn's stored rounds vanished from view the moment you pressed Resume —
// the stream continued into an empty screen even when it was visible.
// seedFromBlocks rebuilds the settled timeline from the prompt's own
// stored blocks — thinking as collapsed segments, tools in place with
// their outcomes, decisions reconstructed the way the web rebuilds them
// (input.decision → "Recorded ADR"), narration in order — so the live
// continuation appends onto the history instead of replacing it.
// Stale plumbing never seeds: tool_result (outcomes ride the map),
// ask_user (the tail owns the card) and turn_end (the newest-wins marker
// rule owns the ending).
//
// Pre-fix this module does not exist — the module-resolution failure IS
// the red: there is no continuation to test before there is a seeder.

import { seedFromBlocks } from '@/lib/timeline'
import type { ConversationBlock } from '@/lib/api'

test('L53c1 — seeds the stored blocks in order as settled timeline rows', () => {
  const blocks: ConversationBlock[] = [
    { kind: 'thinking', text: 'let me look at how x is structured' },
    { kind: 'tool_use', id: 't1', name: 'read_file', input: { path: 'app/index.tsx' } },
    { kind: 'tool_use', id: 'q1', name: 'ask_user', input: { question: 'Ship it?' } },
    { kind: 'text', text: 'Wiring the tabs now.' },
    { kind: 'tool_use', id: 't2', name: 'write_adr', input: { decision: 'Use Solana Kit\nreasons' } },
    { kind: 'tool_result', tool_use_id: 't1', content: 'file bytes' },
    { kind: 'turn_end', status: 'cancelled', rounds: 1 },
  ]

  const items = seedFromBlocks(blocks)
  expect(items.map((i) => i.kind)).toEqual(['thinking', 'tool', 'narration', 'decision'])
  expect(items[0]).toMatchObject({ text: 'let me look at how x is structured', streaming: false })
  expect(items[1]).toMatchObject({ name: 'read_file', status: 'completed', target: 'app/index.tsx' })
  expect(items[3]).toMatchObject({ name: 'Recorded ADR', summary: 'Use Solana Kit\nreasons' })
})

test('L53c2 — a tool_result outcome seeds the row’s real status', () => {
  const blocks: ConversationBlock[] = [
    { kind: 'tool_use', id: 't1', name: 'run_lint', input: { command: 'npm run lint' } },
    { kind: 'tool_result', tool_use_id: 't1', is_error: true, content: 'boom' },
  ]
  const outcomes = new Map<string, boolean>([['t1', true]])
  const items = seedFromBlocks(blocks, outcomes)
  expect(items).toHaveLength(1)
  expect(items[0]).toMatchObject({ kind: 'tool', name: 'run_lint', status: 'error', target: 'npm run lint' })
})

test('L53c3 — blank thinking and empty text seed nothing (no empty rows on resume)', () => {
  const items = seedFromBlocks([
    { kind: 'thinking', text: '   ' },
    { kind: 'text', text: '' },
  ])
  expect(items).toEqual([])
})
