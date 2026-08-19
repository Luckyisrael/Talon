// Package evals measures whether SeekerStudio produces good apps regardless of the
// model driving the build — the whole point of the knowledge, guardrail, and
// verification layers. It runs a fixed suite of build prompts and grades each
// result with deterministic checks, so the same build always earns the same score
// and two models can be compared on the same yardstick.
//
// The grader is a pure function of what a build produced (its files and outcome),
// never of a live model or compiler, which is what makes a score reproducible. The
// package deliberately does not import the orchestrator: the caller wires a real
// build — or a fake, in tests — through BuildFunc and hands back what it produced.
package evals

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Sample is everything the grader needs about one build to score it: the source
// files it produced, keyed by workspace-relative path, and how the build itself
// went. Grading over data rather than a live workspace is what keeps it
// deterministic.
type Sample struct {
	Files    map[string]string
	Verified bool   // did the verification ladder pass?
	Rounds   int    // how many agent rounds it took
	Err      string // non-empty if the build never finished
}

// Expectations are the eval-specific things a particular task should produce, layered
// on top of the universal checks every build must pass.
type Expectations struct {
	// UsesMWA requires at least one file to import Mobile Wallet Adapter — the only
	// sanctioned wallet path on this device.
	UsesMWA bool
	// ImportsAnyOf requires at least one produced file to import one of these.
	ImportsAnyOf []string
	// CreatesFileMatching requires at least one produced path to contain one of
	// these fragments (e.g. "app/" for a new screen).
	CreatesFileMatching []string
}

// Eval is one fixed build task: a stable prompt and what a correct result looks
// like. The prompts never change, so a regression shows up as a score that drops.
type Eval struct {
	ID     string
	Prompt string
	Expect Expectations
}

// Result is the outcome of one check against one build.
type Result struct {
	Name     string
	Passed   bool
	Critical bool   // a failed critical check means the build is unacceptable, not just weaker
	Detail   string // why it failed, or what was found
}

// Scorecard is every check's result for one (model, eval) pair.
type Scorecard struct {
	Model   string
	EvalID  string
	Results []Result
}

// Score is the fraction of checks that passed, 0..1 — a simple, transparent
// aggregate. Passed() is the gate; Score() is the gradient.
func (c Scorecard) Score() float64 {
	if len(c.Results) == 0 {
		return 0
	}
	passed := 0
	for _, r := range c.Results {
		if r.Passed {
			passed++
		}
	}
	return float64(passed) / float64(len(c.Results))
}

// Passed reports whether the build is acceptable: every critical check passed. A
// build can lose correctness points and still be acceptable, but a browser-wallet
// import or a leaked key is disqualifying however good the rest is.
func (c Scorecard) Passed() bool {
	for _, r := range c.Results {
		if r.Critical && !r.Passed {
			return false
		}
	}
	return true
}

// Failures returns the checks that did not pass, for reporting.
func (c Scorecard) Failures() []Result {
	var out []Result
	for _, r := range c.Results {
		if !r.Passed {
			out = append(out, r)
		}
	}
	return out
}

// Markers an independent judge scans for. These duplicate the guardrail denylist on
// purpose: an eval that called the guardrail would only prove the guardrail agrees
// with itself. Here we check the final artifact for what actually landed, however
// it got there.
var (
	forbiddenWalletMarkers = []string{
		"@solana/wallet-adapter",
		"@solana/wallet-standard",
		"@wallet-standard/",
		"@solflare-wallet/",
		"@phantom/",
		"'wallet-standard'",
		"\"wallet-standard\"",
	}
	secretMarkers = []string{
		"fromsecretkey",
		"'bip39'",
		"\"bip39\"",
		"mnemonictoseed",
		"generatemnemonic",
	}
	// Unambiguous web/DOM markers that have no place in a React Native file.
	webPrimitiveMarkers = []string{"<div", "<span", "classname="}
	mwaMarker           = "@solana-mobile/mobile-wallet-adapter-protocol"
)

