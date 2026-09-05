package runtime

import (
	"regexp"
	"strings"
	"time"
)

// Line is one parsed logcat row in `-v time` format:
// `09-05 14:22:31.123  1234  5678 I TagName: message`.
type Line struct {
	Time    time.Time
	PID     string
	Level   string
	Tag     string
	Message string
}

var timeFormat = regexp.MustCompile(`^(\d{2}-\d{2} \d{2}:\d{2}:\d{2}\.\d{3})\s+(\d+)\s+(\d+)\s+([VDIWEF])\s+([^:]+):\s?(.*)$`)

// ParseLine parses one `-v time` row. Rows without a timestamp (brief
// format, headers, blank lines) return ok=false — callers that need timing
// (BootGap) skip them, filters keep the raw text.
func ParseLine(raw string) (Line, bool) {
	m := timeFormat.FindStringSubmatch(strings.TrimSpace(raw))
	if m == nil {
		return Line{}, false
	}
	ts, err := time.ParseInLocation("01-02 15:04:05.000", m[1], time.Local)
	if err != nil {
		return Line{}, false
	}
	// Logcat rows carry no year — stamp the current one so comparisons
	// against real timestamps work. (A device log is always "now".)
	ts = ts.AddDate(time.Now().Year(), 0, 0)
	return Line{Time: ts, PID: m[2], Level: m[4], Tag: strings.TrimSpace(m[5]), Message: m[6]}, true
}

// Head returns the first n raw lines. Tail returns the last n.
func Head(text string, n int) string {
	return firstLines(text, n)
}

// Tail returns the last n raw lines.
func Tail(text string, n int) string {
	lines := splitLines(text)
	if len(lines) <= n {
		return text
	}
	return strings.Join(lines[len(lines)-n:], "\n")
}

// After returns lines after the first line containing marker (exclusive).
// Empty when the marker never appears.
func After(text, marker string) string {
	lines := splitLines(text)
	for i, l := range lines {
		if strings.Contains(l, marker) && i+1 < len(lines) {
			return strings.Join(lines[i+1:], "\n")
		}
	}
	return ""
}

// Contains returns only lines mentioning substr (case-insensitive).
func Contains(text, substr string) string {
	var out []string
	for _, l := range splitLines(text) {
		if strings.Contains(strings.ToLower(l), strings.ToLower(substr)) {
			out = append(out, l)
		}
	}
	return strings.Join(out, "\n")
}

// FirstAppLine returns the first parsed line whose tag or message mentions
// pkg, at or after launch. It is the on-device proof of first launch —
// pidof says the process exists, this says it actually ran code.
func FirstAppLine(text, pkg string, launch time.Time) (Line, bool) {
	lower := strings.ToLower(pkg)
	for _, raw := range splitLines(text) {
		l, ok := ParseLine(raw)
		if !ok || l.Time.Before(launch) {
			continue
		}
		if strings.Contains(strings.ToLower(l.Tag), lower) ||
			strings.Contains(strings.ToLower(l.Message), lower) {
			return l, true
		}
	}
	return Line{}, false
}

// BootGap is the silence between launch and the app's first log line: how
// long the process existed before it said anything. ok=false when the app
// never logged (the alive-but-silent case run_boot fails on).
func BootGap(text, pkg string, launch time.Time) (gap time.Duration, first Line, ok bool) {
	first, ok = FirstAppLine(text, pkg, launch)
	if !ok {
		return 0, Line{}, false
	}
	return first.Time.Sub(launch), first, true
}

// CrashExcerpt returns the last n lines plus any FATAL EXCEPTION /
// AndroidRuntime block: the part of the log a crash triage actually needs,
// not the full dump.
func CrashExcerpt(text string, n int) string {
	tail := Tail(text, n)
	var fatal []string
	for _, l := range splitLines(text) {
		if strings.Contains(l, "FATAL EXCEPTION") || strings.Contains(l, "AndroidRuntime") {
			fatal = append(fatal, l)
		}
	}
	if len(fatal) == 0 {
		return tail
	}
	return tail + "\n--- fatal ---\n" + strings.Join(fatal, "\n")
}

func splitLines(text string) []string {
	var out []string
	for _, l := range strings.Split(text, "\n") {
		if strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	return out
}

func firstLines(text string, n int) string {
	lines := splitLines(text)
	if len(lines) <= n {
		return text
	}
	return strings.Join(lines[:n], "\n")
}

// CrashEntry is one run-scoped crash record: which step failed, the log
// excerpt that explains it, and the screenshot taken at the time (when the
// step captures one). The index lives on the orchestrator session — it dies
// with the run — and wallet-tx steps file under their tx name so the vision
// screenshot and the logcat delta for one transaction sit in one entry.
type CrashEntry struct {
	Tx         string
	Tool       string
	Excerpt    string
	Screenshot string
	Round      int
}
