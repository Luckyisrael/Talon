import React from 'react'
import { Linking, Pressable, ScrollView, StyleSheet, View } from 'react-native'
import { MenuScreen } from '@/components/nav/MenuScreen'
import { Card, MarkdownView, UIText } from '@/components/ui'
import { Colors, Spacing } from '@/constants/theme'

// Web docs base: explicit env wins, otherwise the same host as the API on
// the web port. Null when neither resolves, and the link row hides.
function webDocsUrl(): string | null {
  const explicit = (process.env.EXPO_PUBLIC_WEB_URL ?? '').trim()
  if (explicit) return `${explicit.replace(/\/$/, '')}/docs#plugins`
  const api = (process.env.EXPO_PUBLIC_API_URL ?? '').trim()
  const m = api.match(/^(https?:\/\/[^/:]+)(:\d+)?/i)
  if (!m) return null
  return `${m[1]}:3000/docs#plugins`
}

// Documentation: the guides that make the rest of the app legible. One card
// per guide, like Help and Support, rendered in the midnight voice. Bundled
// text, readable offline.
const GUIDES: { title: string; body: string }[] = [
  {
    title: 'Send your first build',
    body: 'Open Build, pick Plan to talk through the app or Build to write code, and send. Describing the app creates the project and starts the turn.\n\nThe trace shows each file the agent reads and writes. Attach a photo when words fall short, a markdown file when the spec already exists.',
  },
  {
    title: 'Answer the agent',
    body: 'Some turns pause with a question. The question card is the only thing to answer, and the turn resumes on send. Anything you type in the composer while a turn runs waits for the next turn.',
  },
  {
    title: 'Connect this phone',
    body: 'Install the dev client once from the Device tab, then open the deep link there. Metro connects the app to the workspace and reloads after each turn.\n\nNo streaming. The screen in your hand is the render.',
  },
  {
    title: 'Pair a second phone',
    body: 'The Device tab shows a pairing QR once a session exists. Scan it with the other phone camera. It needs the dev client too.',
  },
  {
    title: 'Read the diff',
    body: 'The Diff tab shows the working tree against the template baseline, one file per row. Tapping a row folds its hunks open.\n\n| Color | Meaning |\n| --- | --- |\n| Green | Added lines |\n| Red | Deleted lines |\n| Teal | Hunk headers |',
  },
  {
    title: 'When Metro complains',
    body: 'The Device tab lists recent Metro events under Metro activity. A red line names the failing file. Fix it in the next prompt or let the agent read the event on its next turn.',
  },
  {
    title: 'Plans live twice',
    body: 'A settled plan writes PLAN.md into the project and ticks its checklist in the Build stream. Approving the plan card sends the build.',
  },
]

export default function DocsScreen() {
  const pluginsUrl = webDocsUrl()
  return (
    <MenuScreen title="Documentation">
      <ScrollView showsVerticalScrollIndicator={false}>
        <View style={styles.stack}>
          {GUIDES.map((g) => (
            <Card key={g.title}>
              <View style={styles.card}>
                <UIText variant="title" tone="hero">
                  {g.title}
                </UIText>
                <MarkdownView>{g.body}</MarkdownView>
              </View>
            </Card>
          ))}
          {pluginsUrl ? (
            <Pressable
              onPress={() => void Linking.openURL(pluginsUrl)}
              accessibilityRole="link"
              style={({ pressed }) => [styles.linkRow, pressed && styles.pressed]}
            >
              <View style={styles.linkText}>
                <UIText variant="body" tone="hero">
                  Plugin guide on the web
                </UIText>
                <UIText variant="caption" tone="muted">
                  Using and building Talon plugins, with the web3-components reference.
                </UIText>
              </View>
              <UIText variant="body" tone="faint">
                ›
              </UIText>
            </Pressable>
          ) : null}
        </View>
      </ScrollView>
    </MenuScreen>
  )
}

const styles = StyleSheet.create({
  stack: { gap: Spacing.s12, paddingBottom: Spacing.s32 },
  card: { gap: Spacing.s8 },
  linkRow: {
    flexDirection: 'row',
    alignItems: 'center',
    justifyContent: 'space-between',
    gap: Spacing.s12,
    minHeight: 60,
    paddingHorizontal: 14,
    borderRadius: 10,
    backgroundColor: Colors.carbon,
  },
  linkText: { gap: 2, flex: 1 },
  pressed: { opacity: 0.7 },
})
