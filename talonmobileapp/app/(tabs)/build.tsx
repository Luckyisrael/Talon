import React from 'react';
import { Alert, Pressable, RefreshControl, ScrollView, StyleSheet, Text, View } from 'react-native';
import { useFocusEffect, useRouter } from 'expo-router';
import * as DocumentPicker from 'expo-document-picker';
import * as ImagePicker from 'expo-image-picker';
import { Add, ArrowUp, Stop } from 'iconsax-react-native';
import { Field, GhostButton, Screen, UIText } from '@/components/ui';
import { StagedAttachments } from '@/components/build/Attachments';
import { RestoredTranscript, type RestoredTurn } from '@/components/build/Transcript';
import { TraceRow } from '@/components/build/TraceRow';
import { PixelGrid } from '@/components/build/PixelGrid';
import { QuestionCard } from '@/components/build/QuestionCard';
import { FileBrowser } from '@/components/files/FileBrowser';
import { Colors, Spacing, Type } from '@/constants/theme';
import { useActiveProject, usePromptStream } from '@/hooks';
import { attachmentsApi, conversationApi, type PlanTask } from '@/lib/api';
import { loadSnapshot, saveSnapshot } from '@/lib/history-cache';
import {
  MAX_ATTACHMENTS,
  classifyPicked,
  describeSize,
  type LocalAttachment,
} from '@/lib/attachments';

// The build tab drives the real prompt stream: send a plan/build prompt,
// watch tool calls and task updates land live, answer ask_user pauses
// through the product's own answer endpoint. No echoes, no seed rows.

const TOOL_LABELS: Record<string, { label: string; target?: string }> = {
  write_file: { label: 'Wrote' },
  read_file: { label: 'Read' },
  run_typecheck: { label: 'Checked', target: 'run_typecheck' },
  run_lint: { label: 'Linted', target: 'run_lint' },
  run_bundle: { label: 'Bundled', target: 'run_bundle' },
  build_apk: { label: 'Assembling', target: 'run_gradle' },
  run_boot: { label: 'Booted', target: 'run_boot' },
  run_vision: { label: 'Reviewed', target: 'run_vision' },
  run_interact: { label: 'Reached', target: 'run_interact' },
  run_shell: { label: 'Probing' },
  read_skill: { label: 'Consulted skill' },
  explore: { label: 'Exploring' },
};

function toolRow(name: string) {
  const base = name.startsWith('Reading ')
    ? { label: 'Read', target: name.slice(8) }
    : name.startsWith('Probing ')
      ? { label: 'Probing', target: name.slice(8) }
      : name.startsWith('Consulting ')
        ? { label: 'Consulted skill', target: name.slice(11) }
        : name.startsWith('Wrote ')
          ? { label: 'Wrote', target: name.slice(6) }
          : TOOL_LABELS[name.split(' ')[0]] ?? { label: name };
  return base;
}

