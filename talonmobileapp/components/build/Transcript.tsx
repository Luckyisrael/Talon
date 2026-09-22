import React from 'react'
import { StyleSheet, TouchableOpacity, View } from 'react-native'
import { MarkdownView } from '@/components/ui'
import { TraceRow } from '@/components/build/TraceRow'
import { QuestionCard } from '@/components/build/QuestionCard'
import { TurnAttachments } from '@/components/build/Attachments'
import { UIText } from '@/components/ui'
import { Colors, Spacing } from '@/constants/theme'
import { stripInlineDocs, type SentAttachment } from '@/lib/attachments'
import type { ConversationBlock } from '@/lib/api'

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
}: {
  turns: RestoredTurn[]
  projectId: number
  waitingPromptId?: number | null
  onAnswer: (promptId: number, text: string) => void
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

        const narration = (t.blocks ?? [])
          .filter((b) => b.kind === 'text' && b.text?.trim())
          .map((b) => b.text!)
          .join('\n\n')
        const tools = (t.blocks ?? []).filter((b) => b.kind === 'tool_use')
        const question = (t.blocks ?? []).find((b) => b.kind === 'tool_use' && b.name === 'ask_user')
        const ending = (t.blocks ?? []).find((b) => b.kind === 'turn_end')
        if (!narration && tools.length === 0 && !ending) return null
        return (
          <View key={i} style={styles.assistantWrap}>
            {narration ? <MarkdownView>{narration}</MarkdownView> : null}
            {tools
              .filter((b) => b.name !== 'ask_user')
              .map((b, j) => {
                const input = (b.input ?? {}) as Record<string, unknown>
                const str = (v: unknown) => (typeof v === 'string' ? v : undefined)
                return (
                  <TraceRow
                    key={j}
                    tool={b.name}
                    label={toolLabel(b.name ?? '', input)}
                    target={str(input.path) ?? str(input.command) ?? str(input.pattern) ?? ''}
                    status={b.id && outcomes.get(b.id) ? 'fail' : 'done'}
                  />
                )
              })}
            {ending ? <TurnEndRow block={ending} /> : null}
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
  // A turn that completed cleanly needs no announcement: the assistant text
  // already ends it. Interrupted is the restart sweep's marker; failed and
  // cancelled are the two a user needs to see.
  if (status === 'completed') return null

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
