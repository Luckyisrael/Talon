// Package guardrails holds the deterministic checks that run before a tool call
// executes. The always-on rules tell the model what not to do; these make the
// security-critical ones true whether or not the model complied.
//
// A guardrail is a pure decision over (tool name, raw input, session state) —
// no I/O, no model call — so it is cheap, identical on every provider, and
// unit-testable in isolation. The orchestrator calls Check at the single point
// every tool call flows through and refuses the call on a rejected Decision.
package guardrails

import (
	"fmt"
	"encoding/json"
	"strings"
)

// Decision is the outcome of checking one tool call. A rejected Decision carries
// a stable Rule slug (for telemetry and the UI) and a Message written for the
// model: it says what was refused and what to do instead, because a guardrail
// that only says "no" just burns rounds.
type Decision struct {
	Allowed bool
	Rule    string
	Message string
}

var allow = Decision{Allowed: true}

func reject(rule, msg string) Decision { return Decision{Allowed: false, Rule: rule, Message: msg} }

// State is what a guardrail needs to know about the session so far. The
// orchestrator supplies it; keeping it an interface leaves this package a leaf
// with no dependency on the session or filesystem types.
type State interface {
	// HasReadFile reports whether the model has read this path in the current
	// turn. Read tracking resets each turn, so this is "seen recently" — exactly
	// the window in which a blind edit is dangerous.
	HasReadFile(path string) bool
	// FileExists reports whether the path already exists in the workspace.
	FileExists(path string) bool
}

// Versioned is the optional H5 extension of State: version-stamped
// observation. An implementing State reports the file's modification stamp as
// the model last saw it (read or write) and the workspace reports its current
// stamp; a write whose current stamp moved past the last-seen one is a
// conflict — someone else (the user, another agent) edited between the
// model's read and its write, and overwriting would silently clobber them.
// Zero from either side means unknown: the check degrades to today's
// read-before-write rule rather than refusing on missing information.
type Versioned interface {
	State
	// LastSeenVersion returns the file's mtime as the model last saw it, or 0
	// when unknown.
	LastSeenVersion(path string) int64
	// CurrentVersion returns the file's on-disk mtime now, or 0 when it does
	// not exist.
	CurrentVersion(path string) int64
}

// versionConflict checks the H5 stamp on one path. ok=false means no conflict
// (or no information): proceed.
func versionConflict(st State, path string) (conflict bool, lastSeen, current int64) {
	v, ok := st.(Versioned)
	if !ok || path == "" {
		return false, 0, 0
	}
	lastSeen = v.LastSeenVersion(path)
	current = v.CurrentVersion(path)
	// Unknown on either side is not a conflict — the check fails open to the
	// existing read-before-write rule, never to a blind refusal.
	if lastSeen == 0 || current == 0 {
		return false, lastSeen, current
	}
	return current != lastSeen, lastSeen, current
}

// Check evaluates one tool call and returns the first guardrail it violates, or
// an allowed Decision. preinstalled is the set of package names that ship with
// every project (lowercased); a nil map disables the reinstall check. A nil
// State disables the read-before-write checks (the content checks still run).
func Check(toolName string, rawInput json.RawMessage, st State, preinstalled map[string]bool) Decision {
	switch toolName {
	case "install_package":
		return checkInstall(rawInput, preinstalled)
	case "write_file":
		return checkWrite(rawInput, st)
	case "edit_file":
		return checkEdit(rawInput, st)
	case "multi_edit":
		return checkMultiEdit(rawInput, st)
	}
	return allow
}

// ── install_package ───────────────────────────────────

func checkInstall(rawInput json.RawMessage, preinstalled map[string]bool) Decision {
	var a struct {
		Name string `json:"name"`
	}
	if json.Unmarshal(rawInput, &a) != nil {
		return allow // malformed args fail in the tool itself; nothing to guard
	}
	pkg := basePackageName(a.Name)
	if pkg == "" {
		return allow
	}
	if d := browserWalletPackage(pkg); !d.Allowed {
		return d
	}
	if preinstalled != nil && preinstalled[pkg] {
		return reject("already-preinstalled",
			"`"+pkg+"` already ships with every project — it is preinstalled. Import it directly; "+
				"do not install it again. (Installing it churns dependencies and can force a native rebuild.)")
	}
	return allow
}

// browserWalletPackage refuses the browser-wallet packages that cannot run on
// the device. The discriminator is the scope: the mobile stack lives under
// @solana-mobile/, which none of these prefixes match, so the correct MWA
// packages (@solana-mobile/mobile-wallet-adapter-protocol*) are never caught.
func browserWalletPackage(pkg string) Decision {
	switch {
	case strings.HasPrefix(pkg, "@solana/wallet-adapter"),
		strings.HasPrefix(pkg, "@solana/wallet-standard"),
		pkg == "wallet-standard",
		strings.HasPrefix(pkg, "@wallet-standard/"),
		strings.HasPrefix(pkg, "@solflare-wallet/"),
		strings.HasPrefix(pkg, "@phantom/"):
		return reject("browser-wallet-package",
			"`"+pkg+"` is a browser wallet package and cannot run on the Seeker device. Wallet work goes "+
				"through Mobile Wallet Adapter: `transact()` from "+
				"`@solana-mobile/mobile-wallet-adapter-protocol-web3js` (preinstalled). See the wallet-mwa skill.")
	}
	return allow
}

