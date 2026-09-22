package lsp

import (
	"sort"
	"strings"
	"unicode/utf8"
)

// TextEdit is one replacement: 1-based line/col bounds (the tool surface's
// convention) over UTF-16 columns — the LSP wire unit — plus the text to
// put there. Byte-offset conversion lives in ApplyEdits.
type TextEdit struct {
	StartLine, StartCol int
	EndLine, EndCol     int
	NewText             string
}

// ApplyEdits applies edits to content in one pass: each 1-based range
// becomes byte offsets (clamped to the document), then spans splice back
// to front so earlier offsets stay valid — edits from organize-imports do
// not overlap, and for the ones that somehow do the clamp degrades to a
// safe deterministic result rather than a corrupted write. The result is
// byte-identical to content when edits are empty or already-applied
// non-changes, so callers can diff cheaply.
func ApplyEdits(content string, edits []TextEdit) string {
	if len(edits) == 0 {
		return content
	}
	type span struct {
		from, to int
		text     string
	}
	spans := make([]span, 0, len(edits))
	for _, e := range edits {
		from := offsetOf(content, e.StartLine, e.StartCol)
		to := offsetOf(content, e.EndLine, e.EndCol)
		if to < from {
			from, to = to, from
		}
		spans = append(spans, span{from, to, e.NewText})
	}
	sort.Slice(spans, func(i, j int) bool { return spans[i].from > spans[j].from })

	out := []byte(content)
	for _, s := range spans {
		if s.from > len(out) {
			continue
		}
		if s.to > len(out) {
			s.to = len(out)
		}
		merged := make([]byte, 0, len(out)-(s.to-s.from)+len(s.text))
		merged = append(merged, out[:s.from]...)
		merged = append(merged, s.text...)
		merged = append(merged, out[s.to:]...)
		out = merged
	}
	return string(out)
}

// offsetOf converts a 1-based line/column position to a byte offset in
// content, clamped to the document (line past EOF → end; column past the
// line → the newline). Columns count UTF-16 units — the LSP wire unit, a
// non-BMP rune is two — which is one unit per byte for ASCII, i.e. every
// import line short of emoji.
func offsetOf(content string, line, col int) int {
	pos := 0
	for cur := 1; cur < line; cur++ {
		i := strings.IndexByte(content[pos:], '\n')
		if i < 0 {
			return len(content)
		}
		pos += i + 1
	}
	units := col - 1
	if units < 0 {
		units = 0
	}
	i := pos
	for units > 0 && i < len(content) && content[i] != '\n' {
		r, size := utf8.DecodeRuneInString(content[i:])
		if r <= 0xFFFF {
			units--
		} else {
			units -= 2
		}
		i += size
	}
	return i
}
