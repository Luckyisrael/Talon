import React, { useEffect, useRef, useState } from 'react';
import { Pressable, StyleSheet, View } from 'react-native';
import { Colors } from '@/constants/theme';
import { UIText } from '@/components/ui';

// L51 / FM-THINKa — the reasoning disclosure.
//
// Every builder ships this shape (CopilotKit's reasoning card: "Thinking…"
// while streaming → "Thought for X seconds" collapsed, expandable; Claude
// Code's collapsed stub; DeepSeek's ReasoningRow): the raw reasoning is NOT
// the interface — the tool/decision/command rows are — and the wall of
// thinking text is one tap away for whoever wants it.
//
// - running  → header ticks: "Thinking… 1.2s"   (duration from this row's
//              first appearance — the moment the burst reached the screen)
// - settled  → header freezes: "Thought for 1.2s"
// - expanded → the full text, capped so a long reasoning never eats the
//              screen it is disclosure'd behind.
//
// L52 / FM-TLKa — `preview` (opt-in): one honest line of the LIVE segment
// beneath the ticking label. The segment's first line is what the model is
// about to do ("now it's looking at how x is structured") — the progress
// the user asked to see while the model works, without ever handing over
// the wall (the raw text stays one tap away, exactly as L51 pinned).
// Settled segments hide it again: duration + tap, nothing to chase.

function formatElapsed(ms: number): string {
  if (ms < 1000) return `${Math.max(0, Math.round(ms))}ms`;
  return `${(ms / 1000).toFixed(1)}s`;
}

/** The segment's first meaningful line, clamped — one row of context, never
 *  a paragraph. Empty when the burst has nothing yet (before the first
 *  delta), so the label alone renders. */
function previewLine(text: string): string {
  const line = text
    .split('\n')
    .map((s) => s.trim())
    .find((s) => s.length > 0);
  if (!line) return '';
  const CLAMP = 90;
  return line.length > CLAMP ? `${line.slice(0, CLAMP)}…` : line;
}

export function ThinkingRow({ text, running, preview }: { text: string; running: boolean; preview?: boolean }) {
  const [expanded, setExpanded] = useState(false);
  const [elapsedMs, setElapsedMs] = useState(0);
  const startRef = useRef(Date.now());

  useEffect(() => {
    if (!running) {
      // Freeze at the flip: the last label this row shows for the burst.
      setElapsedMs(Date.now() - startRef.current);
      return;
    }
    const id = setInterval(() => setElapsedMs(Date.now() - startRef.current), 200);
    return () => clearInterval(id);
  }, [running]);

  const duration = formatElapsed(elapsedMs);
  const label = running ? `Thinking… ${duration}` : `Thought for ${duration}`;
  // L52d: the live line only — collapsed row, running segment. Expanded
  // means the full text is already on screen; settled means the burst is
  // over and there is nothing new to preview.
  const liveLine = preview && running && !expanded ? previewLine(text) : '';

  return (
    <View style={styles.row}>
      <Pressable
        testID="thinking-row-toggle"
        onPress={() => setExpanded((v) => !v)}
        accessibilityRole="button"
        accessibilityState={{ expanded }}
        accessibilityLabel={expanded ? 'Collapse reasoning' : 'Expand reasoning'}
        style={styles.header}
      >
        <UIText variant="caption" tone="faint">
          {label}
        </UIText>
        <UIText variant="caption" tone="faint">
          {expanded ? '▾' : '▸'}
        </UIText>
      </Pressable>
      {liveLine ? (
        <UIText variant="caption" tone="muted" numberOfLines={1} style={styles.preview}>
          {liveLine}
        </UIText>
      ) : null}
      {expanded && (
        <UIText variant="bodySm" tone="muted" style={styles.body}>
          {text}
        </UIText>
      )}
    </View>
  );
}

const styles = StyleSheet.create({
  row: { gap: 4, padding: 10, backgroundColor: Colors.carbon, borderRadius: 8 },
  header: { flexDirection: 'row', alignItems: 'center', justifyContent: 'space-between' },
  preview: { lineHeight: 16 },
  body: { lineHeight: 18 },
});
