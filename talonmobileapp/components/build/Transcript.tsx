import React from 'react'
import { StyleSheet, TouchableOpacity, View } from 'react-native'
import { MarkdownView, UIText } from '@/components/ui'
import { TraceRow } from '@/components/build/TraceRow'
import { ThinkingRow } from '@/components/build/ThinkingRow'
import { QuestionCard } from '@/components/build/QuestionCard'
import { TurnAttachments } from '@/components/build/Attachments'
import { Colors, Spacing } from '@/constants/theme'
import { VerifyEvidenceRow, evidenceFromMarker } from '@/components/build/VerifyEvidence'
import { PlanCard } from '@/components/build/PlanCard'
import { stripInlineDocs, type SentAttachment } from '@/lib/attachments'
import { normalizePlan, type ConversationBlock, type PlanData } from '@/lib/api'

// A block longer than this renders clamped behind a Show-more toggle. Six is
// enough to read a normal sentence or two; anything past it is a document, and
// a document does not get to own the scroll.
const MAX_LINES = 6

export interface RestoredTurn {
  role: string
  blocks: ConversationBlock[]
  /** The prompt this turn belongs to. The live tail keys its retirement on
   *  this: once history contains the prompt id in flight, the optimistic rows
   *  hand off to the durable ones and nothing is shown twice. */
  prompt_id?: number
}