// Grade scores one build against one eval. It is pure and deterministic: the same
// sample and eval always produce the same scorecard.
func Grade(model string, sample Sample, ev Eval) Scorecard {
	c := Scorecard{Model: model, EvalID: ev.ID}
	add := func(name string, critical, passed bool, detail string) {
		c.Results = append(c.Results, Result{Name: name, Critical: critical, Passed: passed, Detail: detail})
	}

	// Did the build finish and pass verification? Critical — an app that does not
	// compile is not an app.
	switch {
	case sample.Err != "":
		add("build-finished", true, false, "build errored: "+sample.Err)
	case !sample.Verified:
		add("build-finished", true, false, "verification ladder did not pass")
	default:
		add("build-finished", true, true, "")
	}

	// Security, both critical: no browser wallet package, no key/seed material. These
	// are the constraints that cannot be relaxed for any model.
	if path, hit := firstMatch(sample.Files, forbiddenWalletMarkers); hit {
		add("no-browser-wallet", true, false, path+" references a browser wallet package")
	} else {
		add("no-browser-wallet", true, true, "")
	}
	if path, hit := firstMatch(sample.Files, secretMarkers); hit {
		add("no-secret-material", true, false, path+" handles a private key or seed phrase")
	} else {
		add("no-secret-material", true, true, "")
	}

	// Correctness, non-critical: the router scaffold survived, and no web primitives
	// leaked into the RN code.
	if ok, detail := routerIntact(sample.Files); ok {
		add("router-structure", false, true, "")
	} else {
		add("router-structure", false, false, detail)
	}
	if path, hit := webPrimitive(sample.Files); hit {
		add("native-components-only", false, false, path+" uses a web/DOM primitive")
	} else {
		add("native-components-only", false, true, "")
	}

	// Per-eval expectations.
	if ev.Expect.UsesMWA {
		// Scan code, not package.json — MWA is preinstalled, so it is always in the
		// manifest; what we are grading is whether the app actually uses it.
		if _, hit := firstMatchCode(sample.Files, []string{mwaMarker}); hit {
			add("uses-mwa", false, true, "")
		} else {
			add("uses-mwa", false, false, "a wallet task with no Mobile Wallet Adapter import")
		}
	}
	for _, imp := range ev.Expect.ImportsAnyOf {
		if _, hit := firstMatchCode(sample.Files, []string{strings.ToLower(imp)}); !hit {
			add("imports:"+imp, false, false, "expected an import of "+imp)
		} else {
			add("imports:"+imp, false, true, "")
		}
	}
	if len(ev.Expect.CreatesFileMatching) > 0 {
		if path, ok := pathMatching(sample.Files, ev.Expect.CreatesFileMatching); ok {
			_ = path
			add("creates-expected-file", false, true, "")
		} else {
			add("creates-expected-file", false, false,
				"no file matching "+strings.Join(ev.Expect.CreatesFileMatching, " / "))
		}
	}

	return c
}

// firstMatch returns the first file (in sorted path order, for a stable Detail)
// whose lowercased content contains any of the markers. It scans every file,
// including package.json — a browser wallet in dependencies is a failure whether
// or not any code imports it.
func firstMatch(files map[string]string, markers []string) (string, bool) {
	for _, path := range sortedPaths(files) {
		lower := strings.ToLower(files[path])
		for _, m := range markers {
			if strings.Contains(lower, m) {
				return path, true
			}
		}
	}
	return "", false
}

// firstMatchCode is firstMatch restricted to source files. Usage checks ("did the
// app actually import X") must ignore the manifest, since a preinstalled package
// is always listed there regardless of whether the code uses it.
func firstMatchCode(files map[string]string, markers []string) (string, bool) {
	for _, path := range sortedPaths(files) {
		if !isCodeFile(path) {
			continue
		}
		lower := strings.ToLower(files[path])
		for _, m := range markers {
			if strings.Contains(lower, m) {
				return path, true
			}
		}
	}
	return "", false
}

func isCodeFile(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".ts", ".tsx", ".js", ".jsx":
		return true
	}
	return false
}

