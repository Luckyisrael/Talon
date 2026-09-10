import React from 'react';
import { Pressable, StyleSheet, View } from 'react-native';
import Animated, { FadeInDown } from 'react-native-reanimated';
import { Ionicons } from '@expo/vector-icons';
import { UIText } from '@/components/ui';
import { Colors, Spacing } from '@/constants/theme';

export type TraceStatus = 'running' | 'done' | 'fail';

// Tool family → glyph, mirroring web AgentTrace.toolIcon (file / pencil /
// terminal / sparkle). Unknown tools get the dot.
function familyIcon(tool?: string): 'document-text-outline' | 'pencil' | 'terminal' | 'sparkles' | 'ellipse' {
  switch (tool) {
    case 'read_file':
    case 'list_files':
    case 'grep_codebase':
    case 'search_files':
    case 'find_files':
    case 'explore':
    case 'read_skill':
      return 'document-text-outline';
    case 'write_file':
    case 'edit_file':
    case 'multi_edit':
    case 'try_edit':
    case 'keep_edit':
    case 'install_package':
      return 'pencil';
    case 'run_command':
    case 'run_shell':
    case 'run_typecheck':
    case 'run_lint':
    case 'run_checks':
    case 'run_gate':
    case 'run_bundle':
    case 'run_gradle':
    case 'run_boot':
    case 'run_vision':
    case 'run_interact':
    case 'build_apk':
    case 'tail_logcat':
    case 'test_wallet_roundtrip':
    case 'capture_screenshot':
      return 'terminal';
    default:
      return 'ellipse';
  }
}

// TraceRow mirrors web ToolRow: 13px family glyph, 12.5px/500 label (lime
// while running, mist otherwise), mono target chip, running row in lime
// wash with a 2px lime left edge, read_skill lime square marker, expandable
// summaries, fade-up entrance.
export function TraceRow({
  tool,
  label,
  target,
  summary,
  status,
}: {
  tool?: string;
  label: string;
  target?: string;
  summary?: string;
  status: TraceStatus;
}) {
  const [open, setOpen] = React.useState(false);
  const running = status === 'running';
  const decision = tool === 'read_skill';
  const expandable = !!summary && !running;

  return (
    <Animated.View entering={FadeInDown.duration(300)}>
      <Pressable
        disabled={!expandable}
        onPress={() => setOpen((v) => !v)}
        style={[styles.row, running && styles.rowRunning]}
      >
        {running && <View style={styles.edge} />}
        <View style={styles.iconBox}>
          {decision && !running ? (
            <View style={styles.decisionMark} />
          ) : (
            <Ionicons
              name={familyIcon(tool)}
              size={13}
              color={running ? Colors.acidLime : Colors.fog}
            />
          )}
        </View>
        <UIText variant="bodySm" tone={running ? 'accent' : 'body'} style={styles.label}>
          {label}
        </UIText>
        {target ? (
          <View style={styles.chip}>
            <UIText variant="monoSm" tone="muted" numberOfLines={1}>
              {target}
            </UIText>
          </View>
        ) : null}
      </Pressable>
      {expandable && open && summary ? (
        <View style={styles.expanded}>
          <UIText variant="bodySm" tone="muted">
            {summary}
          </UIText>
        </View>
      ) : null}
    </Animated.View>
  );
}

const styles = StyleSheet.create({
  row: {
    flexDirection: 'row',
    alignItems: 'center',
    gap: Spacing.s8,
    minHeight: 30,
    borderRadius: 6,
    paddingHorizontal: 6,
    paddingVertical: 4,
    overflow: 'hidden',
  },
  rowRunning: { backgroundColor: 'rgba(228,242,34,0.10)' },
  edge: {
    position: 'absolute',
    left: 0,
    top: 0,
    bottom: 0,
    width: 2,
    backgroundColor: Colors.acidLime,
  },
  iconBox: { width: 16, alignItems: 'center', justifyContent: 'center' },
  decisionMark: { width: 8, height: 8, borderRadius: 2, backgroundColor: Colors.acidLime },
  label: { fontWeight: '500' },
  chip: {
    flex: 1,
    backgroundColor: 'rgba(255,255,255,0.05)',
    borderRadius: 6,
    paddingHorizontal: 6,
    paddingVertical: 3,
  },
  expanded: {
    marginLeft: 8,
    marginTop: 2,
    paddingLeft: 14,
    borderLeftWidth: StyleSheet.hairlineWidth,
    borderLeftColor: Colors.graphite,
    paddingVertical: 2,
  },
});
