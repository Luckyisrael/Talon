import React from 'react';
import { Alert, Pressable, RefreshControl, ScrollView, StyleSheet, Text, View } from 'react-native';
import { useFocusEffect, useRouter } from 'expo-router';
import * as DocumentPicker from 'expo-document-picker';
import * as ImagePicker from 'expo-image-picker';
import { Add, ArrowUp, Stop } from 'iconsax-react-native';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Card, Field, GhostButton, LimeButton, Screen, UIText } from '@/components/ui';
import { StagedAttachments } from '@/components/build/Attachments';
import { RestoredTranscript, type RestoredTurn } from '@/components/build/Transcript';
import { TraceRow } from '@/components/build/TraceRow';
import { PixelGrid } from '@/components/build/PixelGrid';
import { PlanCard } from '@/components/build/PlanCard';
import { QuestionCard } from '@/components/build/QuestionCard';
import { VerifyBadge } from '@/components/build/VerifyBadge';
import { VerifyEvidenceRow } from '@/components/build/VerifyEvidence';
import { FileBrowser } from '@/components/files/FileBrowser';
import { Colors, Spacing, Type } from '@/constants/theme';
import { useActiveProject, usePromptStream } from '@/hooks';
import {
  attachmentsApi,
  conversationApi,
  isResumable,
  projectsApi,
  promptsApi,
  type LatestPrompt,
  type PlanData,
  type PlanTask,
} from '@/lib/api';
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

// One sentence per resumable status, the web's banner copy in miniature:
// say what actually happened, then what Resume does about it. Unknown
// statuses fall to the failure line — isResumable only admits four.
function resumeCopy(latest: LatestPrompt): string {
  if (latest.status === 'running') return 'The turn was interrupted mid-run. Resume picks up where it stopped.';
  if (latest.status === 'cancelled') return 'This turn was stopped early. Resume continues it.';
  if (latest.status === 'paused') return 'This turn hit its round budget while still working.';
  return 'This turn ended with an error. Resume retries from where it stopped.';
}

