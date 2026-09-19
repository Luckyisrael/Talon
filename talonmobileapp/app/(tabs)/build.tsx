import React from 'react';
import { Pressable, ScrollView, StyleSheet, Text, View } from 'react-native';
import { Field, LimeButton, Screen, UIText } from '@/components/ui';
import { TraceRow } from '@/components/build/TraceRow';
import { PixelGrid } from '@/components/build/PixelGrid';
import { QuestionCard } from '@/components/build/QuestionCard';
import { FileBrowser } from '@/components/files/FileBrowser';
import { Colors, Spacing, Type } from '@/constants/theme';
import { useActiveProject, usePromptStream } from '@/hooks';
import { projectsApi } from '@/lib/api';
import type { PlanTask } from '@/lib/api';

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
  const project = useActiveProject();
  const projectId = project?.id ?? null;
  const { turn, send, answer, stop } = usePromptStream(projectId);
  const [draft, setDraft] = React.useState('');
  const [mode, setMode] = React.useState<'plan' | 'build'>('build');
  const [answers, setAnswers] = React.useState<string[]>([]);
  const [subView, setSubView] = React.useState<'chat' | 'files'>('chat');
  const [newName, setNewName] = React.useState('');
  const [creating, setCreating] = React.useState(false);

  const busy = turn.phase === 'streaming';

  function submit() {
    const text = draft.trim();
    if (!text || projectId == null) return;
    setDraft('');
    setAnswers([]);
    send(mode, text);
  }

  async function createProject() {
    const name = newName.trim()
    if (!name) return
    setCreating(true)
    try {
      await projectsApi.create(name)
      setNewName('')
    } catch {}
    setCreating(false)
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
        <View style={{ gap: 12, paddingVertical: 16 }}>
          <UIText variant="body" tone="muted" style={styles.empty}>No project yet — create one to start building.</UIText>
          <View style={{ flexDirection: 'row', gap: 8 }}>
            <View style={{ flex: 1 }}>
              <Field value={newName} onChangeText={setNewName} placeholder="My app" />
            </View>
            <LimeButton title={creating ? '…' : 'Create'} onPress={createProject} disabled={!newName.trim() || creating} style={{ minWidth: 88 }} />
          </View>
        </View>
      ) : subView === 'files' ? (
        <View style={{ flex: 1, marginTop: Spacing.s12 }}>
          <FileBrowser projectId={projectId} />
        </View>
      ) : (
      <ScrollView style={styles.stream} showsVerticalScrollIndicator={false}>
        <View style={styles.stack}>
          {turn.phase === 'idle' && (
            <UIText variant="body" tone="muted" style={styles.empty}>
              Describe what you want. The agent plans, writes files, and verifies on the device.
            </UIText>
          )}

          {/* Live task updates — the plan shape the agent is working to. */}
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

          {/* The ask_user pause — the negotiation-first law, mobile side. */}
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

      {subView === 'chat' && (
      <View style={styles.composer}>
        <Field
          value={draft}
          onChangeText={setDraft}
          placeholder={projectId == null ? 'Create a project first…' : 'Tell the agent what to do…'}
          multiline
          style={styles.input}
        />
        <LimeButton
          title={busy ? 'Stop' : 'Send'}
          style={styles.send}
          disabled={!busy && (!draft.trim() || projectId == null)}
          onPress={busy ? stop : submit}
        />
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
    borderWidth: StyleSheet.hairlineWidth,
    borderColor: Colors.acidLime,
    borderRadius: 4,
  },
  subTabs: { flexDirection: 'row', gap: 8, marginTop: 4 },
  subTab: { paddingHorizontal: 12, paddingVertical: 6, borderRadius: 9999, backgroundColor: Colors.carbon, borderWidth: StyleSheet.hairlineWidth, borderColor: Colors.graphite },
  subTabOn: { backgroundColor: Colors.graphite, borderColor: Colors.mist },
  stream: { flex: 1, marginTop: Spacing.s12 },
  stack: { gap: Spacing.s12, paddingBottom: Spacing.s16 },
  empty: { textAlign: 'center', paddingVertical: Spacing.s16 },
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
  composer: { flexDirection: 'row', gap: Spacing.s8, paddingTop: Spacing.s8 },
  input: { flex: 1 },
  send: { minWidth: 88 },
});
