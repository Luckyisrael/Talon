// Package planfile is the W3.4 plan artifact: the settled plan written to the
// workspace as .talon/plan.md — durable, user-editable, checkpointed — the
// DSH/Claude-Code "plan is a file" model.
//
// The file is a deterministic render of (plan, checklist rows, statuses) with a
// content-hash marker on line 1. Direction is decided by that marker:
//
//   - body hash == marker hash → the bytes are the system's last write, safe to
//     overwrite freely (structure refresh at seed, marker sync after a flip);
//   - a mismatch — or a missing marker, which a hand-written file has — means
//     the user edited it. Their checkbox states import back into the
//     plan_task_status backlog at the next reconciliation boundary (build
//     start, and each status flip except the row that just flipped), and their
//     text is preserved: status writes go through Sync, which rewrites only the
//     marker of checklist lines whose description names a current row.
//
// The artifact joins on the task DESCRIPTION, not the ID: the file is the human
// surface and IDs are internal, so a line the user reworded imports nothing
// (stale-but-honest) rather than matching the wrong row. The same derivation
// (orchestrator.PlanTaskRows) seeds the backlog and renders the file, so the
// file, the store, and the prompt checklist cannot drift.
//
// Everything here is gated on Enabled — the W3 phase flag (the same
// TALONSTUDIO_PLAN_TOOLS switch api.planToolsEnabled reads), shipped dark:
// off means no file is read, written, or imported anywhere.
package planfile

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/luckyisrael/talon/backend/internal/ai"
)

// RelPath is the artifact's location inside the workspace: the .talon config
// directory already holds skills and plugin pins, so the plan lives beside them.
const RelPath = ".talon/plan.md"

// Enabled reads the W3 phase flag. Unset/off means zero behavior change: no
// artifact is read, written, or imported.
func Enabled() bool {
	// Exactly api.planToolsEnabled's truthy set — the same env var is the
	// whole W3 switch, and two definitions that disagree would mean an
	// artifact written under one reading and never read under the other.
	switch strings.ToLower(strings.TrimSpace(os.Getenv("TALONSTUDIO_PLAN_TOOLS"))) {
	case "1", "true", "on", "v1":
		return true
	}
	return false
}

// Row is one checklist line: the backlog ID the store and the mark_task_done
// matcher key on, and the description the file, the prompt checklist, and the
// import all display and join on.
type Row struct {
	ID          string
	Description string
	// Files is the shaped feature's declared workspace files (L15): the
	// seed copies them onto the task so the marking heuristic and the
	// audit can hold the feature to the screens' bar. Rows parsed back
	// from the artifact carry none — the file direction is plan → task.
	Files []string
	// Acceptance is the shaped feature's one-line bar (L16): seed copies
	// it onto the task for the checklist's "done when: …". The artifact
	// does not render it — plan.md stays a checkbox file (see the
	// package doc), and the round-trip test pins that format.
	Acceptance string
}

var (
	// markerRe matches the line-1 provenance marker and captures its hash. The
	// body is everything after the marker line; the hash covers exactly that.
	markerRe = regexp.MustCompile(`\A<!-- talon-plan:v1 sha256:([0-9a-f]{64}) -->\r?\n?`)
	// checkRe matches a talon checkbox line: one of the four status markers,
	// then the description. Any other marker char is the user's own list item
	// and is never imported or rewritten.
	checkRe = regexp.MustCompile(`\A- \[([ xX~!])\] (.+)$`)
)

// hashBody is the marker's content hash — the edit-detection unit.
func hashBody(body string) string {
	sum := sha256.Sum256([]byte(body))
	return hex.EncodeToString(sum[:])
}

// markerFor maps a task status to its checkbox char (default: pending).
func markerFor(status string) byte {
	switch status {
	case "completed":
		return 'x'
	case "in_progress":
		return '~'
	case "failed":
		return '!'
	}
	return ' '
}

// statusFor maps a checkbox char back to a task status; ok=false for chars
// outside the talon vocabulary.
func statusFor(m byte) (string, bool) {
	switch m {
	case ' ':
		return "pending", true
	case 'x', 'X':
		return "completed", true
	case '~':
		return "in_progress", true
	case '!':
		return "failed", true
	}
	return "", false
}

// seal wraps a body in a fresh marker — the one place marker lines are built.
func seal(body string) string {
	return fmt.Sprintf("<!-- talon-plan:v1 sha256:%s -->\n%s", hashBody(body), body)
}

// stripMarker returns the content without its leading marker line (any content,
// edited or not — callers decide what that means).
func stripMarker(content string) string {
	if m := markerRe.FindString(content); m != "" {
		return content[len(m):]
	}
	return content
}

