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
	"encoding/json"
	"fmt"
	"regexp"
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
	case "read_file", "grep_codebase", "search_files", "find_files":
		return checkNodeModulesSurvey(rawInput)
	case "run_shell", "run_command":
		// FM-L10b: the survey moves to shell the moment the file tools
		// refuse — Run-3 proved it. Same rule, same hint, shell shape.
		return checkNodeModulesShellSurvey(rawInput)
	}
	return allow
}

// ── L10: node_modules survey guardrail ───────────────────────────────

// checkNodeModulesSurvey refuses survey-style access to dependency
// internals (L10, contract tier): deep reads, directory targets and
// path/glob arguments pointing into node_modules/ — Run 2 spent ~16
// minutes on .d.ts archaeology that inspect_package answers in one call.
//
// FM-L10a's scope, by exact path segment: a package's own package.json
// (a config) and files directly at the node_modules root stay readable,
// and nothing outside a node_modules segment is ever touched — project
// root configs and files whose name merely contains "node_modules"
// pass. grep_codebase's walk already excludes the tree, so a grep that
// names it is searching for nothing: refusing it with the answer beats
// silently returning wrong results.
func checkNodeModulesSurvey(rawInput json.RawMessage) Decision {
	var a struct {
		Path string `json:"path"`
		Glob string `json:"glob"`
	}
	if json.Unmarshal(rawInput, &a) != nil {
		return allow // malformed args fail in the tool itself; nothing to guard
	}
	for _, target := range []string{a.Path, a.Glob} {
		if nodeModulesRefusal(target) {
			return reject("node-modules-survey", nodeModulesSurveyMessage)
		}
	}
	return allow
}

// nodeModulesRefusal reports whether one path/glob target is a survey
// target under FM-L10a's exemptions: a real file directly at the
// node_modules root ("root files") and a package's own config
// ("configs") pass. A single component that names a package directory
// or a glob — node_modules/expo-notifications, node_modules/** — is a
// survey target, not a file, and must not slip through. Shared by the
// file tools and the shell check (FM-L10b) so the two shapes cannot
// drift on what counts as a survey.
func nodeModulesRefusal(target string) bool {
	rest, inside := nodeModulesRest(target)
	if !inside {
		return false
	}
	if rest != "" && !strings.Contains(rest, "/") && isPlainFilePath(rest) {
		return false
	}
	if base := rest[strings.LastIndex(rest, "/")+1:]; base == "package.json" {
		return false
	}
	return true
}

const nodeModulesSurveyMessage = "Refused: dependency internals under node_modules/ are not directly readable — " +
	"dependency source is not your source. Call inspect_package with the package name: it returns " +
	"the version, the entry point and the full type surface. If its output is truncated, work from " +
	"the signatures it returned and verify your code with run_typecheck. Project files and " +
	"package.json configs remain readable normally."

// surveyVerbRe matches the read verbs that make a shell command
// archaeology (FM-L10b): Run-2/3's actual bypasses were grep/find/
// sed/cat/ls over package trees. A command that names none of them is
// not reading dependency internals, whatever else it does (FM-L10c).
var surveyVerbRe = regexp.MustCompile(`\b(grep|egrep|fgrep|rg|find|fd|cat|sed|head|tail|less|more|nl|awk|wc|ls|tree|stat|du|strings|cut|diff|sort|xxd)\b`)

// pkgPrefixRe is FM-L10c's hard rule: package-manager and toolchain
// commands pass untouched, even when their pipeline mentions
// node_modules — npm/gradle running inside the tree is the build doing
// its job, not a survey. Deliberately NOT containing a shell (bash/sh/
// sh -c is the trivial bypass).
var pkgPrefixRe = regexp.MustCompile(`^\s*(npm|npx|yarn|pnpm|pnpx|bun|node|deno|expo|gradle|\./gradlew|adb)\b`)

// shellNodeModulesRe extracts node_modules tokens from a command, with
// an exact segment boundary: "somenode_modules/x" is a project path
// whose name merely contains the string, same scope as the file tools.
// The token ends at the first shell delimiter (whitespace, quote, pipe,
// redirect, substitution); whatever glob or path text remains is judged
// by the same exemption rules as the file tools.
var shellNodeModulesRe = regexp.MustCompile(`(^|[^A-Za-z0-9_.-])(node_modules/[^ \t"'` + "`" + `$;|&<>(){}]+)`)

// checkNodeModulesShellSurvey is FM-L10b: Run-3's model was refused
// twice on the file tools and routed the identical survey through
// run_shell — find/grep over .d.ts, one composing straight into the
// turn's only 183 s gap. The rule keys on the shape of the READ, not
// the tool carrying it; the hint is the same one that ends the survey
// in one call.
func checkNodeModulesShellSurvey(rawInput json.RawMessage) Decision {
	var a struct {
		Command string `json:"command"`
	}
	if json.Unmarshal(rawInput, &a) != nil {
		return allow // malformed args fail in the tool itself; nothing to guard
	}
	cmd := a.Command
	if strings.TrimSpace(cmd) == "" {
		return allow
	}
	// FM-L10c first: build and toolchain commands never refuse.
	if pkgPrefixRe.MatchString(cmd) {
		return allow
	}
	// Only a read verb can be archaeology.
	if !surveyVerbRe.MatchString(cmd) {
		return allow
	}
	for _, m := range shellNodeModulesRe.FindAllStringSubmatch(cmd, -1) {
		if nodeModulesRefusal(m[2]) {
			return reject("node-modules-survey", nodeModulesSurveyMessage)
		}
	}
	return allow
}

// nodeModulesRest reports whether p points inside a node_modules directory,
// returning what lies below the directory itself ("" = the directory or its
// direct root, "pkg/file.d.ts" = deeper). Segment-exact: "node_modules" must
// be a whole path element, so node_modules.config.ts and my_node_modules/x
// never match.
func nodeModulesRest(p string) (rest string, inside bool) {
	p = strings.ReplaceAll(strings.TrimSpace(p), "\\", "/")
	switch {
	case p == "node_modules":
		return "", true
	case strings.HasPrefix(p, "node_modules/"):
		return p[len("node_modules/"):], true
	case strings.HasSuffix(p, "/node_modules"):
		return "", true
	case strings.Contains(p, "/node_modules/"):
		return p[strings.Index(p, "/node_modules/")+len("/node_modules/"):], true
	}
	return "", false
}

// isPlainFilePath reports whether a single component directly under
// node_modules/ names a file rather than a package directory or a glob: it
// has an extension and carries no glob metacharacter. node_modules/
// .package-lock.json is a root file (FM-L10a exempt); node_modules/
// expo-notifications is a package tree and node_modules/** is a glob — both
// survey targets.
func isPlainFilePath(component string) bool {
	if strings.ContainsAny(component, "*?[") {
		return false
	}
	dot := strings.LastIndex(component, ".")
	return dot > 0 && dot < len(component)-1
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

	// Key material never belongs in app code: on Talon the app never holds a
	// key — signing happens inside the wallet through MWA.
	if strings.Contains(content, "fromSecretKey") {
		return reject("secret-key-in-code",
			"`fromSecretKey` puts a private key in the app. On Talon the app never holds key material — "+
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
