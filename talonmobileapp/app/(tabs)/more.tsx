import React from 'react';
import { ScrollView, StyleSheet, View } from 'react-native';
import { useRouter } from 'expo-router';
import { Badge, Card, GhostButton, Screen, SubtleCard, UIText } from '@/components/ui';
import { Colors, Spacing } from '@/constants/theme';
import { useActiveProject, useMe, useNotifications, useUsage } from '@/hooks';
import { memoryApi } from '@/lib/api';
import { clearToken } from '@/lib/auth';
import { useQuery } from '@tanstack/react-query';

// More tab: usage (real cents), profile tier, project memory (what the
// agent knows), notifications (E: OneSignal-backed in-app history), and
// sign-out. No dummy meters, no seed memories.

export default function MoreScreen() {
  const router = useRouter();
  const project = useActiveProject();
  const projectId = project?.id ?? null;
  const { data: usage } = useUsage();
  const { data: me } = useMe();
  const { data: notifications } = useNotifications();
  const { data: memory } = useQuery({
    queryKey: ['memory', projectId],
    enabled: projectId != null,
    queryFn: () => memoryApi.get(projectId!),
  });

  const used = usage?.used_cents ?? 0;
  const limit = usage?.limit_cents ?? 1;
  const pct = Math.max(0, Math.min(100, Math.round((used / limit) * 100)));
  const usedDollars = (used / 100).toFixed(2);
  const limitDollars = (limit / 100).toFixed(2);

  const memories = memory?.memories ?? [];

  async function signOut() {
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
                ${usedDollars} / ${limitDollars}
              </UIText>
            </View>
            <View style={styles.bar}>
              <View style={[styles.fill, { flex: Math.max(pct, 1) }]} />
              <View style={{ flex: Math.max(100 - pct, 1) }} />
            </View>
            <UIText variant="caption" tone="muted">
              billed per prompt — device minutes included
            </UIText>
          </Card>

          {/* Identity: /v1/me is the truth the settings surface reads. */}
          <Card>
            <UIText variant="title" tone="hero">
              {me?.email ?? 'Signed out'}
            </UIText>
            <UIText variant="bodySm" tone="muted">
              {me?.name ? me.name : 'GitHub/Google identity'}
            </UIText>
          </Card>

          {/* Notifications: the in-app history of what OneSignal pushed. */}
          <UIText variant="title" tone="hero">
            Notifications
          </UIText>
          {(notifications ?? []).length === 0 && (
            <UIText variant="bodySm" tone="muted" style={styles.empty}>
              Nothing yet — input-needed pauses and build finishes land here.
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
            Memory
          </UIText>
          {projectId == null && (
            <UIText variant="bodySm" tone="muted" style={styles.empty}>
              No project — memory follows the active project.
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
  empty: { textAlign: 'center', paddingVertical: Spacing.s8 },
});