// Render builds the full document: a fresh marker over a deterministic body.
// Same inputs produce byte-identical output, which is what makes "plan file
// written and re-read stable" testable.
func Render(plan *ai.AppPlan, rows []Row, statuses map[string]string) string {
	var b strings.Builder
	b.WriteString("# Plan\n\n## Checklist\n")
	for _, r := range rows {
		fmt.Fprintf(&b, "- [%c] %s\n", markerFor(statuses[r.Description]), r.Description)
	}
	if plan == nil {
		return seal(b.String())
	}
	if nav := strings.TrimSpace(plan.Navigation); nav != "" {
		fmt.Fprintf(&b, "\n## Navigation\n%s\n", nav)
	}
	if len(plan.Packages) > 0 {
		b.WriteString("\n## Packages\n")
		for _, p := range plan.Packages {
			fmt.Fprintf(&b, "- %s\n", p)
		}
	}
	if st := plan.Structure; st != nil {
		writeList := func(label string, items []string) {
			if len(items) > 0 {
				fmt.Fprintf(&b, "\n## Structure\n- %s: %s\n", label, strings.Join(items, ", "))
			}
		}
		// Structure renders as one section: emit its heading once.
		if len(st.Keep)+len(st.Prune)+len(st.Write) > 0 || strings.TrimSpace(st.Folders) != "" {
			b.WriteString("\n## Structure\n")
			if len(st.Keep) > 0 {
				fmt.Fprintf(&b, "- keep: %s\n", strings.Join(st.Keep, ", "))
			}
			if len(st.Prune) > 0 {
				fmt.Fprintf(&b, "- prune: %s\n", strings.Join(st.Prune, ", "))
			}
			if len(st.Write) > 0 {
				fmt.Fprintf(&b, "- write: %s\n", strings.Join(st.Write, ", "))
			}
			if folders := strings.TrimSpace(st.Folders); folders != "" {
				fmt.Fprintf(&b, "- folders: %s\n", folders)
			}
		}
		_ = writeList // (kept out of the render path; see comment above)
	}
	return seal(b.String())
}

// Read returns the artifact's bytes and whether the file existed. A missing
// file is ("", false, nil) — a normal state (never saved, or the flag was off);
// a real read error is reported so callers never clobber what they could not
// read.
func Read(workspacePath string) (string, bool, error) {
	content, err := os.ReadFile(filepath.Join(workspacePath, RelPath))
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, err
	}
	return string(content), true, nil
}

// Write stores content iff it differs from what is on disk, creating .talon/
// on the way. It reports whether bytes actually changed so callers skip the
// checkpoint churn of a no-op save.
func Write(workspacePath, content string) (bool, error) {
	prev, existed, err := Read(workspacePath)
	if err != nil {
		return false, err
	}
	if existed && prev == content {
		return false, nil
	}
	if err := os.MkdirAll(filepath.Join(workspacePath, filepath.Dir(RelPath)), 0o755); err != nil {
		return false, err
	}
	if err := os.WriteFile(filepath.Join(workspacePath, RelPath), []byte(content), 0o644); err != nil {
		return false, err
	}
	return true, nil
}

// Edited reports whether the file no longer matches its own marker: the user
// changed the body, or the marker is gone (a hand-written file — the user's by
// definition).
func Edited(content string) bool {
	m := markerRe.FindStringSubmatch(content)
	if m == nil {
		return true
	}
	return hashBody(content[len(m[0]):]) != m[1]
}

// ParseStatuses reads checkbox states out of the artifact, keyed by
// description — the file-side join key. Lines whose marker is outside the
// talon four are the user's own items and are skipped.
func ParseStatuses(content string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(stripMarker(content), "\n") {
		if m := checkRe.FindStringSubmatch(line); m != nil {
			if st, ok := statusFor(m[1][0]); ok {
				out[strings.TrimSuffix(m[2], "\r")] = st
			}
		}
	}
	return out
}

// Sync rewrites the marker of every checklist line whose description names a
// row in statuses to that row's current status, leaves every other byte —
// headings, prose, the user's own items, their reworded text — untouched, and
// re-seals the file with a fresh marker hash (adopting whatever text the user
// left behind into the canonical body). It is how a status flip reaches the
// file without flattening an edit the user made to it.
//
// statuses is keyed by description; rows absent from the map keep their line
// byte-identical.
func Sync(content string, statuses map[string]string) string {
	lines := strings.Split(stripMarker(content), "\n")
	for i, line := range lines {
		m := checkRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		cur, ok := statusFor(m[1][0])
		if !ok {
			continue // unreachable via checkRe; kept for clarity
		}
		next, ok := statuses[strings.TrimSuffix(m[2], "\r")]
		if !ok || next == cur {
			continue
		}
		// Rebuild only the marker char — the rest of the line (including a
		// trailing \r from a Windows editor) stays byte-identical.
		lines[i] = "- [" + string(markerFor(next)) + "] " + m[2]
	}
	return seal(strings.Join(lines, "\n"))
}
