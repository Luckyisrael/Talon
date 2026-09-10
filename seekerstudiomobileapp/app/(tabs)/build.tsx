import React from 'react';
import { ScrollView, StyleSheet, View } from 'react-native';
import { Field, LimeButton, Screen, UIText } from '@/components/ui';
import { TraceRow } from '@/components/build/TraceRow';
import { PixelGrid } from '@/components/build/PixelGrid';
import { QuestionCard } from '@/components/build/QuestionCard';
import { Colors, Spacing } from '@/constants/theme';

export default function BuildScreen() {
  const [draft, setDraft] = React.useState('');
  const [echoes, setEchoes] = React.useState<string[]>([]);
  const [answers, setAnswers] = React.useState<string[]>([]);

  return (
    <Screen topInset={false}>
      {/* Status header: eyebrow + project + partial-verified line */}
      <View style={styles.head}>
        <UIText variant="micro" tone="faint">
          BUILD 42 · ROUND 7
        </UIText>
        <UIText variant="heading" tone="hero">
          Demo App
        </UIText>
        <UIText variant="bodySm" tone="faint" style={{ color: Colors.amber }}>
          verified (static only — boot, vision could not run)
        </UIText>
      </View>

      <ScrollView style={styles.stream} showsVerticalScrollIndicator={false}>
        <View style={styles.stack}>
          <TraceRow
            tool="write_file"
            label="Wrote"
            target="app/(tabs)/tip.tsx"
            summary="312 lines · wallet connect + presets + history"
            status="done"
          />
          <TraceRow
            tool="run_typecheck"
            label="Checked"
            target="run_typecheck"
            summary="tsc --noEmit · 0 errors"
            status="done"
          />
          <TraceRow
            tool="read_skill"
            label="Consulted skill"
            target="wallet-mwa"
            status="done"
          />
          <TraceRow
            tool="explore"
            label="Probing"
            target="@solana/kit"
            status="running"
          />
          <TraceRow
            tool="run_typecheck"
            label="Checked"
            target="run_typecheck"
            summary="tsc --noEmit · 0 errors"
            status="done"
          />
          <TraceRow
            tool="run_lint"
            label="Linted"
            target="run_lint"
            summary="eslint · 0 problems"
            status="done"
          />
          <TraceRow
            tool="run_bundle"
            label="Bundled"
            target="run_bundle"
            summary="expo export · 182 KB in 4.1s"
            status="done"
          />
          <TraceRow
            tool="build_apk"
            label="Assembling"
            target="run_gradle"
            summary=":app:assembleDebug · 45/828 tasks"
            status="running"
          />
          <TraceRow
            tool="run_boot"
            label="Booted"
            target="run_boot"
            summary="pid 2780 · first log after 1.1s"
            status="done"
          />
          <TraceRow
            tool="run_vision"
            label="Reviewed"
            target="run_vision"
            status="running"
          />
          <TraceRow
            tool="run_interact"
            label="Reached"
            target="run_interact"
            summary="/watch · tap landed"
            status="done"
          />
          <QuestionCard
            question="Gradle needs ~13 minutes cold. Build the APK and install it on the device now?"
            choices={['Build + install', 'Code only for now']}
            onAnswer={(a) => setAnswers((prev) => [...prev, a])}
          />
          {answers.map((a, i) => (
            <UIText key={i} variant="caption" tone="faint">
              → answered: {a} (dummy)
            </UIText>
          ))}
          <PixelGrid label="Working" variant="Drive" />
          {echoes.map((e, i) => (
            <UIText key={i} variant="body" tone="hero" style={styles.echo}>
              {e}
            </UIText>
          ))}
        </View>
      </ScrollView>

      {/* Composer: local echo only (no API calls in Phase 2) */}
      <View style={styles.composer}>
        <Field
          value={draft}
          onChangeText={setDraft}
          placeholder="Tell the agent what to do…"
          multiline
          style={styles.input}
        />
        <LimeButton
          title="Send"
          style={styles.send}
          disabled={!draft.trim()}
          onPress={() => {
            setEchoes((prev) => [...prev, draft.trim()]);
            setDraft('');
          }}
        />
      </View>
    </Screen>
  );
}

const styles = StyleSheet.create({
  head: { gap: 8, marginBottom: Spacing.s4 },
  stream: { flex: 1, marginTop: Spacing.s12 },
  stack: { gap: Spacing.s12, paddingBottom: Spacing.s16 },
  echo: {
    backgroundColor: 'rgba(228,242,34,0.08)',
    borderRadius: 6,
    padding: Spacing.s12,
  },
  composer: { flexDirection: 'row', gap: Spacing.s8, paddingTop: Spacing.s8 },
  input: { flex: 1 },
  send: { minWidth: 88 },
});
