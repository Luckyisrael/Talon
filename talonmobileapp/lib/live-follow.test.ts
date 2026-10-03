// L52d / FM-TLKa — fails-first: the feed follows itself, and lets go when
// the reader scrolls away.
//
// The gap: the build tail had NO auto-follow — new rows landed below the
// fold while the screen held still — and no way back once you scrolled up.
// The rule is pure position math, so it lives in one testable function:
//   · at (or within a thumb's reach of) the bottom → keep following
//   · a manual scroll up past that reach → release
//   · the ↓ live pill shows only while the turn is still producing and
//     following is off; pressing it re-attaches (follow = true).
//
// Pre-fix this module does not exist — the module-resolution failure IS the
// red (the house's accepted form): a feed that cannot follow itself has no
// follow rule to test.

import { followAfterScroll, livePillVisible } from '@/lib/live-follow'

test('L52g1 — at the bottom → following stays on; scrolled up → released', () => {
  // content 1200, viewport 200 → true bottom at offsetY 1000
  expect(followAfterScroll(1000, 1200, 200)).toBe(true)
  expect(followAfterScroll(970, 1200, 200)).toBe(true) // within reach of the bottom
  expect(followAfterScroll(800, 1200, 200)).toBe(false) // reading history → release
  expect(followAfterScroll(0, 1200, 200)).toBe(false) // top of the transcript
})

test('L52g2 — content that fits never "scrolls away" (no negative release)', () => {
  expect(followAfterScroll(0, 150, 200)).toBe(true)
  expect(followAfterScroll(0, 0, 0)).toBe(true) // nothing measured yet → follow
})

test('L52h — the pill shows only while live AND off-follow', () => {
  expect(livePillVisible(false, true)).toBe(true) // scrolled up mid-turn → offer the jump
  expect(livePillVisible(true, true)).toBe(false) // following → nothing to rejoin
  expect(livePillVisible(false, false)).toBe(false) // turn settled → no pill
})