export default function BuildScreen() {
  const router = useRouter();
  // The composer belongs to a project: select or create one on the Projects
  // screen first. The input box only exists under a project, and it pins to
  // the bottom of this screen in every sub-view.
  const project = useActiveProject();
  const projectId = project?.id ?? null;
  const { turn, send, answer, answerPrompt, resume, discard, steer, parkQueue, stop, diag } = usePromptStream(projectId);
  const [draft, setDraft] = React.useState('');
  const [mode, setMode] = React.useState<'plan' | 'build'>('build');
  // H2 delivery toggle, the web's queueInstead: OFF (default) a submitted
  // message steers the running build or parks with the plan; ON it stays in
  // the composer until this turn ends. Persists until tapped again, one
  // visible decision at a time.
  const [queueInstead, setQueueInstead] = React.useState(false);
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
  // The resume/discard offer: history's latest build turn left in a state
  // that can be picked up again — the web's banner reads the same `latest`
  // row. Every history load recomputes it (a completed turn clears it, a
  // fresh failure re-sets it), and a new send clears it outright.
  const [resumable, setResumable] = React.useState<LatestPrompt | null>(null);

  // M4 union seed: the newest turn may lack its turn_end marker (running,
  // waiting, failed, or legacy pre-marker) — its prompt row's files_touched is
  // the only durable record of what it wrote. One fetch, cached by row id; the
  // marker always wins when present. Mirrors web F8/F10/F11 (files only, never
  // a verdict).
  const seedPromptId = React.useMemo(() => {
    for (let i = history.length - 1; i >= 0; i--) {
      const t = history[i];
      if (t.role === 'user') continue;
      const hasMarker = (t.blocks ?? []).some((b) => b.kind === 'turn_end');
      return !hasMarker && t.prompt_id != null ? t.prompt_id : undefined;
    }
    return undefined;
  }, [history]);
  const { data: seedRow } = useQuery({
    queryKey: ['prompt-row', seedPromptId],
    enabled: seedPromptId != null,
    queryFn: () => promptsApi.get(seedPromptId as number),
  });
  const promptEvidence = React.useMemo(
    () =>
      seedPromptId != null && seedRow?.files_touched?.length
        ? { [seedPromptId]: { files_touched: seedRow.files_touched } }
        : undefined,
    [seedPromptId, seedRow],
  );

  const loadHistory = React.useCallback(
    async (opts?: { silent?: boolean }) => {
      if (!projectId) return;
      if (!opts?.silent) setRefreshing(true);
      try {
        const r = await conversationApi.get(projectId);
        setHistory((r.turns ?? []) as RestoredTurn[]);
        setWaitingId(r.latest?.status === 'waiting' ? (r.latest.id ?? null) : null);
        setResumable(isResumable(r.latest) ? r.latest : null);
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
  // The sweep row lands with the turn: refresh the badge's claim alongside the
  // history refetch that carries the marker it reads from.
  const qc = useQueryClient();
  React.useEffect(() => {
    if (turn.phase === 'done' || turn.phase === 'error') {
      void loadHistory({ silent: true });
      if (projectId != null) void qc.invalidateQueries({ queryKey: ['verify-state', projectId] });
    }
  }, [turn.phase, loadHistory, qc, projectId]);
  useFocusEffect(
    React.useCallback(() => {
      void loadHistory({ silent: true });
    }, [loadHistory]),
  );

  const busy = turn.phase === 'streaming';

  // The optimistic tail is visible while the turn is in flight AND for the
  // brief settled window after done/error, until loadHistory returns the
  // turns for this prompt id — then the transcript carries them and the tail
  // retires so nothing is shown twice. Without the retirement check a finished
  // turn's streamed text and tool rows would duplicate the rows history just
  // loaded.
  const historyHasTurn = React.useMemo(
    () => (turn.promptId != null && history.some((t) => t.prompt_id === turn.promptId)),
    [history, turn.promptId],
  );
  const liveVisible =
    (turn.phase === 'streaming' || turn.phase === 'waiting' || turn.phase === 'done' || turn.phase === 'error') &&
    !historyHasTurn;

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
    if (uploading) return;
    if (projectId == null) {
      setAttError('Pick a project first');
      return;
    }
    // A pending question owns the conversation — the web blocks the composer
    // the same way. The answer continues the paused turn through /answer;
    // a fresh prompt here would strand the agent mid-question. Covers both
    // the live card and the restored one history restored for us.
    if ((turn.phase === 'waiting' && turn.question) || waitingId != null) {
      setAttError('answer the question in the transcript first');
      return;
    }
    // H2 while a turn runs: a submitted message steers the running build by
    // default, or parks for the next turn when the current call is a plan —
    // the web's delivery rules. Queue-instead leaves the draft right where
    // it is (it goes out when this turn ends). Nothing is ever silently
    // dropped: a steer shows its row and its failure, a park shows a parked
    // row until delivery; steer() returning false (the phase event has not
    // named the turn yet) keeps the draft for the retry.
    if (busy) {
      if (queueInstead) return;
      if (!text) return;
      setAttError(null);
      if (turn.planStream) {
        parkQueue(text);
        setDraft('');
      } else if (steer(text)) {
        setDraft('');
      }
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
      setResumable(null);
      send(mode, text, ids, projectId);
    } catch (e) {
      setAttError(e instanceof Error ? e.message : 'could not start the turn');
    } finally {
      setUploading(false);
    }
  }

  // Approve hands the plan to a build turn — the web's approvePlan, same
  // contract: PLAN.md lands in the workspace first (best effort — the plan
  // still lives in the conversation), the mode flips to build, and the build
  // prompt names the screens to implement. Shared by the live plan card and
  // the restored one, so either path builds the same PLAN.md.
  async function approvePlan(plan: PlanData) {
    if (projectId == null || busy) return;
    const screens = (plan.screens ?? []).map((s) => s.name).filter(Boolean);
    const lines = ['# Build plan', ''];
    if (screens.length > 0) {
      lines.push('## Screens');
      for (const s of plan.screens ?? []) {
        lines.push(`- ${s.name}${s.description ? ` — ${s.description}` : ''}`);
      }
      lines.push('');
    }
    if (plan.navigation) lines.push('## Navigation', plan.navigation, '');
    const features = plan.features ?? [];
    if (features.length > 0) lines.push('## Features', ...features.map((f) => `- ${f}`), '');
    const packages = plan.packages ?? [];
    if (packages.length > 0) lines.push('## Packages', ...packages.map((p) => `- ${p}`), '');
    try {
      await projectsApi.writeFiles(projectId, [{ path: 'PLAN.md', content: lines.join('\n') + '\n' }]);
    } catch {
      // The plan still lives in the conversation; the build proceeds.
    }
    const list = screens.length > 0 ? screens.join(', ') : 'the plan we just agreed on';
    setMode('build');
    setResumable(null);
    setAttError(null);
    setAnswers([]);
    send(
      'build',
      `Build ${list} per PLAN.md in the workspace. Implement every screen fully — no placeholders, no TODOs — and wire up the navigation from the plan. Mark each plan item done as you finish it.`,
      [],
      projectId,
    );
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
        {/* Project-level sweep claim — persistent chrome, so it lives in the
            header like the web Topbar's badge, not in the turn's status line.
            Renders nothing unless GET /verify-state 200s a passing sweep. */}
        {projectId != null && <VerifyBadge projectId={projectId} />}
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
          {/* The transcript is always rendered. It is the durable log, and the
              visible list is a projection of it — sending a prompt must append
              below, not replace what is above. It used to gate on a phase that
              flipped to 'streaming' on send, which unmounted the whole history
              the moment you tapped send and made the conversation "disappear".
              The empty state only shows when there is genuinely nothing. */}
          {history.length > 0 && projectId != null && (
            <View style={styles.recent}>
              {historyStale && (
                <UIText variant="monoSm" tone="faint">
                  offline snapshot
                </UIText>
              )}
              <RestoredTranscript
                turns={history}
                projectId={projectId}
                waitingPromptId={waitingId}
                onAnswer={(pid, text) => void answerPrompt(pid, text)}
                onApprovePlan={approvePlan}
                promptEvidence={promptEvidence}
              />
            </View>
          )}
          {history.length === 0 && turn.phase === 'idle' && (
            <UIText variant="body" tone="muted" style={styles.empty}>
              Describe what you want. The agent plans, writes files, and verifies on the device.
            </UIText>
          )}

          {/* The live turn — the optimistic tail. It renders below the
              transcript while work is in flight and retires the moment the
              history absorbs this prompt (loadHistory runs on done/error), so
              rows never duplicate. This is DSH's one-list model: the stream
              appends to the same array the history fills, instead of swapping
              places with it. */}
          {liveVisible && (
            <View style={styles.liveTurn}>
              {turn.userEcho ? (
                <UIText variant="body" tone="hero">
                  {turn.userEcho}
                </UIText>
              ) : null}

              {/* Steered messages: marked rows, honest that they redirect
                  into THIS running turn rather than starting one — a failed
                  delivery lands on its own row (the web's dimmed bubble). */}
              {(turn.steers ?? []).map((s) => (
                <View key={s.id} style={styles.parkedRow}>
                  <UIText variant="caption" tone="muted" numberOfLines={2}>
                    ↳ {s.text}
                  </UIText>
                  {s.error ? (
                    <UIText variant="monoSm" tone="danger">
                      {s.error}
                    </UIText>
                  ) : null}
                </View>
              ))}

              {/* Parked messages: visible and labelled as not-yet-sent — each
                  fires as its own plan turn when this call settles (the web's
                  queued bubble, "queued — sends when the plan lands"). */}
              {(turn.queue ?? []).map((q, i) => (
                <View key={`queued-${i}`} style={styles.parkedRow}>
                  <UIText variant="caption" tone="muted" numberOfLines={2}>
                    {q.text}
                  </UIText>
                  <UIText variant="monoSm" tone="faint">
                    queued — sends when the plan lands
                  </UIText>
                </View>
              ))}

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
                    target={c.target ?? meta.target ?? c.name}
                    status={c.status === 'completed' ? 'done' : c.status === 'error' ? 'fail' : 'running'}
                  />
                );
              })}

              {/* The ask_user pause, the negotiation-first law, mobile side.
                  Choices come from the question frame when the model named
                  them — MWA vs email, plan vs build — not a guessed pair. */}
              {turn.phase === 'waiting' && turn.question && (
                <QuestionCard
                  question={turn.question}
                  choices={
                    turn.questionChoices?.length ? turn.questionChoices : ['Yes, proceed', 'No, stop']
                  }
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

              {/* Live command output — typecheck, lint and build stdout as it
                  prints. The web streams this to a terminal pane and into the
                  trace; the mobile shows the bounded tail, which is what tells
                  you what the tools are doing right now. */}
              {turn.output !== '' && (
                <View style={styles.outputBox}>
                  <UIText variant="monoSm" tone="faint" numberOfLines={8}>
                    {turn.output}
                  </UIText>
                </View>
              )}

              {/* The model's reasoning, streamed live. This is the bulk of
                  what a turn produces while the user watches — a build emits
                  hundreds of thinking deltas — and it is what fills the dead
                  window between sending the prompt and the first tool call.
                  Rendered before the text, as the web orders it: the decision
                  before the act. */}
              {turn.thinking !== '' && (
                <View style={styles.thinkingBox}>
                  <UIText variant="caption" tone="faint">Thinking</UIText>
                  <UIText variant="bodySm" tone="muted" style={styles.thinkingText}>
                    {turn.thinking}
                  </UIText>
                </View>
              )}

              {/* The assistant's streamed text. */}
              {turn.text !== '' && (
                <UIText variant="body" tone="hero" style={styles.echo}>
                  {turn.text}
                </UIText>
              )}

              {/* The done frame's verify evidence — the live twin of the
                  restored marker's footer, so the claim lands when the turn
                  lands, not one refetch later. Undefined until the frame
                  arrives; absent fields claim nothing. */}
              {turn.evidence ? <VerifyEvidenceRow evidence={turn.evidence} /> : null}

              {busy && <PixelGrid label={turn.activity ?? 'Working'} variant="Drive" />}
            </View>
          )}

          {/* The parsed plan outlives the live tail: it renders even after
              history absorbs this turn, until the restored card in the
              transcript above takes over (historyHasTurn) or the next send
              clears it — the approve button never disappears mid-decision. */}
          {turn.plan && !historyHasTurn ? (
            <PlanCard plan={turn.plan} message={turn.planMessage} onApprove={approvePlan} />
          ) : null}

          {/* A settled turn's failure, shown only briefly before loadHistory
              absorbs it into the transcript above. */}
          {!liveVisible && turn.phase === 'error' && (
            <UIText variant="bodySm" tone="faint" style={{ color: Colors.coralRed }}>
              {turn.error}
            </UIText>
          )}

          {/* TEMP DIAGNOSTIC — shows exactly what the SSE stream delivered to
              this app, so we can see whether events arrive live or only at the
              end. Remove once streaming works. */}
          {turn.phase !== 'idle' && (
            <View style={styles.diagBox}>
              <UIText variant="monoSm" tone="faint">
                DIAG events={diag.count} kinds={diag.kinds.join(',') || 'none'}
              </UIText>
              {diag.lastData ? (
                <UIText variant="monoSm" tone="faint" numberOfLines={3}>
                  last: {diag.lastData}
                </UIText>
              ) : null}
            </View>
          )}
        </View>
      </ScrollView>
      )}

      {/* The resume/discard offer for a build turn that died before it
          finished — the web's banner, same honest copy: running means the
          server dropped it, cancelled it was stopped, paused it hit the
          round budget, failed it errored out. Waiting turns are NOT
          resumable (isResumable), so this never competes with a question
          card. */}
      {projectId != null && subView === 'chat' && resumable != null && !busy && (
        <Card style={styles.resumeBanner}>
          <UIText variant="bodySm" tone="muted">
            {resumeCopy(resumable)}
            {resumable.round != null ? ` · reached round ${resumable.round}` : ''}
          </UIText>
          <View style={styles.resumeBtns}>
            <LimeButton
              title="Resume"
              style={styles.resumeBtn}
              onPress={() => {
                const id = resumable.id;
                setResumable(null);
                setAttError(null);
                setMode('build');
                void resume(id);
              }}
            />
            <GhostButton
              title="Discard"
              style={styles.resumeBtn}
              onPress={() => {
                const id = resumable.id;
                void discard(id)
                  .then(() => {
                    setResumable(null);
                    return loadHistory({ silent: true });
                  })
                  .catch(() => {
                    // A failed discard keeps the offer: the turn really is
                    // still resumable, and claiming otherwise would lie.
                  });
              }}
            />
          </View>
        </Card>
      )}

      {projectId != null && (
      <View style={styles.composer}>
        <StagedAttachments
          items={staged}
          onRemove={(key) => setStaged((prev) => prev.filter((a) => a.key !== key))}
          error={attError}
        />
        {/* H2: while a turn runs the mode pill makes way for the delivery
            toggle — steer the running build (default) or park for the next
            turn, one visible decision at a time, like the web's pill. */}
        {busy && (
          <Pressable
            onPress={() => setQueueInstead((q) => !q)}
            accessibilityRole="button"
            accessibilityState={{ selected: queueInstead }}
            accessibilityLabel={queueInstead ? 'Queue for the next turn' : 'Steer the running build'}
            style={[styles.deliveryPill, queueInstead && styles.deliveryPillOn]}
          >
            <UIText variant="monoSm" tone={queueInstead ? 'accent' : 'faint'}>
              {queueInstead ? 'QUEUE INSTEAD' : 'STEERS THE BUILD'}
            </UIText>
          </Pressable>
        )}
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
            placeholder={
              (turn.phase === 'waiting' && turn.question) || waitingId != null
                ? 'Answer the question above to continue…'
                : busy
                  ? queueInstead
                    ? 'Queue for the next turn…'
                    : 'Steer the running build…'
                  : 'Tell the agent what to do…'
            }
            multiline
            style={styles.input}
          />
          {/* Stop and Send coexist while a turn runs: Stop ends the call,
              Send takes the composer's text wherever the toggle says — the
              web shows both buttons in the streaming state. */}
          {busy && (
            <Pressable
              onPress={stop}
              accessibilityLabel="Stop this turn"
              style={({ pressed }) => [styles.stopBtn, pressed && styles.pressed]}
            >
              <Stop size={20} color={Colors.coralRed} variant="Bold" />
            </Pressable>
          )}
          <Pressable
            onPress={submit}
            disabled={!draft.trim() || uploading}
            accessibilityLabel={
              busy
                ? queueInstead
                  ? 'Queue for the next turn'
                  : 'Steer the running build'
                : 'Send'
            }
            style={({ pressed }) => [
              styles.sendBtn,
              pressed && styles.pressed,
              (!draft.trim() || uploading) && styles.btnOff,
            ]}
          >
            <ArrowUp size={20} color={Colors.acidLimeInk} variant="Linear" />
          </Pressable>
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
  composerRow: { flexDirection: 'row', gap: Spacing.s8, alignItems: 'flex-end', marginBottom: Spacing.s12 },
  // The streaming delivery toggle: a small mono pill, self-start above the
  // composer row — lime-washed when armed to queue, quiet when steering.
  deliveryPill: {
    alignSelf: 'flex-start',
    paddingHorizontal: 10,
    paddingVertical: 5,
    borderRadius: 9999,
    backgroundColor: Colors.carbon,
  },
  deliveryPillOn: { backgroundColor: 'rgba(228,242,34,0.12)' },
  // A steered or parked message: its own quiet surface in the live tail, the
  // mobile reading of the web's dimmed / queued bubbles.
  parkedRow: { gap: 2, padding: 8, backgroundColor: Colors.carbon, borderRadius: 6 },
  // The resume offer sits directly above the composer — the place you would
  // have typed the continuation — as one surface with a copy line over a
  // two-button row, Resume carrying the flex.
  resumeBanner: { gap: Spacing.s8, padding: Spacing.s12, marginBottom: Spacing.s8 },
  resumeBtns: { flexDirection: 'row', gap: Spacing.s8 },
  resumeBtn: { flex: 1 },
  input: { flex: 1, borderWidth: 0, borderRadius: 20 },
  attachBtn: {
    width: 48,
    height: 48,
    borderRadius: 20,
    backgroundColor: Colors.obsidian,
    alignItems: 'center',
    justifyContent: 'center',
    flexShrink: 0,
  },
  sendBtn: {
    width: 48,
    height: 48,
    borderRadius: 20,
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
  // The optimistic tail. It sits below the transcript, so the only separation
  // it needs from the settled rows above is a top rule — the conversation is
  // one continuous scroll, not a panel swap.
  liveTurn: { gap: 12, paddingVertical: 8, borderTopWidth: 1, borderTopColor: Colors.graphite, marginTop: 8 },
  // Reasoning is set apart from the model's prose by surface, not colour: it
  // reads as the machine's working, subordinate to what it eventually says.
  thinkingBox: { gap: 4, padding: 10, backgroundColor: Colors.carbon, borderRadius: 8 },
  thinkingText: { lineHeight: 18 },
  outputBox: { padding: 10, backgroundColor: Colors.void, borderRadius: 8 },
  diagBox: { padding: 8, backgroundColor: Colors.carbon, borderRadius: 8, gap: 4 },
});
