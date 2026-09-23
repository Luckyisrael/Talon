package approval

// modes.go — W9: the permission modes, composed over the mechanisms that
// already exist (no fourth mechanism).
//
//	 read-only  — writes are refused with a structured error. Enforced by
//	              the sandbox capability at ModeReadOnly and by the
//	              human-initiated action handlers before they reach the seam;
//	              this package only names the mode.
//	 ask        — today's behavior, byte for byte: sensitive acts prompt the
//	              user through the seam (the default whenever nothing says
//	              otherwise).
//	 accept-edits — edit-class escalations (writes into the workspace)
//	              auto-allow; everything else still asks.
//	 auto       — sensitive acts allow without prompting, EXCEPT the
//	              hard-deny class (secrets, egress exfil, path escapes),
//	              which is rejected before any allowlist is consulted —
//	              no standing grant can waive it.
//
// Modes resolve per session (a request-body override) over per project
// (.talon/config.json "permission_mode"), defaulting to ask. An invalid or
// absent value is ask — fail toward the behavior that prompts a human.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// Mode is one session's/project's permission posture.
type Mode string

const (
	// ModeReadOnly refuses every write with a structured error.
	ModeReadOnly Mode = "read-only"
	// ModeAsk prompts for every sensitive act — the default, and today's
	// behavior exactly.
	ModeAsk Mode = "ask"
	// ModeAcceptEdits auto-allows edit-class (workspace-write) acts and
	// prompts for everything else.
	ModeAcceptEdits Mode = "accept-edits"
	// ModeAuto allows sensitive acts without prompting, minus the
	// hard-deny class which no allowlist can waive.
	ModeAuto Mode = "auto"
)

// String returns the mode's wire/config spelling.
func (m Mode) String() string { return string(m) }

// ParseMode validates a mode spelling (trimmed, case-insensitive). An
// empty string is the valid spelling of the default (ask). Unknown values
// are invalid — callers fall back to ask rather than guessing.
func ParseMode(s string) (Mode, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "ask":
		return ModeAsk, true
	case "read-only", "readonly", "read_only":
		return ModeReadOnly, true
	case "accept-edits", "accept_edits", "acceptedits":
		return ModeAcceptEdits, true
	case "auto":
		return ModeAuto, true
	}
	return ModeAsk, false
}

// ResolveMode composes the session override over the project setting:
// a valid, non-empty session spelling wins; otherwise the project's
// already-parsed posture (ProjectMode never returns an invalid value) —
// and everything else collapses to ask.
func ResolveMode(session string, project Mode) Mode {
	if m, ok := ParseMode(session); ok && strings.TrimSpace(session) != "" {
		return m
	}
	if project != "" {
		return project
	}
	return ModeAsk
}

// permissionConfigKeys is the W9 view of .talon/config.json — same file
// W6 reads for verify_policy, one key per concern (see verify_policy.go).
type permissionConfigKeys struct {
	PermissionMode string `json:"permission_mode"`
}

// ProjectMode reads workspacePath/.talon/config.json's "permission_mode".
// Any failure — no file, bad JSON, unknown spelling — is ModeAsk: the
// posture that prompts a human is the safe collapse of every surprise.
func ProjectMode(workspacePath string) Mode {
	if strings.TrimSpace(workspacePath) == "" {
		return ModeAsk
	}
	raw, err := os.ReadFile(filepath.Join(workspacePath, ".talon", "config.json"))
	if err != nil {
		return ModeAsk
	}
	var cfg permissionConfigKeys
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return ModeAsk
	}
	m, ok := ParseMode(cfg.PermissionMode)
	if !ok {
		return ModeAsk
	}
	return m
}

// editClassScopes are the acts accept-edits auto-allows: workspace-write
// sandbox escalations (the lattice's edit rung) and any future explicit
// file-edit scope.
const escalationScopePrefix = "sandbox-escalation:"

// IsEditClass reports whether a request is an edit (workspace-write act).
// Keyed on the seam's stable scope strings, never on free text.
func IsEditClass(req Request) bool {
	if req.Scope == "file-edit" {
		return true
	}
	return req.Scope == escalationScopePrefix+"workspace-write"
}

// humanScopes are the acts a person initiated by clicking a button
// (push, export, device build). A permission mode never auto-allows them
// — the human is by definition standing there; read-only refuses them
// with a structured error, ask keeps today's confirm-through-the-seam.
var humanScopes = map[string]bool{
	"git-push":       true,
	"release-export": true,
	"device-build":   true,
}

// IsHumanInitiated reports whether the scope is a person's button press
// rather than a model-initiated act.
func IsHumanInitiated(scope string) bool { return humanScopes[scope] }