export default function BuildScreen() {
  const router = useRouter();
  // The composer belongs to a project: select or create one on the Projects
  // screen first. The input box only exists under a project, and it pins to
  // the bottom of this screen in every sub-view.
  const project = useActiveProject();
  const projectId = project?.id ?? null;
  const { turn, send, answer, answerPrompt, stop } = usePromptStream(projectId);
  const [draft, setDraft] = React.useState('');
  const [mode, setMode] = React.useState<'plan' | 'build'>('build');
  const [answers, setAnswers] = React.useState<string[]>([]);
  const [subView, setSubView] = React.useState<'chat' | 'files'>('chat');
  const [staged, setStaged] = React.useState<LocalAttachment[]>([]);
  const [attError, setAttError] = React.useState<string | null>(null);
  const [uploading, setUploading] = React.useState(false);
  const keyRef = React.useRef(0);
  const [history, setHistory] = React.useState<RestoredTurn[]>([]);
  const [historyStale, setHistoryStale] = React.useState(false);
  const [refreshing, setRefreshing] = React.useState(false);
  const [waitingId, setWaitingId] = React.useState<number | null>(null);

  const loadHistory = React.useCallback(
    async (opts?: { silent?: boolean }) => {
      if (!projectId) return;
      if (!opts?.silent) setRefreshing(true);
      try {
        const r = await conversationApi.get(projectId);
        setHistory((r.turns ?? []) as RestoredTurn[]);
        setWaitingId(r.latest?.status === 'waiting' ? (r.latest.id ?? null) : null);
        setHistoryStale(false);
        void saveSnapshot(projectId, r.turns ?? []);
      } catch {
        // Offline keeps the snapshot with its stale badge; a first-run
        // failure simply leaves the empty state.
      } finally {
        if (!opts?.silent) setRefreshing(false);
      }
    },
    [projectId],
  );

  // Cold start paints the snapshot instantly, then the network replaces it.
  // Turn end, project switch, and tab focus re-sync: the server owns truth,
  // this screen never merges.
  React.useEffect(() => {
    if (!projectId) {
      setHistory([]);
      return;
    }
    let alive = true;
    void loadSnapshot(projectId).then((snap) => {
      if (alive && snap && snap.turns.length > 0) {
        setHistory(snap.turns as RestoredTurn[]);
        setHistoryStale(true);
      }
    });
    void loadHistory({ silent: true });
    return () => {
      alive = false;
    };
  }, [projectId, loadHistory]);
  React.useEffect(() => {
    if (turn.phase === 'done' || turn.phase === 'error') void loadHistory({ silent: true });
  }, [turn.phase, loadHistory]);
  useFocusEffect(
    React.useCallback(() => {
      void loadHistory({ silent: true });
    }, [loadHistory]),
  );

  const busy = turn.phase === 'streaming';

  function addPicked(assets: { uri: string; name?: string | null; mimeType?: string | null; fileSize?: number | null }[]) {
    if (pendingBlock()) return;
    const next = [...staged];
    let err: string | null = null;
    for (const a of assets) {
      if (next.length >= MAX_ATTACHMENTS) {
        err = `at most ${MAX_ATTACHMENTS} files per turn`;
        break;
      }
      const verdict = classifyPicked({ name: a.name, mimeType: a.mimeType, size: a.fileSize });
      if ('error' in verdict) {
        err = verdict.error;
        continue;
      }
      keyRef.current += 1;
      next.push({
        key: `${Date.now()}-${keyRef.current}`,
        uri: a.uri,
        mime: a.mimeType ?? (verdict.kind === 'image' ? 'image/jpeg' : 'text/markdown'),
        kind: verdict.kind,
        name: a.name ?? (verdict.kind === 'image' ? 'photo' : 'notes.md'),
        sizeLabel: a.fileSize != null ? describeSize(a.fileSize) : '',
      });
    }
    setAttError(err);
    setStaged(next);
  }

  // Steering and attachments do not mix: steers carry text to a running
  // turn, files claim to the next fresh turn.
  function pendingBlock(): boolean {
    if (busy) {
      setAttError('wait for this turn to finish, files send with the next one');
      return true;
    }
    return false;
  }

  function pickAttach() {
    if (pendingBlock()) return;
    Alert.alert('Attach', undefined, [
      {
        text: 'Photo',
        onPress: async () => {
          const res = await ImagePicker.launchImageLibraryAsync({
            quality: 0.8,
            allowsMultipleSelection: true,
            selectionLimit: MAX_ATTACHMENTS - staged.length,
          });
          if (!res.canceled) addPicked(res.assets);
        },
      },
      {
        text: 'Markdown file',
        onPress: async () => {
          const res = await DocumentPicker.getDocumentAsync({
            type: ['text/markdown', 'text/plain', 'application/octet-stream'],
            multiple: true,
            copyToCacheDirectory: true,
          });
          if (!res.canceled) addPicked(res.assets);
        },
      },
      { text: 'Cancel', style: 'cancel' },
    ]);
  }

  async function submit() {
    const text = draft.trim();
    if (busy || uploading) return;
    if (projectId == null) {
      setAttError('Pick a project first');
      return;
    }
    if (!text) {
      if (staged.length > 0) setAttError('describe what the files are for');
      return;
    }
    // Files upload first, then the turn claims them by id. An upload
    // failure stops the send: a turn whose chips the model never saw
    // would be a lie.
    setUploading(true);
    setAttError(null);
    try {
      const ids: number[] = [];
      for (const a of staged) {
        const rec = await attachmentsApi.upload(projectId, {
          uri: a.uri,
          name: a.name,
          type: a.mime,
        });
        ids.push(rec.id);
      }
      setStaged([]);
      setDraft('');
      setAttError(null);
      setAnswers([]);
      send(mode, text, ids, projectId);
    } catch (e) {
      setAttError(e instanceof Error ? e.message : 'could not start the turn');
    } finally {
      setUploading(false);
    }
  }

  return (
    <Screen topInset={false}>
      <View style={styles.head}>
        <UIText variant="micro" tone="faint">
          {project ? `${project.name.toUpperCase()} · ${mode.toUpperCase()} MODE` : 'BUILD'}
        </UIText>
        <View style={styles.titleRow}>
          <UIText variant="heading" tone="hero" style={styles.title}>
            {project ? project.name : 'No project'}
          </UIText>
          <View style={styles.modeSwitch}>
            {(['plan', 'build'] as const).map((m) => (
              <UIText
                key={m}
                variant="label"
                tone={mode === m ? 'hero' : 'faint'}
                style={[styles.modeTab, mode === m && styles.modeTabOn]}
                onPress={() => setMode(m)}
              >
                {m.toUpperCase()}
              </UIText>
            ))}
          </View>
        </View>
        {projectId && (
          <View style={styles.subTabs}>
            {(['chat', 'files'] as const).map(v => (
              <Pressable key={v} onPress={() => setSubView(v)} style={[styles.subTab, subView === v && styles.subTabOn]}>
                <Text style={{ color: subView === v ? Colors.paper : Colors.fog, fontSize: Type.label.fontSize, fontWeight: '500' }}>{v === 'chat' ? 'Chat' : `Files`}</Text>
              </Pressable>
            ))}
          </View>
        )}
        {turn.phase === 'done' && (
          <UIText variant="bodySm" tone="faint">
            turn finished
          </UIText>
        )}
        {turn.phase === 'error' && (
          <UIText variant="bodySm" tone="faint" style={{ color: Colors.coralRed }}>
            {turn.error}
          </UIText>
        )}
      </View>

      {projectId == null ? (
        <View style={styles.gate}>
          <UIText variant="title" tone="hero" style={styles.center}>
            No project selected.
          </UIText>
          <UIText variant="body" tone="muted" style={styles.center}>
            Pick one from Projects to describe your app. Creating happens there too.
          </UIText>
          <GhostButton title="Open projects" onPress={() => router.push('/menu/projects')} />
        </View>
      ) : subView === 'files' ? (
        <View style={{ flex: 1, marginTop: Spacing.s12 }}>
          <FileBrowser projectId={projectId} />
        </View>
      ) : (
      <ScrollView
        style={styles.stream}
        showsVerticalScrollIndicator={false}
        refreshControl={<RefreshControl refreshing={refreshing} onRefresh={() => void loadHistory()} tintColor={Colors.fog} />}
      >
        <View style={styles.stack}>
          {turn.phase === 'idle' && history.length > 0 && (
            <View style={styles.recent}>
              <View style={styles.recentHead}>
                <UIText variant="label" tone="faint">Transcript</UIText>
                {historyStale && (
                  <UIText variant="monoSm" tone="faint">
                    offline snapshot
                  </UIText>
                )}
              </View>
              {projectId != null && (
                <RestoredTranscript
                  turns={history}
                  projectId={projectId}
                  waitingPromptId={waitingId}
                  onAnswer={(pid, text) => void answerPrompt(pid, text)}
                />
              )}
            </View>
          )}
          {turn.phase === 'idle' && history.length === 0 && (
            <UIText variant="body" tone="muted" style={styles.empty}>
              Describe what you want. The agent plans, writes files, and verifies on the device.
            </UIText>
          )}

          {/* Live task updates, the plan shape the agent is working to. */}
          {turn.tasks.map((t: PlanTask) => (
            <View key={t.id} style={styles.taskRow}>
              <View style={[styles.taskBox, t.status === 'completed' && styles.taskBoxDone]}>
                {t.status === 'completed' && <UIText style={styles.tick}>✓</UIText>}
              </View>
              <View style={styles.taskText}>
                <UIText variant="bodySm" tone={t.status === 'completed' ? 'faint' : 'hero'}>
                  {t.description}
                </UIText>
                {!!t.result && (
                  <UIText variant="caption" tone="muted" numberOfLines={3}>
                    {t.result}
                  </UIText>
                )}
              </View>
            </View>
          ))}

          {/* The live tool trace. */}
          {turn.toolCalls.map((c, i) => {
            const meta = toolRow(c.name);
            return (
              <TraceRow
                key={i}
                tool={c.name.split(' ')[0]}
                label={meta.label}
                target={meta.target ?? c.name}
                status={c.status === 'completed' ? 'done' : c.status === 'error' ? 'fail' : 'running'}
              />
            );
          })}

          {/* The ask_user pause, the negotiation-first law, mobile side. */}
          {turn.phase === 'waiting' && turn.question && (
            <QuestionCard
              question={turn.question}
              choices={['Yes, proceed', 'No, stop']}
              onAnswer={(a) => {
                setAnswers((prev) => [...prev, a]);
                answer(a);
              }}
            />
          )}
          {answers.map((a, i) => (
            <UIText key={i} variant="caption" tone="faint">
              → answered: {a}
            </UIText>
          ))}

          {/* The assistant's streamed text. */}
          {turn.text !== '' && (
            <UIText variant="body" tone="hero" style={styles.echo}>
              {turn.text}
            </UIText>
          )}

          {busy && <PixelGrid label="Working" variant="Drive" />}
        </View>
      </ScrollView>
      )}

      {projectId != null && (
      <View style={styles.composer}>
        <StagedAttachments
          items={staged}
          onRemove={(key) => setStaged((prev) => prev.filter((a) => a.key !== key))}
          error={attError}
        />
        <View style={styles.composerRow}>
          <Pressable
            onPress={pickAttach}
            disabled={busy || uploading}
            accessibilityLabel="Attach a photo or markdown file"
            style={({ pressed }) => [
              styles.attachBtn,
              pressed && !(busy || uploading) && styles.pressed,
              (busy || uploading) && styles.btnOff,
            ]}
          >
            <Add size={20} color={Colors.mist} variant="Linear" />
          </Pressable>
          <Field
            value={draft}
            onChangeText={setDraft}
            placeholder="Tell the agent what to do…"
            multiline
            style={styles.input}
          />
          {busy ? (
            <Pressable
              onPress={stop}
              accessibilityLabel="Stop this turn"
              style={({ pressed }) => [styles.stopBtn, pressed && styles.pressed]}
            >
              <Stop size={20} color={Colors.coralRed} variant="Bold" />
            </Pressable>
          ) : (
            <Pressable
              onPress={submit}
              disabled={!draft.trim() || uploading}
              accessibilityLabel="Send"
              style={({ pressed }) => [
                styles.sendBtn,
                pressed && styles.pressed,
                (!draft.trim() || uploading) && styles.btnOff,
              ]}
            >
              <ArrowUp size={20} color={Colors.acidLimeInk} variant="Linear" />
            </Pressable>
          )}
        </View>
      </View>
      )}
    </Screen>
  );
}

