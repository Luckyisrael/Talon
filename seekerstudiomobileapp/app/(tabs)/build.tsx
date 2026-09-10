import React from 'react';
import { ScrollView, StyleSheet, View } from 'react-native';
import { Field, LimeButton, Screen, UIText } from '@/components/ui';
import { TraceRow } from '@/components/build/TraceRow';
import { ThinkingRow } from '@/components/build/ThinkingRow';
import { QuestionCard } from '@/components/build/QuestionCard';
import { GateRow } from '@/components/build/GateRow';
import { Colors, Spacing } from '@/constants/theme';

// Dummy transcript exercising every Build-tab state. No API calls — Phase 2
// is UI only; the live stream wires these same components in the next step.
function useElapsed(running: boolean): string {
  const [secs, setSecs] = React.useState(0);
  React.useEffect(() => {
    if (!running) return;
    const t = setInterval(() => setSecs((s) => s + 1), 1000);
    return () => clearInterval(t);
  }, [running]);
  return `0:${String(secs).padStart(2, '0')}`;
}

export default function BuildScreen() {
  const elapsed = useElapsed(true);
  const [draft, setDraft] = React.useState('');
  const [echoes, setEchoes] = React.useState<string[]>([]);
  const [answers, setAnswers] = React.useState<string[]>([]);

  return (
    <Screen>
      {/* Status header: eyebrow + project + partial-verified line */}
      <UIText variant="micro" tone="faint">
        BUILD 42 · ROUND 7
      </UIText>
      <UIText variant="heading" tone="hero">
        TipKit
      </UIText>
      <UIText variant="caption" tone="faint" style={{ color: Colors.amber }}>
        verified (static only — boot, vision could not run)
      </UIText>

      <ScrollView style={styles.stream} showsVerticalScrollIndicator={false}>
        <View style={styles.stack}>
          <TraceRow verb="Wrote" target="app/(tabs)/tip.tsx" detail="312 lines" status="done" />
          <TraceRow verb="Checked" target="run_typecheck" detail="no type errors" status="done" />
          <TraceRow
            verb="Probing"
            target="@solana/kit"
            detail="inspect_package → full API surface"
            status="running"
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
          <GateRow name="typecheck" state="pass" detail="clean" output="tsc --noEmit · 0 errors" />
          <GateRow name="lint" state="pass" detail="clean" />
          <GateRow
            name="boot"
            state="skipped"
            detail="could not run"
            output="No device attached — gate skipped, not failed."
          />
          <ThinkingRow elapsed={elapsed} />
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
  stream: { flex: 1, marginTop: Spacing.s16 },
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
