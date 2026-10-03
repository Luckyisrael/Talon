// L52d / FM-TLKa — the feed follows the newest row, and lets go the
// moment the reader scrolls away.
//
// The gap: the build tail had no auto-follow at all — new rows landed
// below the fold while the screen held still — and once you scrolled up
// there was no way back except dragging. Pure position math lives here so
// it can be pinned without a scroll view: distance from the bottom within
// a thumb's reach → following; past it → released (the ↓ live pill then
// offers the jump back).

/** How close to the bottom still counts as "at the bottom". 64pt is about
 *  one row — far enough that reading upward releases, close enough that
 *  riding the live edge never flickers. */
export const FOLLOW_THRESHOLD = 64

/**
 * Recompute stickiness from a scroll position.
 * Returns true when the viewport is at (or within FOLLOW_THRESHOLD of)
 * the content's bottom — including content shorter than the viewport,
 * where nothing can scroll away.
 */
export function followAfterScroll(offsetY: number, contentHeight: number, viewportHeight: number): boolean {
  if (contentHeight <= 0 || viewportHeight <= 0) return true // nothing measured yet → follow
  return contentHeight - viewportHeight - offsetY <= FOLLOW_THRESHOLD
}

/** The ↓ live pill: only while the turn is still producing AND the reader
 *  has released the follow. Settled turns have nothing to rejoin. */
export function livePillVisible(following: boolean, live: boolean): boolean {
  return live && !following
}