// basePackageName strips any version suffix and lowercases, handling scoped
// names: "@Scope/Pkg@^1.2.3" -> "@scope/pkg", "Lodash@4" -> "lodash". A scoped
// name starts with '@' and its version-separating '@' is the second one; an
// unscoped name's separator is the first '@'.
func basePackageName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	if len(name) < 2 {
		return name
	}
	if at := strings.Index(name[1:], "@"); at >= 0 {
		name = name[:at+1]
	}
	return name
}

// ── write_file / edit_file / multi_edit ───────────────

func checkWrite(rawInput json.RawMessage, st State) Decision {
	var a struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if json.Unmarshal(rawInput, &a) != nil {
		return allow
	}
	if d := scanContent(a.Content); !d.Allowed {
		return d
	}
	// Replacing a file the model has not read this turn clobbers whatever it did
	// not know was there. Creating a new file is fine.
	if st != nil && a.Path != "" && st.FileExists(a.Path) && !st.HasReadFile(a.Path) {
		return reject("read-before-write", readBeforeMsg(a.Path, "overwriting"))
	}
	// H5 version stamp: the file changed since the model last saw it — the
	// user (or someone) edited between its read and this write. Overwriting
	// would silently clobber their edit; refuse and name the conflict.
	if conflict, _, current := versionConflict(st, a.Path); conflict {
		return reject("write-conflict", fmt.Sprintf(
			"%s changed on disk since you last read it (modified at %d — not by you). "+
				"Re-read it, incorporate the changes you find, then write again. The other edit is preserved on disk.",
			a.Path, current))
	}
	return allow
}

func checkEdit(rawInput json.RawMessage, st State) Decision {
	var a struct {
		Path      string `json:"path"`
		NewString string `json:"new_string"`
	}
	if json.Unmarshal(rawInput, &a) != nil {
		return allow
	}
	if d := scanContent(a.NewString); !d.Allowed {
		return d
	}
	return readBeforeEdit(a.Path, st)
}

func checkMultiEdit(rawInput json.RawMessage, st State) Decision {
	var a struct {
		Path  string `json:"path"`
		Edits []struct {
			NewString string `json:"new_string"`
		} `json:"edits"`
	}
	if json.Unmarshal(rawInput, &a) != nil {
		return allow
	}
	var b strings.Builder
	for _, e := range a.Edits {
		b.WriteString(e.NewString)
		b.WriteByte('\n')
	}
	if d := scanContent(b.String()); !d.Allowed {
		return d
	}
	return readBeforeEdit(a.Path, st)
}

// An edit always targets an existing file; requiring a read this turn stops the
// model editing an assumed version of it.
func readBeforeEdit(path string, st State) Decision {
	if st != nil && path != "" && !st.HasReadFile(path) {
		return reject("read-before-write", readBeforeMsg(path, "editing"))
	}
	return allow
}

func readBeforeMsg(path, verb string) string {
	return "Read " + path + " with read_file before " + verb + " it, so you change its current contents " +
		"and not an assumed version. Call read_file first, then retry."
}

// scanContent rejects introduced source that would put forbidden things in the
// project: an import of a browser wallet package, or private-key / seed-phrase
// handling. These are matched against precise API and module shapes so they do
// not fire on ordinary code. The packages named here must never appear in this
// project at all, so a plain substring match is the honest check.
func scanContent(content string) Decision {
	if content == "" {
		return allow
	}
	lower := strings.ToLower(content)

	for _, mod := range []string{
		"@solana/wallet-adapter", "@solana/wallet-standard", "@wallet-standard/",
		"@solflare-wallet/", "@phantom/", "'wallet-standard'", "\"wallet-standard\"",
	} {
		if strings.Contains(lower, mod) {
			return reject("browser-wallet-import",
				"This introduces a browser wallet package (`"+mod+"`) that cannot run on the device. Use "+
					"Mobile Wallet Adapter: `transact()` from "+
					"`@solana-mobile/mobile-wallet-adapter-protocol-web3js`. See the wallet-mwa skill.")
		}
	}

	// Key material never belongs in app code: on Seeker the app never holds a
	// key — signing happens inside the wallet through MWA.
	if strings.Contains(content, "fromSecretKey") {
		return reject("secret-key-in-code",
			"`fromSecretKey` puts a private key in the app. On Seeker the app never holds key material — "+
				"signing happens in the wallet through Mobile Wallet Adapter. Sign via `transact()` instead.")
	}
	for _, sig := range []string{"'bip39'", "\"bip39\"", "mnemonictoseed", "generatemnemonic"} {
		if strings.Contains(lower, sig) {
			return reject("seed-phrase-in-code",
				"Seed-phrase handling (bip39 / mnemonics) does not belong in the app — the wallet owns the "+
					"seed. Connect with Mobile Wallet Adapter instead of deriving keys in the app.")
		}
	}
	return allow
}
