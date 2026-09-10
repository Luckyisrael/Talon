package knowledge

import "strings"

// parseFrontMatter splits a rule or skill file into its front-matter fields and
// body. Front matter is a block delimited by lines containing only "---" at the
// top of the file. We hand-roll it rather than pull in a YAML dependency: the
// format is a handful of known keys we author ourselves, and the rest of this
// codebase favours small hand-rolled parsers over dependencies for exactly this.
//
// Supported value forms:
//
//	key: value      → scalar (surrounding quotes trimmed)
//	key: [a, b, c]  → list
//	key: true       → scalar "true"/"false"; callers compare against "true"
//
// Returns the scalar map, the list map, the remaining body, and whether a valid
// front-matter block was found.
func parseFrontMatter(content string) (scalars map[string]string, lists map[string][]string, body string, ok bool) {
	scalars = map[string]string{}
	lists = map[string][]string{}

	// Strip a leading UTF-8 BOM and surrounding whitespace before looking for "---".
	trimmed := strings.TrimLeft(content, "\uFEFF \t\r\n")
	if !strings.HasPrefix(trimmed, "---") {
		return scalars, lists, content, false
	}
	rest := strings.TrimPrefix(trimmed, "---")
	rest = strings.TrimPrefix(rest, "\r")
	rest = strings.TrimPrefix(rest, "\n")

	lines := strings.Split(rest, "\n")
	end := -1
	for i, line := range lines {
		if strings.TrimSpace(strings.TrimRight(line, "\r")) == "---" {
			end = i
			break
		}
	}
	if end == -1 {
		return scalars, lists, content, false
	}

	for _, raw := range lines[:end] {
		line := strings.TrimSpace(strings.TrimRight(raw, "\r"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		colon := strings.Index(line, ":")
		if colon <= 0 {
			continue
		}
		key := strings.TrimSpace(line[:colon])
		val := strings.TrimSpace(line[colon+1:])

		if strings.HasPrefix(val, "[") && strings.HasSuffix(val, "]") {
			inner := strings.TrimSpace(val[1 : len(val)-1])
			if inner == "" {
				lists[key] = []string{}
				continue
			}
			parts := strings.Split(inner, ",")
			items := make([]string, 0, len(parts))
			for _, p := range parts {
				if p = trimQuotes(p); p != "" {
					items = append(items, p)
				}
			}
			lists[key] = items
			continue
		}
		scalars[key] = trimQuotes(val)
	}

	body = strings.TrimLeft(strings.Join(lines[end+1:], "\n"), "\r\n")
	return scalars, lists, body, true
}

// trimQuotes trims surrounding whitespace and a single layer of matching quotes.
func trimQuotes(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') {
			return s[1 : len(s)-1]
		}
	}
	return s
}
