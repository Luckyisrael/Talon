// Package sandbox is the H4 confinement capability: one place that decides
// whether an operation may touch the filesystem, under which mode, and what
// to do when the mode cannot be enforced.
//
// It converges three mechanisms that grew separately — run_shell's tier
// classification, ValidateSandboxPath for the fs tools, and the ask_user
// escalation convention — onto one contract so confinement is uniform instead
// of per-tool bespoke:
//
//	ModeReadOnly   — no writes anywhere; reads inside the workspace.
//	ModeWorkspace  — writes inside the project dir; reads there too; the
//	                 platform's temp areas are writable for scratch.
//	ModeDangerFull — no confinement; for trusted compositions only.
//
// The law: if a mode cannot be enforced, the operation fails closed with a
// structured SANDBOX_UNAVAILABLE verdict — never unconfined. Every check
// returns a Decision, never a bare bool, so refusals carry their reason to
// the transcript instead of dying in a generic error string.
package sandbox

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// Mode is the confinement level of a composition.
type Mode int

const (
	// ModeReadOnly refuses every write and every read outside the project.
	ModeReadOnly Mode = iota
	// ModeWorkspace allows writes inside the project directory (and reads
	// there); the platform temp dir stays writable for scratch output.
	ModeWorkspace
	// ModeDangerFull disables confinement. Only trusted compositions may
	// select it, and the decision is audited at the approval seam.
	ModeDangerFull
)

func (m Mode) String() string {
	switch m {
	case ModeReadOnly:
		return "read-only"
	case ModeWorkspace:
		return "workspace-write"
	case ModeDangerFull:
		return "danger-full-access"
	}
	return "unknown"
}

// Capability is one sandbox decision-maker bound to a workspace root and a
// mode. All methods are safe for concurrent use.
type Capability struct {
	mode Mode
	root string
	// tmpRoots are scratch areas the platform guarantees writable: spill
	// files, gradle caches, tool temp output.
	tmpRoots []string
	// enforce, when nil, means enforcement cannot be verified — the
	// fail-closed knob. A nil enforce makes every mutating check return
	// SANDBOX_UNAVAILABLE rather than allow.
	enforce func(op Operation) error
}

// Operation is one attempted filesystem touch: the verb, the target path, and
// the raw command when the operation is a shell invocation.
type Operation struct {
	Verb string // "read", "write", "delete", "shell"
	Path string // resolved absolute path, "" for shell
	// Command is the raw shell command when Verb == "shell".
	Command string
}

// ErrUnavailable is the structured fail-closed verdict: the mode is set but
// enforcement is missing. It must never be wrapped into a generic error —
// the transcript must show it as SANDBOX_UNAVAILABLE.
var ErrUnavailable = errors.New("SANDBOX_UNAVAILABLE: confinement cannot be enforced; the operation is refused rather than run unconfined")

// Decision is one sandbox verdict with its reason.
type Decision struct {
	Allowed bool
	Reason  string
}

// New builds a Capability. root must be absolute. A nil enforce means the
// composition could not provide an enforcement hook: the capability still
// answers path questions (read checks) but every write refuses closed.
func New(mode Mode, root string, tmpRoots []string, enforce func(op Operation) error) *Capability {
	return &Capability{mode: mode, root: root, tmpRoots: tmpRoots, enforce: enforce}
}

// Mode returns the configured confinement level.
func (c *Capability) Mode() Mode { return c.mode }

// AllowsPath reports whether verb ("read"/"write") on the resolved path is
// permitted. read-only mode refuses writes entirely; workspace mode allows
// writes inside the root (plus tmp roots); danger allows all.
func (c *Capability) AllowsPath(verb, path string) Decision {
	if c.mode == ModeDangerFull {
		return Decision{Allowed: true}
	}
	abs := path
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(c.root, abs)
	}
	inside := c.insideRoot(abs)
	switch verb {
	case "read":
		if inside {
			return Decision{Allowed: true}
		}
		// Reads outside the root: allowed only in danger mode (already
		// returned). Refused with the boundary named.
		return Decision{Allowed: false, Reason: "read outside the project workspace is refused (sandbox: " + c.mode.String() + ")"}
	case "write", "delete":
		if c.mode == ModeReadOnly {
			return Decision{Allowed: false, Reason: "write refused: the session is read-only"}
		}
		if inside || c.insideTmp(abs) {
			if c.enforce == nil {
				// Fail closed: without an enforcement hook the capability
				// cannot guarantee the write stays confined.
				return Decision{false, ErrUnavailable.Error()}
			}
			return Decision{true, ""}
		}
		return Decision{false, "write outside the project workspace and temp areas is refused (sandbox: " + c.mode.String() + ")"}
	}
	return Decision{false, "unknown verb " + verb}
}

// insideRoot reports whether abs is inside the workspace root.
func (c *Capability) insideRoot(abs string) bool {
	if abs == c.root {
		return true
	}
	return strings.HasPrefix(abs, c.root+string(filepath.Separator))
}

// insideTmp reports whether abs is inside one of the scratch roots.
func (c *Capability) insideTmp(abs string) bool {
	for _, r := range c.tmpRoots {
		if abs == r || strings.HasPrefix(abs, r+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// ResolveWorkspace converts a caller-supplied relative path against the root
// into the absolute path the fs tools operate on. It rejects parent escapes
// before they can name anything outside the project.
func (c *Capability) ResolveWorkspace(rel string) (string, error) {
	if filepath.IsAbs(rel) {
		return "", errors.New("absolute paths are not workspace paths: " + rel)
	}
	abs := filepath.Join(c.root, rel)
	if !c.insideRoot(abs) {
		return "", errors.New("path escapes the workspace: " + rel)
	}
	return abs, nil
}

// Stat is a read the capability itself performs: existence + mtime, going
// through the confinement verdict first so even stat cannot probe outside.
func (c *Capability) Stat(path string) (os.FileInfo, error) {
	if d := c.AllowsPath("read", path); !d.Allowed {
		return nil, errors.New(d.Reason)
	}
	return os.Stat(path)
}
