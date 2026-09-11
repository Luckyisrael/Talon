import React from 'react';
import { ScrollView, StyleSheet, View } from 'react-native';
import { Card, Field, GhostButton, LimeButton, Screen, UIText } from '@/components/ui';
import { Colors, Radii, Spacing } from '@/constants/theme';
import { useActiveProject, usePlanTasks, useSaveTriage, useTriage } from '@/hooks';
import type { PlanTask } from '@/lib/api';

// The plan tab renders the project's real plan tasks (persisted plan_json,
// refreshed by the stream's task_updates during a turn) and the billing
// cutover triage — both from the backend, no seed data.

function CheckRow({ task }: { task: PlanTask }) {
  const done = task.status === 'completed';
  return (
    <View style={styles.checkRow}>
      <View style={[styles.box, done && styles.boxDone]}>
        {done && <UIText style={styles.tick}>✓</UIText>}
      </View>
      <View style={styles.checkNameWrap}>
        <UIText variant="bodySm" tone={done ? 'faint' : 'hero'} style={styles.checkName}>
          {task.description || task.id}
        </UIText>
        {!!task.result && (
          <UIText variant="caption" tone="muted" style={styles.checkResult} numberOfLines={3}>
            {task.result}
          </UIText>
        )}
      </View>
    </View>
  );
}

const DECISIONS = ['Undecided', 'Cut over now', 'Cut over on a date', 'Stay on simulator'];

export default function PlanScreen() {
  const project = useActiveProject();
  const projectId = project?.id ?? null;
  const { data: tasks, isLoading } = usePlanTasks(projectId);
  const { data: triage } = useTriage(projectId);
  const saveTriage = useSaveTriage(projectId ?? 0);

  const [decision, setDecision] = React.useState('');
  const [date, setDate] = React.useState('');
  const [exposure, setExposure] = React.useState('');
  const [notes, setNotes] = React.useState('');
  const [saved, setSaved] = React.useState(false);

  // Pre-fill from the saved triage once, when it arrives.
  React.useEffect(() => {
    if (triage) {
      setDecision(triage.decision || DECISIONS[0]);
      setDate(triage.cutover_date || '');
      setExposure(triage.exposure_cents ? String(triage.exposure_cents / 100) : '');
      setNotes(triage.notes || '');
    }
  }, [triage]);

  const screens = (tasks ?? []).filter((t) => t.id.startsWith('plan:screen:'));
  const features = (tasks ?? []).filter((t) => !t.id.startsWith('plan:screen:'));

  return (
    <Screen topInset={false}>
      <View style={styles.head}>
        <UIText variant="micro" tone="faint">
          {project ? project.name.toUpperCase() : 'PLAN'}
        </UIText>
        <UIText variant="heading" tone="hero">
          Plan
        </UIText>
      </View>
      <ScrollView style={styles.stream} showsVerticalScrollIndicator={false}>
        <View style={styles.stack}>
          {!projectId && (
            <UIText variant="body" tone="muted" style={styles.empty}>
              No project yet — create one from the build tab to plan a build.
            </UIText>
          )}
          {projectId && isLoading && (
            <UIText variant="body" tone="muted" style={styles.empty}>
              Loading the plan…
            </UIText>
          )}
          {projectId && !isLoading && tasks && tasks.length === 0 && (
            <UIText variant="body" tone="muted" style={styles.empty}>
              No plan yet — send a plan prompt from the build tab and the tasks land here.
            </UIText>
          )}

          {screens.length > 0 && (
            <>
              <UIText variant="title" tone="hero">
                Screens
              </UIText>
              {screens.map((t) => (
                <CheckRow key={t.id} task={t} />
              ))}
            </>
          )}
          {features.length > 0 && (
            <>
              <UIText variant="title" tone="hero" style={styles.section}>
                Features
              </UIText>
              {features.map((t) => (
                <CheckRow key={t.id} task={t} />
              ))}
            </>
          )}

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
              <UIText variant="label" tone="muted">
                NOTES
              </UIText>
              <Field
                value={notes}
                onChangeText={(v) => { setNotes(v); setSaved(false); }}
                placeholder="Why this decision"
              />
              <LimeButton
                title={saved ? 'Saved ✓' : saveTriage.isPending ? 'Saving…' : 'Save triage'}
                disabled={!projectId || decision === ''}
                onPress={() => {
                  if (!projectId) return;
                  saveTriage.mutate(
                    {
                      decision,
                      cutover_date: date,
                      exposure_cents: Math.round((parseFloat(exposure || '0') || 0) * 100),
                      notes,
                    },
                    { onSuccess: () => setSaved(true) },
                  );
                }}
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
  empty: { textAlign: 'center', paddingVertical: Spacing.s16 },
  checkRow: {
    flexDirection: 'row',
    alignItems: 'flex-start',
    gap: Spacing.s12,
    minHeight: 44,
  },
  box: {
    width: 20,
    height: 20,
    marginTop: 2,
    borderRadius: Radii.badges,
    borderWidth: StyleSheet.hairlineWidth,
    borderColor: Colors.graphite,
    alignItems: 'center',
    justifyContent: 'center',
  },
  boxDone: { backgroundColor: Colors.acidLime, borderColor: Colors.acidLime },
  tick: { color: Colors.acidLimeInk, fontSize: 13, fontWeight: '700', lineHeight: 16 },
  checkNameWrap: { flex: 1 },
  checkName: { flexShrink: 1 },
  checkResult: { marginTop: 2 },
  triage: { gap: Spacing.s12 },
  decisions: { gap: Spacing.s8 },
  decisionActive: { borderColor: Colors.acidLime },
});
