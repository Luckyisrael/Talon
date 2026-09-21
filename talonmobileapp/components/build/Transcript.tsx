import React from 'react'
import { StyleSheet, View } from 'react-native'
import { MarkdownView } from '@/components/ui'
import { TraceRow } from '@/components/build/TraceRow'
import { QuestionCard } from '@/components/build/QuestionCard'
import { TurnAttachments } from '@/components/build/Attachments'
import { UIText } from '@/components/ui'
import { Colors, Spacing } from '@/constants/theme'
import { stripInlineDocs, type SentAttachment } from '@/lib/attachments'
import type { ConversationBlock } from '@/lib/api'

export interface RestoredTurn {
  role: string
  blocks: ConversationBlock[]
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
          const shown = stripInlineDocs(text)
          if (!shown && atts.length === 0) return null
          return (
            <View key={i} style={styles.userWrap}>
              {shown ? (
                <View style={styles.bubble}>
                  <UIText variant="body" tone="hero">
                    {shown}
                  </UIText>
                </View>
              ) : null}
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
        if (!narration && tools.length === 0) return null
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
})
