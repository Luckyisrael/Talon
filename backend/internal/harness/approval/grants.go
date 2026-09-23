package approval

// grants.go — W9.3: standing grants, the per-project command allowlist.
//
// "Always allow npm run lint" becomes a recorded standing grant instead of
// a repetitive ask: when TALONSTUDIO_STANDING_GRANTS is truthy, an
// AllowedOnce for a model-initiated scope (tier-2 shell / sandbox
// escalation) is persisted into the workspace's .talon/approvals.json, and
// the next identical request replays it without prompting.
//
// Trust shape (the plan's risk note, honored literally):
//   - Entries are EXACT scope+action matches — never prefixes, never
//     globs. Repetitive asks die; substring lookalikes still prompt.
//   - Each entry carries sha256(action), hash-pinned like plugins: an
//     entry whose action was edited without rehashing fails closed.
//   - The file lives in the workspace: user-editable, diffable, reviewable
//     in the same commit as the code it trusts.
//   - The flag gates everything: off (the default) means the file is
//     never read and never written — today's one-shot contract, intact.
//   - The hard-deny class consults no grant: auto mode and allowlist
//     entries both fail against it (see Seam.HardDeny).

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// StandingGrantsEnabled reports whether TALONSTUDIO_STANDING_GRANTS is in
// the truthy set (1|true|on|v1 — trimmed, case-insensitive, the repo-wide
// convention). Unset/off: grants are never looked up or recorded.
func StandingGrantsEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("TALONSTUDIO_STANDING_GRANTS"))) {
	case "1", "true", "on", "v1":
		return true
	}
	return false
}

// grantEntry is one standing grant in .talon/approvals.json.
type grantEntry struct {
	Scope     string    `json:"scope"`
	Action    string    `json:"action"`
	SHA256    string    `json:"sha256"`
	GrantedAt time.Time `json:"granted_at"`
}

// grantFile is the whole allowlist document.
type grantFile struct {
	Grants []grantEntry `json:"grants"`
}

// grantHash is the pin over an entry's exact action text.
func grantHash(action string) string {
	sum := sha256.Sum256([]byte(action))
	return hex.EncodeToString(sum[:])
}

func grantsPath(workspacePath string) string {
	return filepath.Join(workspacePath, ".talon", "approvals.json")
}

// lookupGrant reports whether workspacePath carries a standing grant for
// exactly this scope+action with a valid hash. Flag off, empty inputs, an
// unreadable file, or a tampered entry are all "no grant" — the caller
// falls through to asking a human.
func lookupGrant(workspacePath, scope, action string) (ok bool, hash string) {
	if !StandingGrantsEnabled() {
		return false, ""
	}
	if strings.TrimSpace(workspacePath) == "" || scope == "" || action == "" {
		return false, ""
	}
	raw, err := os.ReadFile(grantsPath(workspacePath))
	if err != nil {
		return false, ""
	}
	var f grantFile
	if err := json.Unmarshal(raw, &f); err != nil {
		return false, ""
	}
	want := grantHash(action)
	for _, g := range f.Grants {
		if g.Scope == scope && g.Action == action && g.SHA256 == want {
			return true, g.SHA256
		}
		// Exact-match discipline: a same-action entry with a stale hash is
		// a tampered or corrupted record — never a prefix fallback, never
		// a "close enough" grant.
	}
	return false, ""
}

// recordGrant persists one AllowedOnce as a standing grant. Called only
// when the flag is on and only for model-initiated scopes; a failure is
// swallowed (the grant still stands for this turn — the file is an
// optimization over future prompts, never the record of this one;
// turn_approvals remains the binding record of the live decision).
func recordGrant(workspacePath, scope, action string) {
	if !StandingGrantsEnabled() {
		return
	}
	if strings.TrimSpace(workspacePath) == "" || scope == "" || action == "" {
		return
	}
	if ok, _ := lookupGrant(workspacePath, scope, action); ok {
		return // already standing — replay covers it
	}
	raw, err := os.ReadFile(grantsPath(workspacePath))
	if err != nil && !os.IsNotExist(err) {
		return
	}
	var f grantFile
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &f); err != nil {
			// A malformed allowlist must not be silently replaced: that
			// would drop entries the operator wrote. Refuse to record;
			// the next prompt says why by simply asking.
			return
		}
	}
	f.Grants = append(f.Grants, grantEntry{
		Scope:     scope,
		Action:    action,
		SHA256:    grantHash(action),
		GrantedAt: time.Now().UTC(),
	})
	out, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(grantsPath(workspacePath)), 0o755); err != nil {
		return
	}
	_ = os.WriteFile(grantsPath(workspacePath), append(out, '\n'), 0o644)
}
