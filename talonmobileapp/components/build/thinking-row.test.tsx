// L51 / FM-THINKa — fails-first: the reasoning a turn produces must arrive
// as ONE collapsed row, never as the raw text wall the user caught on
// camera ("you are not supposed to show the user the full thinking text").
//
// The researched pattern (CopilotKit's reasoning card, Claude Code's
// collapsed stub, DeepSeek's ReasoningRow): while the model thinks the row
// reads "Thinking… <elapsed>"; when the burst settles it freezes to
// "Thought for <elapsed>"; the full text exists only behind a tap.
//
// Pre-fix this module does not exist — the module-resolution failure IS the
// red (the same accepted form as the keyboard-avoidance row): the collapsed
// default cannot exist before the component does.

import React from 'react'
import { fireEvent, render, screen } from '@testing-library/react-native'
import { ThinkingRow } from '@/components/build/ThinkingRow'

const REASONING =
  'First I will inspect the wallet kit, then wire the tip flow end to end before touching the rewards ledger.'

test('L51d — collapsed by default: the reasoning text is not on screen', async () => {
  await render(<ThinkingRow text={REASONING} running={false} />)

  // The header carries the collapsed state…
  expect(screen.getByText(/Thought for/)).toBeTruthy()
  // …and the raw reasoning wall stays off screen until asked for.
  expect(screen.queryByText(/inspect the wallet kit/)).toBeNull()
})

test('L51e — one tap expands the full reasoning, one tap collapses it again', async () => {
  await render(<ThinkingRow text={REASONING} running={false} />)

  await fireEvent.press(screen.getByText(/Thought for/))
  expect(screen.getByText(/inspect the wallet kit/)).toBeTruthy()

  await fireEvent.press(screen.getByText(/Thought for/))
  expect(screen.queryByText(/inspect the wallet kit/)).toBeNull()
})

test('L51f — a live burst reads Thinking… with its duration, not the text', async () => {
  await render(<ThinkingRow text={REASONING} running />)

  expect(screen.getByText(/^Thinking/)).toBeTruthy()
  expect(screen.queryByText(/inspect the wallet kit/)).toBeNull()
})

// ── L52d / FM-TLKa: one honest line of live reasoning behind the label ──────
//
// The segment's first line IS the progress ("now it's looking at how x is
// structured") — the thing the user asked to see while the model works. It
// shows only while the segment runs (the wall stays one tap away, and the
// settled row collapses back to duration + tap). `preview` is opt-in: the
// L51 contract — running with no preview never shows text — holds unchanged.

const LIVE_LINES = 'look at how x is structured\nthen check the wallet wiring\ntail: the rest of the reasoning'

test('L52d1 — a running segment previews its first line under the ticking label', async () => {
  await render(<ThinkingRow text={LIVE_LINES} running preview />)

  expect(screen.getByText(/^Thinking/)).toBeTruthy()
  expect(screen.getByText('look at how x is structured')).toBeTruthy()
  // One line only — the second line stays behind the tap.
  expect(screen.queryByText(/then check the wallet wiring/)).toBeNull()
})

test('L52d2 — a settled segment hides the preview again: duration + tap only', async () => {
  await render(<ThinkingRow text={LIVE_LINES} running={false} preview />)

  expect(screen.getByText(/Thought for/)).toBeTruthy()
  expect(screen.queryByText('look at how x is structured')).toBeNull()
  // …and the tap still opens the full reasoning.
  await fireEvent.press(screen.getByText(/Thought for/))
  expect(screen.getByText(/then check the wallet wiring/)).toBeTruthy()
})
