// L53 / FM-RSUMa — when the live tail shows, and which stored rows stand
// aside while it does.
//
// The bug these two rules replace: `liveVisible` retired the tail the
// moment `historyHasTurn` matched — and a resumed prompt id is ALREADY in
// history (the interrupted turn is persisted; that is what makes it
// resumable), so the first phase frame hid the stream and every frame
// after it reduced into an unmounted screen. Server proof from the user's
// own session: `POST /v1/prompts/75/resume bytes=69375` streamed while
// the phone showed nothing.

export type TailPhase = 'idle' | 'streaming' | 'waiting' | 'done' | 'error'

/**
 * The tail never retires while the turn is streaming or waiting — that is
 * the resume fix, and it also survives a mid-stream history refetch (tab
 * focus, stream-dead poll) that used to kill a live build the same way.
 * On done/error the ORIGINAL no-duplicate gate holds: retire exactly when
 * the durable log has absorbed the turn, keeping the brief settled window
 * until the refetch lands.
 */
export function isTailVisible(phase: string, historyHasTurn: boolean): boolean {
  if (phase === 'streaming' || phase === 'waiting') return true
  if (phase === 'done' || phase === 'error') return !historyHasTurn
  return false
}

/**
 * While the tail shows prompt P, the restored transcript drops P's rows so
 * nothing renders twice (the tail seeds assistant rows from these very
 * blocks). The user's bubble steps aside only when the tail echoes it —
 * a send echoes; a resume/answer does not, so the stored bubble is all
 * there is and it stays. Released with the tail at settle.
 */
export function excludeLivePrompt<T extends { role: string; prompt_id?: number }>(
  turns: T[],
  phase: string,
  promptId: number | undefined,
  hasEcho: boolean,
): T[] {
  if ((phase !== 'streaming' && phase !== 'waiting') || promptId == null) return turns
  return turns.filter((t) => {
    if (t.prompt_id !== promptId) return true
    if (t.role !== 'user') return false
    return !hasEcho
  })
}
