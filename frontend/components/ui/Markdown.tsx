"use client";

import { Fragment, memo, useCallback, useState, type ReactNode } from "react";
import { copyText } from "@/lib/clipboard";

/**
 * A small Markdown renderer for the agent's prose.
 *
 * The chat used to render every reply as `whitespace-pre-wrap` text, so a reply
 * containing a code block — which most of them do — arrived as a wall of
 * unhighlighted characters with literal backticks in it.
 *
 * This builds React nodes rather than HTML strings. Nothing the model writes can
 * become markup, which matters because the model quotes web pages it fetched and
 * files it read. It covers the subset the agent actually produces: fenced code,
 * inline code, bold, italic, links, images, tables, task lists, headings,
 * lists, blockquotes, rules.
 */

type Block =
  | { kind: "p"; text: string }
  | { kind: "h"; level: number; text: string }
  | { kind: "code"; lang: string; body: string }
  | { kind: "ul"; items: string[] }
  | { kind: "ol"; items: string[] }
  | { kind: "quote"; text: string }
  | { kind: "table"; head: string[]; rows: string[][] }
  | { kind: "img"; alt: string; src: string }
  | { kind: "hr" };

const FENCE = /^\s{0,3}(`{3,}|~{3,})\s*([\w.+-]*)\s*$/;
const HEADING = /^\s{0,3}(#{1,6})\s+(.*)$/;
const RULE = /^\s{0,3}([-*_])\s*(?:\1\s*){2,}$/;
const BULLET = /^\s*[-*+]\s+(.*)$/;
const NUMBER = /^\s*\d{1,9}[.)]\s+(.*)$/;
const QUOTE = /^\s{0,3}>\s?(.*)$/;
const BLOCK_IMAGE = /^\s*!\[([^\]\n]*)\]\(([^()\s]*(?:\([^()\s]*\)[^()\s]*)*)\)\s*$/;
const TASK_ITEM = /^\[( |x|X)\]\s+(.*)$/;

// A table is a header line containing pipes immediately followed by a
// delimiter row (| --- | :---: | ---: |). Both halves are required: a line
// with pipes but no delimiter below stays an ordinary paragraph, so shell
// snippets and prose mentioning `a | b` never become tables by accident.
function splitTableRow(line: string): string[] {
  let s = line.trim();
  if (s.startsWith("|")) s = s.slice(1);
  if (s.endsWith("|")) s = s.slice(0, -1);
  return s.split("|").map((c) => c.trim());
}

function isDelimiterRow(line: string): boolean {
  const cells = splitTableRow(line);
  if (cells.length === 0) return false;
  return cells.every((c) => /^:?-{3,}:?$/.test(c));
}

function parseBlocks(src: string): Block[] {
  const lines = src.replace(/\r\n?/g, "\n").split("\n");
  const blocks: Block[] = [];
  let i = 0;

  while (i < lines.length) {
    const line = lines[i];

    if (line.trim() === "") {
      i++;
      continue;
    }

    const fence = line.match(FENCE);
    if (fence) {
      const marker = fence[1][0];
      const body: string[] = [];
      i++;
      // An unterminated fence is normal mid-stream: the closing ``` has not
      // arrived yet. Render what we have rather than dropping the block.
      while (i < lines.length) {
        const close = lines[i].match(FENCE);
        if (close && close[1][0] === marker) {
          i++;
          break;
        }
        body.push(lines[i]);
        i++;
      }
      blocks.push({ kind: "code", lang: fence[2] || "", body: body.join("\n") });
      continue;
    }

    const heading = line.match(HEADING);
    if (heading) {
      blocks.push({ kind: "h", level: heading[1].length, text: heading[2].trim() });
      i++;
      continue;
    }

    if (RULE.test(line)) {
      blocks.push({ kind: "hr" });
      i++;
      continue;
    }

    if (QUOTE.test(line)) {
      const parts: string[] = [];
      while (i < lines.length) {
        const q = lines[i].match(QUOTE);
        if (!q) break;
        parts.push(q[1]);
        i++;
      }
      blocks.push({ kind: "quote", text: parts.join(" ").trim() });
      continue;
    }

    // A lone image line renders as a linked figure rather than a paragraph
    // carrying image syntax. Unsafe destinations fall back to alt text.
    const blockImage = line.match(BLOCK_IMAGE);
    if (blockImage) {
      blocks.push({ kind: "img", alt: blockImage[1], src: blockImage[2] });
      i++;
      continue;
    }

    if (
      line.includes("|") &&
      i + 1 < lines.length &&
      splitTableRow(lines[i + 1]).length === splitTableRow(line).length &&
      isDelimiterRow(lines[i + 1])
    ) {
      const head = splitTableRow(line);
      i += 2;
      const rows: string[][] = [];
      // Body rows keep coming while lines are non-blank and carry pipes. A
      // ragged row is padded to the header width so columns never shift.
      while (i < lines.length) {
        const l = lines[i];
        if (l.trim() === "" || !l.includes("|")) break;
        const cells = splitTableRow(l);
        while (cells.length < head.length) cells.push("");
        rows.push(cells.slice(0, head.length));
        i++;
      }
      blocks.push({ kind: "table", head, rows });
      continue;
    }

    if (BULLET.test(line) || NUMBER.test(line)) {
      const ordered = !BULLET.test(line);
      const items: string[] = [];
      while (i < lines.length) {
        const m = lines[i].match(ordered ? NUMBER : BULLET);
        if (m) {
          items.push(m[1]);
          i++;
          continue;
        }
        // A wrapped continuation line belongs to the item above it.
        if (
          lines[i].trim() !== "" &&
          /^\s{2,}\S/.test(lines[i]) &&
          !FENCE.test(lines[i]) &&
          items.length > 0
        ) {
          items[items.length - 1] += " " + lines[i].trim();
          i++;
          continue;
        }
        break;
      }
      blocks.push({ kind: ordered ? "ol" : "ul", items });
      continue;
    }

    const para: string[] = [];
    while (i < lines.length) {
      const l = lines[i];
      if (
        l.trim() === "" ||
        FENCE.test(l) ||
        HEADING.test(l) ||
        RULE.test(l) ||
        BULLET.test(l) ||
        NUMBER.test(l) ||
        QUOTE.test(l) ||
        BLOCK_IMAGE.test(l) ||
        (l.includes("|") &&
          i + 1 < lines.length &&
          splitTableRow(lines[i + 1]).length === splitTableRow(l).length &&
          isDelimiterRow(lines[i + 1]))
      ) {
        break;
      }
      para.push(l.trim());
      i++;
    }
    blocks.push({ kind: "p", text: para.join(" ") });
  }

  return blocks;
}

