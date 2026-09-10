import React from 'react';
import { Pressable, ScrollView, StyleSheet, View } from 'react-native';
import { Card, Field, GhostButton, LimeButton, Screen, UIText } from '@/components/ui';
import { Colors, Radii, Spacing } from '@/constants/theme';

type TaskState = 'pending' | 'completed';

interface Task {
  id: string;
  name: string;
  state: TaskState;
}

const SEED_SCREENS: Task[] = [
  { id: 's1', name: 'Welcome', state: 'completed' },
  { id: 's2', name: 'Watchlist', state: 'completed' },
  { id: 's3', name: 'Send', state: 'pending' },
  { id: 's4', name: 'Activity', state: 'pending' },
];

const SEED_FEATURES: Task[] = [
  { id: 'f1', name: 'MWA authorize', state: 'completed' },
  { id: 'f2', name: 'Balance refresh', state: 'pending' },
];

function CheckRow({ task, onToggle }: { task: Task; onToggle: () => void }) {
  const done = task.state === 'completed';
  return (
    <Pressable onPress={onToggle} style={styles.checkRow}>
      <View style={[styles.box, done && styles.boxDone]}>
        {done && <UIText style={styles.tick}>✓</UIText>}
      </View>
      <UIText variant="bodySm" tone={done ? 'faint' : 'hero'} style={styles.checkName}>
        {task.name}
      </UIText>
    </Pressable>
  );
}

const DECISIONS = ['Undecided', 'Cut over now', 'Cut over on a date', 'Stay on simulator'];

export default function PlanScreen() {
  const [screens, setScreens] = React.useState(SEED_SCREENS);
  const [features, setFeatures] = React.useState(SEED_FEATURES);
  const [decision, setDecision] = React.useState(DECISIONS[0]);
  const [date, setDate] = React.useState('');
  const [exposure, setExposure] = React.useState('');
  const [saved, setSaved] = React.useState(false);

  const toggle = (
    list: Task[],
    setList: React.Dispatch<React.SetStateAction<Task[]>>,
    id: string,
  ) =>
    setList(list.map((t) => (t.id === id ? { ...t, state: t.state === 'completed' ? 'pending' : 'completed' } : t)));

  return (
    <Screen topInset={false}>
      <View style={styles.head}>
        <UIText variant="micro" tone="faint">
          DEMO · PLAN
        </UIText>
        <UIText variant="heading" tone="hero">
          Plan
        </UIText>
      </View>
      <ScrollView style={styles.stream} showsVerticalScrollIndicator={false}>
        <View style={styles.stack}>
          <UIText variant="title" tone="hero">
            Screens
          </UIText>
          {screens.map((t) => (
            <CheckRow key={t.id} task={t} onToggle={() => toggle(screens, setScreens, t.id)} />
          ))}
          <UIText variant="title" tone="hero" style={styles.section}>
            Features
          </UIText>
          {features.map((t) => (
            <CheckRow key={t.id} task={t} onToggle={() => toggle(features, setFeatures, t.id)} />
          ))}

          <Card>
            <View style={styles.triage}>
              <UIText variant="title" tone="hero">
                Billing cutover triage
              </UIText>
              <UIText variant="label" tone="muted">
                DECISION
              </UIText>
              <View style={styles.decisions}>
                {DECISIONS.map((d) => (
                  <GhostButton
                    key={d}
                    title={d}
                    style={decision === d ? styles.decisionActive : undefined}
                    onPress={() => {
                      setDecision(d);
                      setSaved(false);
                    }}
                  />
                ))}
              </View>
              <UIText variant="label" tone="muted">
                CUTOVER DATE
              </UIText>
              <Field value={date} onChangeText={(v) => { setDate(v); setSaved(false); }} placeholder="2026-10-01" />
              <UIText variant="label" tone="muted">
                EXPOSURE ($)
              </UIText>
              <Field
                value={exposure}
                onChangeText={(v) => { setExposure(v); setSaved(false); }}
                placeholder="199.99"
                keyboardType="decimal-pad"
              />
              <LimeButton
                title={saved ? 'Saved ✓' : 'Save triage'}
                onPress={() => setSaved(true)}
              />
            </View>
          </Card>
        </View>
      </ScrollView>
    </Screen>
  );
}

const styles = StyleSheet.create({
  head: { gap: 8, marginBottom: Spacing.s4 },
  stream: { flex: 1, marginTop: Spacing.s12 },
  stack: { gap: Spacing.s12, paddingBottom: Spacing.s32 },
  section: { marginTop: Spacing.s16 },
  checkRow: {
    flexDirection: 'row',
    alignItems: 'center',
    gap: Spacing.s12,
    minHeight: 44,
  },
  box: {
    width: 20,
    height: 20,
    borderRadius: Radii.badges,
    borderWidth: StyleSheet.hairlineWidth,
    borderColor: Colors.graphite,
    alignItems: 'center',
    justifyContent: 'center',
  },
  boxDone: { backgroundColor: Colors.acidLime, borderColor: Colors.acidLime },
  tick: { color: Colors.acidLimeInk, fontSize: 13, fontWeight: '700', lineHeight: 16 },
  checkName: { flex: 1 },
  triage: { gap: Spacing.s12 },
  decisions: { gap: Spacing.s8 },
  decisionActive: { borderColor: Colors.acidLime },
});