// RestoredTranscript renders stored turns with the same vocabulary as the
// live stream: user bubbles with chips, assistant narration as markdown,
// tool calls as trace rows. Plumbing turns (bare tool results) never render.
// A waiting ask_user on the latest turn restores its question card so the
// pause survives a reload exactly as it streams live.
export function RestoredTranscript({
  turns,
  projectId,
  waitingPromptId,
  onAnswer,
  onApprovePlan,
  promptEvidence,
}: {
  turns: RestoredTurn[]
  projectId: number
  waitingPromptId?: number | null
  onAnswer: (promptId: number, text: string) => void
  /** Hand a restored plan to the approve flow (PLAN.md + build turn), the
   *  same action the live card offers. Optional: screens without it render
   *  the plan read-only. */
  onApprovePlan?: (plan: PlanData) => void
  /** The newest marker-less turn's prompt-row union, keyed by prompt id
   *  (files_touched only — the union carries no verdict, so a seed can never
   *  claim a check ran or passed). Supplied by the screen via promptsApi.get. */
  promptEvidence?: Record<number, { files_touched?: string[] }>
}) {
  // Tool outcomes arrive in later turns keyed by call id: collect them
  // first so every tool row resolves to done or fail, live or restored.
  const outcomes = React.useMemo(() => {
    const map = new Map<string, boolean>()
    for (const t of turns) {
      for (const b of t.blocks ?? []) {
        if (b.kind === 'tool_result' && b.tool_use_id) {
          map.set(b.tool_use_id, !!b.is_error)
        }
      }
    }
    return map
  }, [turns])

  // The newest plan block wins: older plans are superseded, and only the
  // current one earns the approve action — a plan kept approving after three
  // more plans is how a build gets built to the wrong spec. Keyed by turn
  // index so the card lands inside the turn that produced it.
  const restoredPlan = React.useMemo(() => {
    for (let i = turns.length - 1; i >= 0; i--) {
      const b = (turns[i].blocks ?? []).find((x) => x.kind === 'plan')
      if (b && b.plan) return { turn: i, plan: normalizePlan(b.plan) }
    }
    return null
  }, [turns])

  // L52k / FM-TLKa — only the NEWEST marker per prompt renders. Resume cycles
  // leave a stale "Turn cancelled" from the interrupted attempt beside the
  // completed marker that superseded it; without this rule one turn ends
  // twice, the older ending outliving the newer one. Stored order is
  // chronological, so the last marker seen wins.
  const latestMarker = React.useMemo(() => {
    const map = new Map<number | string, { row: number; block: number }>()
    turns.forEach((t, i) => {
      const key: number | string = t.prompt_id ?? `row-${i}`
      ;(t.blocks ?? []).forEach((b, j) => {
        if (b.kind === 'turn_end') map.set(key, { row: i, block: j })
      })
    })
    return map
  }, [turns])

  return (
    <View style={styles.stack}>
      {turns.map((t, i) => {
        if (t.role === 'user') {
          const text = (t.blocks ?? [])
            .filter((b) => b.kind === 'text' && b.text)
            .map((b) => b.text!)
            .join('')
            .trim()
          const atts: SentAttachment[] = (t.blocks ?? [])
            .filter((b) => (b.kind === 'image' || b.kind === 'doc') && typeof b.attachment_id === 'number')
            .map((b) => ({ id: b.attachment_id!, name: b.name ?? 'file', mime: b.mime ?? 'application/octet-stream' }))
          // Machine guidance rides the same user-role turn but is not chat.
          // Rendered as a chip, it takes one line instead of 400.
          const ctxs = (t.blocks ?? []).filter((b) => b.kind === 'context' && b.title)
          const shown = stripInlineDocs(text)
          if (!shown && atts.length === 0 && ctxs.length === 0) return null
          return (
            <View key={i} style={styles.userWrap}>
              {shown ? (
                <View style={styles.bubble}>
                  <CollapsibleText text={shown} variant="body" tone="hero" />
                </View>
              ) : null}
              {ctxs.map((c, j) => (
                <ContextRow key={`ctx-${j}`} block={c} />
              ))}
              {atts.length > 0 ? <TurnAttachments projectId={projectId} items={atts} /> : null}
            </View>
          )
        }

        // L52e / FM-TLKa — the restore walks stored blocks IN STORED ORDER:
        // thinking as collapsed rows, prose as markdown, tools and decisions
        // as trace rows, the plan card, the newest marker — each at the
        // position it happened. The old fixed buckets hoisted every word
        // above every tool (narration first, plan pinned between) and dropped
        // stored thinking entirely (271 rows in the live DB carry
        // type:"thinking" and none of them rendered after a reload).
        const blocks = t.blocks ?? []
        const promptKey: number | string = t.prompt_id ?? `row-${i}`
        const question = blocks.find((b) => b.kind === 'tool_use' && b.name === 'ask_user')
        const hasMarker = blocks.some((b) => b.kind === 'turn_end')
        const isPlanTurn = restoredPlan?.turn === i
        // M4: a marker-less newest turn has no footer to rebuild — seed it from
        // the prompt row's files_touched union. Files only: the union carries
        // no verdict, so a seed can never claim a check ran or passed.
        const seedFiles = !hasMarker && t.prompt_id != null ? promptEvidence?.[t.prompt_id]?.files_touched : undefined
        const showQuestion = !!(question && waitingPromptId)

        const rows: React.ReactNode[] = []
        // The plan card is the turn: it renders at the TOP of its row — the
        // same position the live tail gives it (L52a) — so the card does not
        // jump from above (streaming) to below (restored) at settle.
        if (isPlanTurn && restoredPlan) {
          rows.push(<PlanCard key="plan" plan={restoredPlan.plan} onApprove={onApprovePlan} />)
        }
        blocks.forEach((b, j) => {
          const key = `b${j}`
          if (b.kind === 'thinking') {
            const text = b.text ?? ''
            if (text.trim()) rows.push(<ThinkingRow key={key} text={text} running={false} />)
            return
          }
          if (b.kind === 'text') {
            if (b.text?.trim()) rows.push(<MarkdownView key={key}>{b.text}</MarkdownView>)
            return
          }
          if (b.kind === 'context') {
            if (b.title) rows.push(<ContextRow key={key} block={b} />)
            return
          }
          if (b.kind === 'plan') return // hoisted above the row (see plan card)
          if (b.kind === 'turn_end') {
            // Newest-wins: a superseded marker renders nothing — the marker
            // that replaced it owns the ending.
            const latest = latestMarker.get(promptKey)
            if (latest && latest.row === i && latest.block === j) rows.push(<TurnEndRow key={key} block={b} />)
            return
          }
          if (b.kind !== 'tool_use' || b.name === 'ask_user') return
          const input = (b.input ?? {}) as Record<string, unknown>
          const str = (v: unknown) => (typeof v === 'string' ? v : undefined)
          if (b.name === 'write_adr' || b.name === 'write_memory') {
            // The decision's text lives in its input — reconstructed the way
            // the web rebuilds it: one row, titled, full text one tap away.
            const full = str(input.decision) ?? str(input.content) ?? ''
            if (!full.trim()) return
            rows.push(
              <TraceRow
                key={key}
                tool={b.name}
                label={b.name === 'write_adr' ? 'Recorded ADR' : 'Recorded memory'}
                target={full.split('\n')[0] || undefined}
                summary={full}
                status={b.id && outcomes.get(b.id) ? 'fail' : 'done'}
              />,
            )
            return
          }
          rows.push(
            <TraceRow
              key={key}
              tool={b.name}
              label={toolLabel(b.name ?? '', input)}
              target={str(input.path) ?? str(input.command) ?? str(input.pattern) ?? ''}
              status={b.id && outcomes.get(b.id) ? 'fail' : 'done'}
            />,
          )
        })
        const seedRow = seedFiles?.length ? <VerifyEvidenceRow evidence={{ filesTouched: seedFiles }} /> : null
        if (rows.length === 0 && !seedRow && !showQuestion) return null
        return (
          <View key={i} style={styles.assistantWrap}>
            {rows}
            {seedRow}
            {question && waitingPromptId ? (
              <QuestionCard
                question={str(inputOf(question, 'question')) ?? 'The agent asked a question.'}
                choices={arrayOfStrings(inputOf(question, 'choices'))}
                onAnswer={(text) => onAnswer(waitingPromptId, text)}
              />
            ) : null}
          </View>
        )
      })}
    </View>
  )
}

