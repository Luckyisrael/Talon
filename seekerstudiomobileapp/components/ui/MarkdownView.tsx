import React from 'react';
import Markdown from 'react-native-markdown-display';
import { Colors } from '@/constants/theme';

// MarkdownView renders markdown in the midnight voice: mist body, paper
// headings, lime links, carbon code blocks. Used anywhere the agent hands
// back rich text (plan messages, runbooks, help articles).
export function MarkdownView({ children }: { children: string }) {
  return <Markdown style={RULES}>{children}</Markdown>;
}

const RULES = {
  body: { color: Colors.mist, fontSize: 16, lineHeight: 24 },
  heading1: { color: Colors.paper, fontSize: 32, lineHeight: 35, fontWeight: '700', marginVertical: 12 },
  heading2: { color: Colors.paper, fontSize: 24, lineHeight: 30, fontWeight: '600', marginVertical: 10 },
  heading3: { color: Colors.paper, fontSize: 20, lineHeight: 26, fontWeight: '600', marginVertical: 8 },
  paragraph: { marginVertical: 6 },
  list_item: { marginVertical: 3 },
  bullet_list: { marginVertical: 6 },
  ordered_list: { marginVertical: 6 },
  code_inline: {
    color: Colors.mist,
    backgroundColor: 'rgba(255,255,255,0.06)',
    borderRadius: 4,
    paddingHorizontal: 5,
    fontSize: 13,
  },
  fence: {
    color: Colors.mist,
    backgroundColor: Colors.carbon,
    borderColor: Colors.graphite,
    borderWidth: 1,
    borderRadius: 8,
    padding: 12,
    fontSize: 12,
  },
  code_block: {
    color: Colors.mist,
    backgroundColor: Colors.carbon,
    borderColor: Colors.graphite,
    borderWidth: 1,
    borderRadius: 8,
    padding: 12,
    fontSize: 12,
  },
  link: { color: Colors.acidLime },
  blockquote: {
    color: Colors.fog,
    borderLeftColor: Colors.graphite,
    borderLeftWidth: 2,
    paddingLeft: 12,
    marginVertical: 6,
  },
  hr: { backgroundColor: Colors.graphite, height: 1, marginVertical: 12 },
  table: { borderColor: Colors.graphite, borderWidth: 1, borderRadius: 8 },
  th: { color: Colors.paper, padding: 8 },
  td: { color: Colors.mist, padding: 8 },
  tr: { borderBottomColor: Colors.graphite, borderBottomWidth: 1 },
} as never;
