import React from 'react';
import { Pressable, ScrollView, StyleSheet, View } from 'react-native';
import { useRouter } from 'expo-router';
import { Badge, Card, Field, GhostButton, LimeButton, Screen, SubtleCard, UIText } from '@/components/ui';
import { Colors, Spacing } from '@/constants/theme';
import { useActiveProject, useMe, useNotifications, useSaveTriage, useTriage, useUsage } from '@/hooks';
import { authApi, memoryApi } from '@/lib/api';
import { clearToken } from '@/lib/auth';
import { useQuery } from '@tanstack/react-query';

const MENU_LINKS = [
  { label: 'Projects', route: '/menu/projects' },
  { label: 'Upgrade', route: '/menu/upgrade' },
  { label: 'Profile', route: '/menu/profile' },
  { label: 'History', route: '/menu/history' },
  { label: 'Settings', route: '/menu/settings' },
  { label: 'Help & Support', route: '/menu/help' },
  { label: 'Documentation', route: '/menu/docs' },
] as const;

// More tab: usage (real cents), profile tier, billing triage, project memory
// (what the agent knows), notifications (OneSignal-backed in-app history),
// and sign-out. No dummy meters, no seed memories.

const TRIAGE_DECISIONS = ['Undecided', 'Cut over now', 'Cut over on a date', 'Stay on simulator'];