// Only these schemes become clickable. The agent quotes URLs it read off the
// web, and a javascript: link rendered as an anchor would be a real hole.
function safeHref(href: string): string | null {
  const trimmed = href.trim();
  if (/^(https?:|mailto:)/i.test(trimmed)) return trimmed;
  if (trimmed.startsWith("/") || trimmed.startsWith("#")) return trimmed;
  return null;
}

type Rule = { re: RegExp; render: (m: RegExpExecArray, key: string) => ReactNode };

// Every pattern is anchored so that match.index is the position of the opening
// marker itself. That is what lets the scanner below simply take whichever rule
// matches earliest, with no per-rule offset arithmetic.
const INLINE_RULES: Rule[] = [
  {
    // Code spans win over everything: `**not bold**` is literal inside them.
    re: /`([^`\n]+)`/,
    render: (m, key) => <code key={key}>{m[1]}</code>,
  },
  {
    // Images before links: the link pattern would otherwise eat the
    // `[alt](src)` half of `![alt](src)` and leave a stray `!`.
    // The destination allows one level of balanced parens so URLs like
    // `…/shot(1).png` — and hostile `javascript:alert(1)` — match whole.
    re: /!\[([^\]\n]*)\]\(([^()\s]*(?:\([^()\s]*\)[^()\s]*)*)\)/,
    render: (m, key) => {
      const href = safeHref(m[2]);
      if (!href) return <Fragment key={key}>{m[1] || m[2]}</Fragment>;
      return (
        <a key={key} href={href} target="_blank" rel="noopener noreferrer" title="Open full size">
          <img src={href} alt={m[1]} loading="lazy" />
        </a>
      );
    },
  },
  {
    re: /\[([^\]\n]*)\]\(([^)\s]+)\)/,
    render: (m, key) => {
      const href = safeHref(m[2]);
      const label = m[1] || m[2];
      if (!href) return <Fragment key={key}>{label}</Fragment>;
      return (
        <a key={key} href={href} target="_blank" rel="noopener noreferrer">
          {label}
        </a>
      );
    },
  },
  {
    re: /\*\*(?!\s)([^\n]+?)(?<!\s)\*\*/,
    render: (m, key) => <strong key={key}>{renderInline(m[1], key)}</strong>,
  },
  {
    re: /(?<![_\w])__(?!\s)([^\n]+?)(?<!\s)__(?![_\w])/,
    render: (m, key) => <strong key={key}>{renderInline(m[1], key)}</strong>,
  },
  {
    re: /(?<![*\w])\*(?!\s)([^*\n]+?)(?<!\s)\*(?![*\w])/,
    render: (m, key) => <em key={key}>{renderInline(m[1], key)}</em>,
  },
  {
    // Underscores inside identifiers are not emphasis — snake_case_names are
    // everywhere in the paths and symbols the agent talks about.
    re: /(?<![_\w])_(?!\s)([^_\n]+?)(?<!\s)_(?![_\w])/,
    render: (m, key) => <em key={key}>{renderInline(m[1], key)}</em>,
  },
  {
    re: /(?<![("\w])(https?:\/\/[^\s<>")\]]*[^\s<>")\].,;:!?])/,
    render: (m, key) => (
      <a key={key} href={m[1]} target="_blank" rel="noopener noreferrer">
        {m[1]}
      </a>
    ),
  },
];

function renderInline(text: string, keyPrefix: string): ReactNode[] {
  if (!text) return [];

  const out: ReactNode[] = [];
  let rest = text;
  let n = 0;

  // Guard against a rule that matches an empty span: without it a bad pattern
  // would spin here forever and hang the tab.
  while (rest.length > 0 && n < 500) {
    let best: { rule: Rule; match: RegExpExecArray } | null = null;
    for (const rule of INLINE_RULES) {
      const match = rule.re.exec(rest);
      if (!match || match[0].length === 0) continue;
      if (best === null || match.index < best.match.index) {
        best = { rule, match };
      }
    }

    if (!best) {
      out.push(rest);
      break;
    }

    if (best.match.index > 0) out.push(rest.slice(0, best.match.index));
    out.push(best.rule.render(best.match, `${keyPrefix}-i${n}`));
    rest = rest.slice(best.match.index + best.match[0].length);
    n++;
  }

  if (n >= 500 && rest) out.push(rest);

  return out;
}

function MarkdownImage({ alt, src }: { alt: string; src: string }) {
  const href = safeHref(src);
  // An unsafe destination (javascript:, data:) renders as its alt text —
  // never as a clickable or loadable resource.
  if (!href) return <>{alt}</>;
  return (
    <a href={href} target="_blank" rel="noopener noreferrer" title="Open full size">
      <img src={href} alt={alt} loading="lazy" />
    </a>
  );
}

function TaskItem({ item, idPrefix }: { item: string; idPrefix: string }) {
  const task = item.match(TASK_ITEM);
  if (!task) return <>{renderInline(item, idPrefix)}</>;
  const checked = task[1].toLowerCase() === "x";
  return (
    <>
      <input
        type="checkbox"
        checked={checked}
        readOnly
        tabIndex={-1}
        aria-label={checked ? "done" : "not done"}
      />
      {renderInline(task[2], idPrefix)}
    </>
  );
}

function CodeBlock({ lang, body }: { lang: string; body: string }) {
  const [copied, setCopied] = useState(false);

  const copy = useCallback(() => {
    // Works in insecure contexts too — the Clipboard API alone does not.
    void copyText(body).then((ok) => {
      setCopied(ok);
      if (ok) window.setTimeout(() => setCopied(false), 1400);
    });
  }, [body]);

  return (
    <div className="group relative">
      {lang && (
        <span className="gauge absolute left-3 top-2 select-none opacity-70">{lang}</span>
      )}
      <button
        type="button"
        onClick={copy}
        className="gauge absolute right-2 top-2 rounded-sm border border-graphite bg-carbon px-1.5 py-0.5 opacity-0 transition-opacity group-hover:opacity-100 focus-visible:opacity-100 hover:text-mist"
      >
        {copied ? "Copied" : "Copy"}
      </button>
      <pre className={lang ? "pt-6" : undefined}>
        <code>{body}</code>
      </pre>
    </div>
  );
}

function Markdown({ children, className }: { children: string; className?: string }) {
  const blocks = parseBlocks(children ?? "");

  return (
    <div className={className ? `prose-agent ${className}` : "prose-agent"}>
      {blocks.map((block, i) => {
        const key = `b${i}`;
        switch (block.kind) {
          case "code":
            return <CodeBlock key={key} lang={block.lang} body={block.body} />;
          case "h": {
            const Tag = (block.level <= 2 ? "h2" : "h3") as "h2" | "h3";
            return <Tag key={key}>{renderInline(block.text, key)}</Tag>;
          }
          case "hr":
            return <hr key={key} className="border-0 border-t border-graphite" />;
          case "quote":
            return <blockquote key={key}>{renderInline(block.text, key)}</blockquote>;
          case "ul":
            return (
              <ul key={key}>
                {block.items.map((item, j) => (
                  <li key={`${key}-${j}`}>
                    <TaskItem item={item} idPrefix={`${key}-${j}`} />
                  </li>
                ))}
              </ul>
            );
          case "ol":
            return (
              <ol key={key}>
                {block.items.map((item, j) => (
                  <li key={`${key}-${j}`}>{renderInline(item, `${key}-${j}`)}</li>
                ))}
              </ol>
            );
          case "table":
            return (
              <table key={key}>
                <thead>
                  <tr>
                    {block.head.map((cell, j) => (
                      <th key={`${key}-h${j}`}>{renderInline(cell, `${key}-h${j}`)}</th>
                    ))}
                  </tr>
                </thead>
                {block.rows.length > 0 ? (
                  <tbody>
                    {block.rows.map((row, r) => (
                      <tr key={`${key}-r${r}`}>
                        {row.map((cell, j) => (
                          <td key={`${key}-r${r}c${j}`}>{renderInline(cell, `${key}-r${r}c${j}`)}</td>
                        ))}
                      </tr>
                    ))}
                  </tbody>
                ) : null}
              </table>
            );
          case "img":
            return <MarkdownImage key={key} alt={block.alt} src={block.src} />;
          default:
            return <p key={key}>{renderInline(block.text, key)}</p>;
        }
      })}
    </div>
  );
}

// Streaming re-renders this on every delta, so skip the work when the text has
// not actually changed.
export default memo(Markdown);