function inputOf(b: ConversationBlock, key: string): unknown {
  const input = (b.input ?? {}) as Record<string, unknown>
  return input[key]
}

// TurnEndRow renders the terminal marker — the line that says how a turn ended.
// This is what was missing when a turn that ran four tools died: the rows were
// there with nothing to say they stopped, so it read as still in flight. DSH
// shows the same thing as its turn-tail; a clean turn renders nothing here so
// the transcript does not gain a line of noise per prompt.
// CollapsibleText caps a block at MAX_LINES and grows on demand. It is the
// backstop: machine text is already a chip, but anything genuinely long that
// still renders — a verbose assistant reply, an attached doc that survived
// stripping — should never again dominate the scroll.
function CollapsibleText({
  text,
  variant,
  tone,
}: {
  text: string
  variant: Parameters<typeof UIText>[0]['variant']
  tone?: Parameters<typeof UIText>[0]['tone']
}) {
  const [expanded, setExpanded] = React.useState(false)
  const lines = text.split('\n')
  const clamped = !expanded && lines.length > MAX_LINES
  const shown = clamped ? lines.slice(0, MAX_LINES).join('\n') : text
  return (
    <View>
      <UIText variant={variant} tone={tone} numberOfLines={clamped ? MAX_LINES : undefined}>
        {shown}
      </UIText>
      {lines.length > MAX_LINES ? (
        <TouchableOpacity onPress={() => setExpanded((v) => !v)} style={styles.moreBtn}>
          <UIText variant="caption" tone="faint">
            {expanded ? 'Show less' : `Show ${lines.length - MAX_LINES} more lines`}
          </UIText>
        </TouchableOpacity>
      ) : null}
    </View>
  )
}

// ContextRow renders machine guidance as a single-line chip. A skill body or a
// lint report is context the model needed, not a message the user sent; shown
// as a bubble it put a 400-line document in the middle of the conversation.
// The body is a tap away, never on by default.
function ContextRow({ block }: { block: ConversationBlock }) {
  const [open, setOpen] = React.useState(false)
  const title = block.title ?? 'Applied guidance'
  const body = block.body ?? ''
  return (
    <View style={styles.ctx}>
      <TouchableOpacity onPress={() => setOpen((v) => !v)} style={styles.ctxHead}>
        <UIText variant="caption" tone="faint" numberOfLines={1} style={styles.ctxTitle}>
          {block.kind === 'verify' ? '⚠ ' : '◆ '}
          {title}
        </UIText>
        <UIText variant="caption" tone="faint">
          {open ? '−' : '+'}
        </UIText>
      </TouchableOpacity>
      {open && body ? (
        <View style={styles.ctxBody}>
          <CollapsibleText text={body} variant="caption" tone="muted" />
        </View>
      ) : null}
    </View>
  )
}