export default function MoreScreen() {
  const router = useRouter();
  const project = useActiveProject();
  const projectId = project?.id ?? null;
  const { data: usage } = useUsage();
  const { data: me } = useMe();
  const { data: notifications } = useNotifications();
  const { data: triage } = useTriage(projectId);
  const saveTriage = useSaveTriage(projectId ?? 0);
  const { data: memory } = useQuery({
    queryKey: ['memory', projectId],
    enabled: projectId != null,
    queryFn: () => memoryApi.get(projectId!),
  });

  const used = usage?.prompts_used ?? 0;
  const limit = usage?.prompts_limit ?? 0;
  const unlimited = limit < 0;
  const pct = unlimited || limit <= 0 ? 0 : Math.max(0, Math.min(100, Math.round((used / limit) * 100)));
  const deviceMinutes = usage?.device_minutes ?? 0;

  const memories = memory?.memories ?? [];

  const [decision, setDecision] = React.useState('');
  const [date, setDate] = React.useState('');
  const [exposure, setExposure] = React.useState('');
  const [notes, setNotes] = React.useState('');
  const [saved, setSaved] = React.useState(false);

  // Pre-fill from the saved triage once, when it arrives.
  React.useEffect(() => {
    if (triage) {
      setDecision(triage.decision || '');
      setDate(triage.cutover_date || '');
      setExposure(triage.exposure_cents ? String(triage.exposure_cents / 100) : '');
      setNotes(triage.notes || '');
    }
  }, [triage]);

  async function signOut() {
    await authApi.revoke().catch(() => {});
    await clearToken();
    router.replace('/auth');
  }

  return (
    <Screen topInset={false}>
      <View style={styles.head}>
        <View style={styles.titles}>
          <UIText variant="micro" tone="faint">
            ACCOUNT
          </UIText>
          <UIText variant="heading" tone="hero">
            More
          </UIText>
        </View>
        <Badge label={me?.tier ?? 'free'} tone="info" />
      </View>

      <ScrollView style={styles.stream} showsVerticalScrollIndicator={false}>
        <View style={styles.stack}>
          {/* Usage meter: real cents from /v1/usage. */}
          <Card>
            <View style={styles.meterHead}>
              <UIText variant="title" tone="hero">
                Usage
              </UIText>
              <UIText variant="monoSm" tone="muted">
                {unlimited ? `${used} prompts · unlimited` : `${used} / ${limit} prompts`}
              </UIText>
            </View>
            <View style={styles.bar}>
              <View style={[styles.fill, { flex: Math.max(pct, 1) }]} />
              <View style={{ flex: Math.max(100 - pct, 1) }} />
            </View>
            <UIText variant="caption" tone="muted">
              billed per prompt · {deviceMinutes} min of device time used
            </UIText>
          </Card>

          {/* Identity: /v1/me is the truth the settings surface reads. */}
          <Card>
            <UIText variant="title" tone="hero">
              {me?.email ?? 'Signed out'}
            </UIText>
            <UIText variant="bodySm" tone="muted">
              {me?.display_name ? me.display_name : 'GitHub/Google identity'}
            </UIText>
          </Card>

          {/* Billing cutover triage for the active project. */}
          {projectId != null && (
            <Card>
              <View style={styles.triage}>
                <UIText variant="title" tone="hero">
                  Billing cutover triage
                </UIText>
                <UIText variant="label" tone="muted">
                  DECISION
                </UIText>
                <View style={styles.decisions}>
                  {TRIAGE_DECISIONS.map((d) => (
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
                  title={saved ? 'Saved' : saveTriage.isPending ? 'Saving…' : 'Save triage'}
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
          )}

          {/* Notifications: the in-app history of what OneSignal pushed. */}
          <UIText variant="title" tone="hero">
            Notifications
          </UIText>
          {(notifications ?? []).length === 0 && (
            <UIText variant="bodySm" tone="muted" style={styles.empty}>
              Nothing yet. Input-needed pauses and build finishes land here.
            </UIText>
          )}
          {(notifications ?? []).map((n) => (
            <SubtleCard key={n.id}>
              <UIText variant="label" tone="faint" style={styles.memType}>
                {n.kind.toUpperCase()}
              </UIText>
              <UIText variant="bodySm" tone="body" numberOfLines={4}>
                {n.payload}
              </UIText>
            </SubtleCard>
          ))}

          <UIText variant="title" tone="hero">
            Workspace
          </UIText>
          <View style={styles.links}>
            {MENU_LINKS.map((l) => (
              <Pressable
                key={l.route}
                onPress={() => router.push(l.route)}
                accessibilityRole="button"
                style={({ pressed }) => [styles.linkRow, pressed && styles.pressed]}
              >
                <UIText variant="body" tone="body">
                  {l.label}
                </UIText>
                <UIText variant="body" tone="faint">
                  ›
                </UIText>
              </Pressable>
            ))}
          </View>

          <UIText variant="title" tone="hero">
            Memory
          </UIText>
          {projectId == null && (
            <UIText variant="bodySm" tone="muted" style={styles.empty}>
              No project. Memory follows the active project.
            </UIText>
          )}
          {projectId != null && memories.length === 0 && (
            <UIText variant="bodySm" tone="muted" style={styles.empty}>
              The agent has not written any memories for this project yet.
            </UIText>
          )}
          {memories.map((m) => (
            <SubtleCard key={m.id}>
              <UIText variant="label" tone="faint" style={styles.memType}>
                {m.kind.toUpperCase()}
              </UIText>
              <UIText variant="bodySm" tone="body">
                {m.text}
              </UIText>
            </SubtleCard>
          ))}

          <GhostButton title="Sign out" onPress={signOut} />
        </View>
      </ScrollView>
    </Screen>
  );
}

const styles = StyleSheet.create({
  head: {
    flexDirection: 'row',
    alignItems: 'flex-start',
    justifyContent: 'space-between',
    gap: Spacing.s12,
    marginBottom: Spacing.s4,
  },
  titles: { gap: 8, flex: 1 },
  stream: { flex: 1, marginTop: Spacing.s12 },
  stack: { gap: Spacing.s12, paddingBottom: Spacing.s32 },
  meterHead: {
    flexDirection: 'row',
    alignItems: 'baseline',
    justifyContent: 'space-between',
  },
  bar: {
    flexDirection: 'row',
    height: 6,
    borderRadius: 3,
    backgroundColor: Colors.graphite,
    overflow: 'hidden',
    marginVertical: Spacing.s8,
  },
  fill: { backgroundColor: Colors.acidLime, borderRadius: 3 },
  memType: { marginBottom: 2 },
  triage: { gap: Spacing.s12 },
  decisions: { gap: Spacing.s8 },
  decisionActive: { borderColor: Colors.acidLime },
  links: { gap: Spacing.s8 },
  linkRow: {
    flexDirection: 'row',
    alignItems: 'center',
    justifyContent: 'space-between',
    minHeight: 52,
    paddingHorizontal: 14,
    borderRadius: 10,
    backgroundColor: Colors.carbon,
  },
  pressed: { opacity: 0.7 },
  empty: { textAlign: 'center', paddingVertical: Spacing.s8 },
});
