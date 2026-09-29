import React from 'react'
import { StyleSheet, View } from 'react-native'
import { useRouter } from 'expo-router'
import { MenuScreen } from '@/components/nav/MenuScreen'
import { Card, GhostButton, UIText } from '@/components/ui'
import { Spacing } from '@/constants/theme'

const FAQ: { q: string; a: string }[] = [
  {
    q: 'The agent paused with a question. What now?',
    a: 'Answer in the question card on the Build tab. The turn resumes from the pause with your reply.',
  },
  {
    q: 'My phone is not reloading after a turn.',
    a: 'Open the Device tab and check Metro activity. The dev client reloads when the bundler finishes; a red Metro line names the file and error.',
  },
  {
    q: 'A turn was interrupted. Is the work lost?',
    a: 'No. Files land on disk as they are written. Send a fresh prompt describing what remains, or resume from History.',
  },
  {
    q: 'What can I attach to a prompt?',
    a: 'Photos and markdown files. Photos let the agent see a design or a bug. Markdown inlines into the prompt. Large photos shrink before upload.',
  },
  {
    q: 'How do I test on this phone?',
    a: 'Open the Device tab and tap Open on this phone. It hands the running project to the dev client over your network — there is no QR to scan when the screen in your hand is the only screen.',
  },
  {
    q: 'How do I test on a second phone?',
    a: 'Open the Device tab and scan the pairing QR with that phone camera. It needs the dev client installed once.',
  },
]

// Help and Support: answers that hold without a network round trip, plus a
// way into the full guides.
export default function HelpScreen() {
  const router = useRouter()
  return (
    <MenuScreen title="Help and Support">
      {FAQ.map((item) => (
        <Card key={item.q}>
          <View style={styles.stack}>
            <UIText variant="body" tone="hero">
              {item.q}
            </UIText>
            <UIText variant="bodySm" tone="muted">
              {item.a}
            </UIText>
          </View>
        </Card>
      ))}
      <GhostButton title="Open documentation" onPress={() => router.push('/menu/docs')} />
    </MenuScreen>
  )
}

const styles = StyleSheet.create({
  stack: { gap: Spacing.s8 },
})