const styles = StyleSheet.create({
  head: { gap: 8, marginBottom: Spacing.s4 },
  titleRow: { flexDirection: 'row', alignItems: 'center', justifyContent: 'space-between' },
  title: { flexShrink: 1 },
  modeSwitch: { flexDirection: 'row', gap: Spacing.s8 },
  modeTab: { paddingHorizontal: 8, paddingVertical: 4 },
  modeTabOn: {
    color: Colors.acidLime,
    backgroundColor: Colors.obsidian,
    borderRadius: 4,
  },
  subTabs: { flexDirection: 'row', gap: 8, marginTop: 4 },
  subTab: { paddingHorizontal: 12, paddingVertical: 6, borderRadius: 9999, backgroundColor: Colors.carbon },
  subTabOn: { backgroundColor: Colors.graphite },
  stream: { flex: 1, marginTop: Spacing.s12 },
  stack: { gap: Spacing.s12, paddingBottom: Spacing.s16 },
  empty: { textAlign: 'center', paddingVertical: Spacing.s16 },
  gate: { flex: 1, alignItems: 'center', justifyContent: 'center', gap: Spacing.s12, paddingHorizontal: Spacing.s16 },
  center: { textAlign: 'center' },
  taskRow: { flexDirection: 'row', gap: Spacing.s12, minHeight: 40 },
  taskBox: {
    width: 20,
    height: 20,
    marginTop: 2,
    borderRadius: 4,
    borderWidth: StyleSheet.hairlineWidth,
    borderColor: Colors.graphite,
    alignItems: 'center',
    justifyContent: 'center',
  },
  taskBoxDone: { backgroundColor: Colors.acidLime, borderColor: Colors.acidLime },
  tick: { color: Colors.acidLimeInk, fontSize: 13, fontWeight: '700', lineHeight: 16 },
  taskText: { flex: 1 },
  echo: {
    backgroundColor: 'rgba(228,242,34,0.08)',
    borderRadius: 6,
    padding: Spacing.s12,
  },
  composer: { gap: Spacing.s8, paddingTop: Spacing.s8 },
  composerRow: { flexDirection: 'row', gap: Spacing.s8, alignItems: 'flex-end' },
  input: { flex: 1 },
  attachBtn: {
    width: 48,
    height: 48,
    borderRadius: 24,
    backgroundColor: Colors.obsidian,
    alignItems: 'center',
    justifyContent: 'center',
    flexShrink: 0,
  },
  sendBtn: {
    width: 48,
    height: 48,
    borderRadius: 24,
    backgroundColor: Colors.acidLime,
    alignItems: 'center',
    justifyContent: 'center',
    flexShrink: 0,
  },
  stopBtn: {
    width: 48,
    height: 48,
    borderRadius: 24,
    backgroundColor: Colors.obsidian,
    alignItems: 'center',
    justifyContent: 'center',
    flexShrink: 0,
  },
  pressed: { opacity: 0.7 },
  btnOff: { opacity: 0.35 },
  recent: { gap: 8, padding: 10, backgroundColor: Colors.obsidian, borderRadius: 8 },
  recentHead: { flexDirection: 'row', alignItems: 'center', justifyContent: 'space-between' },
});