function TurnEndRow({ block }: { block: ConversationBlock }) {
  const status = block.status ?? ''
  // The evidence row renders in both directions: the verify payload when the
  // marker carries one (verified true/false — the claim), or rounds alone on a
  // completed legacy marker (the marker's only knowledge, no claim). Absent
  // both → nothing below.
  const evidence = evidenceFromMarker(block)
  const evidenceRow = evidence ? <VerifyEvidenceRow evidence={evidence} /> : null

  // A turn that completed cleanly needs no announcement: the assistant text
  // already ends it. Interrupted is the restart sweep's marker; failed and
  // cancelled are the two a user needs to see — their status row keeps the
  // rounds sentence, so the evidence row below never repeats them.
  if (status === 'completed') return evidenceRow

  const tone = status === 'failed' || status === 'interrupted' ? Colors.coralRed : Colors.fog
  const label =
    status === 'failed' ? 'Turn failed'
    : status === 'cancelled' ? 'Turn cancelled'
    : status === 'interrupted' ? 'Turn interrupted by a restart'
    : status === 'stalled' ? 'Turn stalled'
    : 'Turn ended'
  const detail = block.error?.trim()
  return (
    <View style={styles.turnEnd}>
      <UIText variant="caption" style={{ color: tone }}>
        {label}{block.rounds ? ` · after ${block.rounds} round${block.rounds === 1 ? '' : 's'}` : ''}
      </UIText>
      {detail ? (
        <UIText variant="caption" tone="faint" numberOfLines={4} style={styles.turnEndError}>
          {detail}
        </UIText>
      ) : null}
      {evidenceRow}
    </View>
  )
}

function str(v: unknown): string | undefined {
  return typeof v === 'string' ? v : undefined
}

function arrayOfStrings(v: unknown): string[] {
  return Array.isArray(v) ? v.filter((x): x is string => typeof x === 'string') : []
}

// toolLabel mirrors the live map in the build tab: verb plus target, so a
// restored row reads exactly like the row that streamed it.
function toolLabel(name: string, input: Record<string, unknown>): string {
  switch (name) {
    case 'read_file':
      return 'Read'
    case 'write_file':
      return 'Created'
    case 'edit_file':
    case 'multi_edit':
      return 'Edited'
    case 'delete_file':
      return 'Deleted'
    case 'list_files':
      return 'Listed'
    case 'run_typecheck':
      return 'Checked types'
    case 'run_lint':
      return 'Linted'
    case 'run_command':
    case 'run_shell':
      return 'Ran'
    case 'read_skill':
      return typeof input.name === 'string' && input.name ? input.name : 'Consulted a skill'
    default:
      return name.replace(/_/g, ' ') || 'Ran'
  }
}

const styles = StyleSheet.create({
  stack: { gap: Spacing.s12 },
  userWrap: { alignItems: 'flex-end', gap: Spacing.s8 },
  bubble: {
    maxWidth: '88%',
    backgroundColor: Colors.obsidian,
    borderRadius: 12,
    padding: Spacing.s12,
    gap: 4,
  },
  assistantWrap: { gap: Spacing.s8 },
  moreBtn: { paddingTop: 2, alignSelf: 'flex-start' },
  ctx: {
    maxWidth: '88%',
    borderRadius: 10,
    paddingHorizontal: Spacing.s12,
    paddingVertical: Spacing.s8,
    gap: Spacing.s4,
  },
  ctxHead: { flexDirection: 'row', alignItems: 'center', gap: Spacing.s8 },
  ctxTitle: { flex: 1 },
  ctxBody: { paddingTop: Spacing.s4 },
  turnEnd: {
    borderLeftWidth: 2,
    borderLeftColor: Colors.coralRed,
    paddingLeft: Spacing.s8,
    paddingVertical: 2,
  },
  turnEndError: { opacity: 0.8 },
})
