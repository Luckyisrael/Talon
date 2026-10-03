// L53 / FM-RSUMa — fails-first: a resumed turn streams where the user can
// see it.
//
// The gap, as reported ("when I press resume it doesn't stream") and as
// proved server-side (`POST /v1/prompts/75/resume bytes=69375` streamed
// while the phone showed nothing): `liveVisible` retired the tail the
// instant `historyHasTurn` matched — and a resumed prompt id is already in
// history, so the first phase frame hid every frame that followed.
//
// Two pure rules fix it together:
//   isTailVisible     — streaming/waiting NEVER retire, regardless of
//                       history; done/error retire only once history has
//                       absorbed the turn (the original no-duplicate gate).
//   excludeLivePrompt — while that tail shows, the restored transcript
//                       drops the live prompt's rows so nothing renders
//                       twice: assistant rows always (the tail seeds them),
//                       the user's bubble only when the tail echoes it
//                       (a send echoes; a resume/answer does not — the
//                       stored bubble is all there is).
//
// Pre-fix this module does not exist — the module-resolution failure IS
// the red: the 69 KB of invisible streaming IS these two rules answered
// wrongly, pinned here as pure functions.

import { excludeLivePrompt, isTailVisible } from '@/lib/turn-visibility'
import type { RestoredTurn } from '@/components/build/Transcript'

test('L53a — the tail stays visible while streaming/waiting even when history holds the prompt', () => {
  // The resume state itself: history already contains prompt 82.
  expect(isTailVisible('streaming', true)).toBe(true)
  expect(isTailVisible('waiting', true)).toBe(true)
  // Fresh send: history cannot have it yet — same answer, both paths.
  expect(isTailVisible('streaming', false)).toBe(true)
  expect(isTailVisible('waiting', false)).toBe(true)
  // Settled: retire exactly when the durable log absorbed the turn…
  expect(isTailVisible('done', true)).toBe(false)
  expect(isTailVisible('error', true)).toBe(false)
  // …and keep the brief settled window when it has not (before refetch).
  expect(isTailVisible('done', false)).toBe(true)
  expect(isTailVisible('error', false)).toBe(true)
  // Nothing in flight → nothing to show.
  expect(isTailVisible('idle', false)).toBe(false)
  expect(isTailVisible('idle', true)).toBe(false)
})

function turn(role: string, promptId: number, text: string): RestoredTurn {
  return { role, prompt_id: promptId, blocks: [{ kind: 'text', text }] }
}

const history: RestoredTurn[] = [
  turn('user', 82, 'keep building the wallet'),
  turn('assistant', 82, 'I was mid-build'),
  turn('user', 81, 'the earlier prompt'),
]

test('L53b — while the tail shows, the restored transcript excludes that prompt’s rows', () => {
  // Resume/answer: no echo in the tail — the stored user bubble stays put.
  const resumed = excludeLivePrompt(history, 'streaming', 82, false)
  expect(resumed.map((t) => `${t.role}:${t.prompt_id}`)).toEqual(['user:82', 'user:81'])

  // Send: the tail echoes the prompt — both of its rows step aside.
  const sent = excludeLivePrompt(history, 'streaming', 82, true)
  expect(sent.map((t) => t.prompt_id)).toEqual([81])

  // Settled: the filter releases together with the tail (no lost rows).
  expect(excludeLivePrompt(history, 'done', 82, true)).toHaveLength(3)
  expect(excludeLivePrompt(history, 'error', 82, false)).toHaveLength(3)

  // Nothing named (first frames of a fresh send) → untouched.
  expect(excludeLivePrompt(history, 'streaming', undefined, true)).toHaveLength(3)
  expect(excludeLivePrompt(history, 'idle', 82, true)).toHaveLength(3)
})
