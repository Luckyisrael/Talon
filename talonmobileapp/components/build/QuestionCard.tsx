import React from 'react';
import { StyleSheet, View } from 'react-native';
import { Card, Field, GhostButton, UIText } from '@/components/ui';
import { Spacing } from '@/constants/theme';

// QuestionCard is the approval surface: the agent's question in bodyLg,
// choices as stacked full-width ghost buttons (never side-by-side), optional
// free-text field on top. Dummy in Phase 2, onAnswer just records locally.
export function QuestionCard({
  question,
  choices,
  onAnswer,
}: {
  question: string;
  choices: string[];
  onAnswer: (answer: string) => void;
}) {
  const [text, setText] = React.useState('');
  const [answered, setAnswered] = React.useState<string | null>(null);
  const reply = (answer: string) => {
    setAnswered(answer);
    onAnswer(answer);
  };
  return (
    <Card>
      <View style={styles.body}>
        <UIText variant="bodyLg" tone="hero">
          {question}
        </UIText>
        <Field
          value={text}
          onChangeText={setText}
          placeholder="Or type your own answer…"
          editable={answered === null}
          onSubmitEditing={() => {
            if (text.trim()) reply(text.trim());
          }}
        />
        {choices.map((c) => (
          <GhostButton
            key={c}
            title={c}
            disabled={answered !== null}
            onPress={() => reply(c)}
          />
        ))}
        {answered !== null && (
          <UIText variant="caption" tone="faint">
            Answered: {answered}
          </UIText>
        )}
      </View>
    </Card>
  );
}

const styles = StyleSheet.create({
  body: { gap: Spacing.s12 },
});
