import type { ConversationBlock } from '@/lib/api'

// L52 / FM-TLKa — the live turn's one chronological timeline.
//
// The reducer used to keep thinking/toolCalls/text in fixed buckets and
// build.tsx rendered them in fixed slots, so chronology died on arrival:
// reasoning pinned above the trace no matter when it arrived, the plan
// card pinned at the bottom, paired running/completed frames appended
// twice. A TimelineItem is one row at its ARRIVAL position — the ordered
// array is the render source; the legacy fields keep updating beside it
// for the contracts that pin them (L51's signal, the P-series).
export type TimelineItem =
  | { key: string; kind: 'thinking'; text: string; streaming: boolean }
  | { key: string; kind: 'narration'; text: string }
  | { key: string; kind: 'tool'; id?: string; name: string; status: string; target?: string; summary?: string }
  | { key: string; kind: 'decision'; name: string; target?: string; summary?: string }
  | { key: string; kind: 'skill'; title: string }
  | { key: string; kind: 'output'; text: string }

/** The bounded tail one output row keeps — same bound as the legacy field. */
export const OUTPUT_TAIL = 2000

type DistributiveOmit<T, K extends PropertyKey> = T extends unknown ? Omit<T, K> : never;
type NewItem = DistributiveOmit<TimelineItem, 'key'>;

/**
 * L53c / FM-RSUMa — rebuild the settled timeline from a prompt's stored
 * blocks so resume/answer continue where they stopped instead of a blank
 * tail. Stored order IS chronology; every row seeds settled (streaming
 * false). Stale plumbing never seeds: tool_result (outcomes ride the map),
 * ask_user (the tail owns the question card), turn_end (the newest-wins
 * marker rule owns the ending), blank thinking/empty text (no empty rows).
 *
 * `outcomes` maps tool_use id → is_error (the same collection the restored
 * transcript builds), so a failed tool seeds as failed.
 */
export function seedFromBlocks(
  blocks: ConversationBlock[],
  outcomes?: Map<string, boolean>,
): TimelineItem[] {
  const items: TimelineItem[] = []
  let n = 0
  const push = (item: NewItem) => {
    // A distinct prefix from the reducer's `tl-` keys: a seeded tail keeps
    // receiving live rows without ever colliding on its React keys.
    items.push({ ...(item as TimelineItem), key: `s-${n++}` } as TimelineItem);
  };
  const str = (v: unknown): string | undefined => (typeof v === 'string' ? v : undefined)

  for (const b of blocks) {
    if (b.kind === 'thinking') {
      const text = (b.text ?? '').trim()
      if (text) push({ kind: 'thinking', text: b.text ?? '', streaming: false })
    } else if (b.kind === 'text') {
      if ((b.text ?? '').trim()) push({ kind: 'narration', text: b.text ?? '' })
    } else if (b.kind === 'tool_use') {
      if (b.name === 'ask_user') continue
      const input = (b.input ?? {}) as Record<string, unknown>
      if (b.name === 'write_adr' || b.name === 'write_memory') {
        // The web's decision reconstruction: the decision lives in the
        // input, not in a bare tool row.
        const full = str(input.decision) ?? str(input.content) ?? ''
        if (!full.trim()) continue
        push({
          kind: 'decision',
          name: b.name === 'write_adr' ? 'Recorded ADR' : 'Recorded memory',
          target: full.split('\n')[0] || undefined,
          summary: full,
        })
      } else {
        push({
          kind: 'tool',
          id: b.id,
          name: b.name ?? '',
          status: b.id && outcomes?.get(b.id) ? 'error' : 'completed',
          target: str(input.path) ?? str(input.command) ?? str(input.pattern),
        })
      }
    }
  }
  return items
}

/**
 * The tool outcomes keyed by call id — tool_result blocks arrive in later
 * turns keyed by the call they answer. Shared shape with the restored
 * transcript's collection so a seeded row and a restored row settle the
 * same way.
 */
export function collectOutcomes(turns: { blocks?: ConversationBlock[] | null }[]): Map<string, boolean> {
  const map = new Map<string, boolean>()
  for (const t of turns) {
    for (const b of t.blocks ?? []) {
      if (b.kind === 'tool_result' && b.tool_use_id) map.set(b.tool_use_id, !!b.is_error)
    }
  }
  return map
}