// webPrimitive looks for DOM markers, but only inside component files — a stray
// "<div" in a comment in package.json is not the failure we mean.
func webPrimitive(files map[string]string) (string, bool) {
	for _, path := range sortedPaths(files) {
		switch strings.ToLower(filepath.Ext(path)) {
		case ".tsx", ".jsx":
		default:
			continue
		}
		lower := strings.ToLower(files[path])
		for _, m := range webPrimitiveMarkers {
			if strings.Contains(lower, m) {
				return path, true
			}
		}
	}
	return "", false
}

// routerIntact checks the expo-router scaffold the template ships with is still
// there: the entry file that imports expo-router, and at least one route under
// app/. A build that deletes these produces a project that cannot boot.
func routerIntact(files map[string]string) (bool, string) {
	entry, ok := files["index.js"]
	if !ok {
		return false, "index.js entry file is missing"
	}
	if !strings.Contains(entry, "expo-router/entry") {
		return false, "index.js no longer imports expo-router/entry"
	}
	for _, path := range sortedPaths(files) {
		if strings.HasPrefix(path, "app/") && strings.HasSuffix(path, ".tsx") {
			return true, ""
		}
	}
	return false, "no route file under app/"
}

func pathMatching(files map[string]string, fragments []string) (string, bool) {
	for _, path := range sortedPaths(files) {
		for _, frag := range fragments {
			if strings.Contains(path, frag) {
				return path, true
			}
		}
	}
	return "", false
}

func sortedPaths(files map[string]string) []string {
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	return paths
}

// BuildFunc runs one eval prompt to completion and returns what it produced. The
// caller wires this to a real orchestrator build (or a fake, in tests); the evals
// package stays free of that dependency.
type BuildFunc func(ctx context.Context, ev Eval) (Sample, error)

// Run grades every eval in the suite for one model. A build that cannot run at all
// is recorded as a failed sample rather than aborting the run, so one broken eval
// does not hide the rest of the scorecard.
func Run(ctx context.Context, model string, suite []Eval, build BuildFunc) []Scorecard {
	cards := make([]Scorecard, 0, len(suite))
	for _, ev := range suite {
		sample, err := build(ctx, ev)
		if err != nil {
			sample.Err = err.Error()
		}
		cards = append(cards, Grade(model, sample, ev))
	}
	return cards
}

// Suite is the fixed set of build tasks. The prompts are stable on purpose: they
// are the yardstick, so they change only when the yardstick should.
func Suite() []Eval {
	return []Eval{
		{
			ID:     "counter",
			Prompt: "Build a simple tap counter: a screen with a large number and a button that increments it. No wallet, no network.",
			// No wallet — this eval checks basic RN correctness in isolation.
		},
		{
			ID:     "balance-viewer",
			Prompt: "Build a read-only screen that connects the wallet with Mobile Wallet Adapter and shows the SOL balance of the connected account. Show a pending state while loading and let the user disconnect.",
			Expect: Expectations{UsesMWA: true},
		},
		{
			ID:     "send-sol",
			Prompt: "Build a screen to send SOL: a recipient address field, an amount field, and a Send button that builds the transaction and signs it with Mobile Wallet Adapter. Handle the user cancelling the signature.",
			Expect: Expectations{UsesMWA: true, CreatesFileMatching: []string{"app/"}},
		},
	}
}

// Snapshot reads a built workspace into the file map Grade expects: the source and
// config files, skipping dependency and native-build trees and anything too large
// to be hand-written source. Paths are workspace-relative.
func Snapshot(root string) (map[string]string, error) {
	const maxFileBytes = 128 * 1024
	skipDirs := map[string]bool{
		"node_modules": true, ".git": true, "android": true, "ios": true,
		".expo": true, "dist": true, "build": true, ".turbo": true,
	}
	includeExt := map[string]bool{
		".ts": true, ".tsx": true, ".js": true, ".jsx": true, ".json": true,
	}

	files := map[string]string{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skipDirs[d.Name()] {
				return fs.SkipDir
			}
			return nil
		}
		if !includeExt[strings.ToLower(filepath.Ext(p))] {
			return nil
		}
		info, err := d.Info()
		if err != nil || info.Size() > maxFileBytes {
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return nil
		}
		files[filepath.ToSlash(rel)] = string(data)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("snapshotting workspace: %w", err)
	}
	return files, nil
}
