package evals

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// W14.5 / H13.1 — the Model Experience docs guard, run as its own named
// make stage (Makefile: docs). Three laws live here:
//
//  1. Every model-facing package writes down what the model sees. The
//     harness/* and tools/ trees are discovered — a new package fails the
//     guard until its README carries the section — and the pipeline stages
//     (which live outside harness/) are declared below.
//  2. No comment-code lies (law 12): a TALONSTUDIO_* flag named in a
//     README or a non-test code comment must be referenced by real
//     non-test code AND documented in .env.example — a mention that names
//     nothing the boot reads is the same drift class as a WithModel
//     comment that promises behavior the code does not have.
//  3. The W8.3 flag-surface law, executable: everything .env.example
//     documents is referenced by non-test code.
//
// "Referenced" means the flag appears as a quoted string literal in
// non-test code (AST literals — comments excluded, so a comment-only
// invention does not count as its own evidence). That shape covers direct
// os.Getenv("...") calls and const indirection like the embed flags'
// EnvURL, and it is exactly what a comment would have to point at to be
// true.

// stageREADMEs are the pipeline stages between the loop and what the model
// is told: each must carry a "What the model sees" section. harness/* and
// tools/ are discovered dynamically; these live directly under internal/,
// so they are declared — a new stage joins this list the same commit it
// joins the plan.
var stageREADMEs = []string{
	"guardrails", "verify", "runtime", "redroid", "metro", "release", "devclient",
}

var (
	modelSeesPhrase = "what the model sees" // compared case-insensitively
	flagNameRe      = regexp.MustCompile(`TALONSTUDIO_[A-Z0-9_]+`)
)

// TestModelExperienceDocs: the H13.1 docs guard. Every subtest is a law
// that used to live in a plan paragraph; this is it enforced by CI,
// permanently.
func TestModelExperienceDocs(t *testing.T) {
	root := backendRoot(t)

	t.Run("harness-readmes", func(t *testing.T) {
		harnessDir := filepath.Join(root, "internal", "harness")
		entries, err := os.ReadDir(harnessDir)
		if err != nil {
			t.Fatalf("read %s: %v", harnessDir, err)
		}
		found := 0
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			found++
			requireModelSees(t, filepath.Join(harnessDir, e.Name(), "README.md"))
		}
		if found == 0 {
			t.Fatalf("no harness packages found under %s — the guard is walking the wrong tree", harnessDir)
		}
	})

	t.Run("tools-readme", func(t *testing.T) {
		requireModelSees(t, filepath.Join(root, "internal", "tools", "README.md"))
	})

	t.Run("stage-readmes", func(t *testing.T) {
		for _, stage := range stageREADMEs {
			requireModelSees(t, filepath.Join(root, "internal", stage, "README.md"))
		}
	})

	t.Run("flag-lies", func(t *testing.T) {
		codeStrs, commentMentions := codeScan(t, root)
		docMentions := readmeMentions(t, root)

		exampleBlob, err := os.ReadFile(filepath.Join(filepath.Dir(root), ".env.example"))
		if err != nil {
			t.Fatalf(".env.example must exist (flag-surface law): %v", err)
		}
		example := map[string]bool{}
		for _, n := range flagNameRe.FindAllString(string(exampleBlob), -1) {
			example[n] = true
		}

		// Direction 1+2 (law 12 / docs honesty): every mention — README or
		// code comment — must be wired (a real code string references it)
		// and documented (.env.example names it).
		mentions := map[string]string{}
		for flag, src := range docMentions {
			mentions[flag] = src
		}
		for flag := range commentMentions {
			if _, dup := mentions[flag]; !dup {
				mentions[flag] = "a non-test code comment"
			}
		}
		for _, flag := range sortedKeys(mentions) {
			src := mentions[flag]
			if !codeStrs[flag] {
				t.Errorf("%s names %s, but no non-test Go code references it — a comment-code lie (law 12): read it or drop the mention",
					src, flag)
			}
			if !example[flag] {
				t.Errorf("%s names %s, but .env.example does not document it — a flag the model's operators are told about must be settable",
					src, flag)
			}
		}

		// Direction 3 (W8.3, executable): everything .env.example documents
		// is referenced by non-test code — no flag in the example is a lie.
		for _, flag := range sortedBoolKeys(example) {
			if !codeStrs[flag] {
				t.Errorf(".env.example documents %s, but no non-test Go code references it (W8.3 flag-surface law)", flag)
			}
		}

		// Vacuity guard: if the mention sources stopped being read (a walk
		// that matches nothing), the directions above would pass by having
		// nothing to say — the law must keep finding flags to police.
		if len(mentions) == 0 {
			t.Error("no TALONSTUDIO_* flags found in any README or code comment — the mention sources are not being read")
		}
		if len(example) == 0 {
			t.Error("no TALONSTUDIO_* flags found in .env.example — the flag-surface law has nothing to check")
		}
	})
}

// requireModelSees fails when a README is missing or lacks the section —
// the section is the point, not the file.
func requireModelSees(t *testing.T, path string) {
	t.Helper()
	blob, err := os.ReadFile(path)
	if err != nil {
		t.Errorf("model-experience doc missing: %s (%v) — every harness/tool/stage package must carry a %q section",
			path, err, modelSeesPhrase)
		return
	}
	if !strings.Contains(strings.ToLower(string(blob)), modelSeesPhrase) {
		t.Errorf("%s has no %q section — write down what this package's model experience actually is (H13.1)",
			path, modelSeesPhrase)
	}
}

// codeScan parses every non-test .go file under root once and returns two
// maps keyed by flag name: flags referenced by real code (string literals
// — comments excluded) and flags named inside comments (mentions that must
// stay honest). Vendored node_modules is skipped.
func codeScan(t *testing.T, root string) (codeStrs, commentMentions map[string]bool) {
	t.Helper()
	codeStrs, commentMentions = map[string]bool{}, map[string]bool{}
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == "node_modules" || d.Name() == ".git") {
			return filepath.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, perr := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if perr != nil {
			t.Errorf("parse %s: %v", path, perr)
			return nil
		}
		for _, cg := range f.Comments {
			for _, flag := range flagNameRe.FindAllString(cg.Text(), -1) {
				commentMentions[flag] = true
			}
		}
		ast.Inspect(f, func(n ast.Node) bool {
			if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING {
				for _, flag := range flagNameRe.FindAllString(lit.Value, -1) {
					codeStrs[flag] = true
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	return codeStrs, commentMentions
}

// readmeMentions collects TALONSTUDIO_* names from every README under the
// backend tree plus the repo-root README (vendored node_modules skipped),
// keyed by flag with the first source path as the value.
func readmeMentions(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	add := func(src string, blob []byte) {
		for _, flag := range flagNameRe.FindAllString(string(blob), -1) {
			if _, seen := out[flag]; !seen {
				out[flag] = src
			}
		}
	}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == "node_modules" || d.Name() == ".git") {
			return filepath.SkipDir
		}
		if !d.IsDir() && d.Name() == "README.md" {
			blob, rerr := os.ReadFile(path)
			if rerr != nil {
				return rerr
			}
			add(path, blob)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk READMEs under %s: %v", root, err)
	}
	if repoREADME := filepath.Join(filepath.Dir(root), "README.md"); fileExists(repoREADME) {
		blob, err := os.ReadFile(repoREADME)
		if err != nil {
			t.Fatalf("read %s: %v", repoREADME, err)
		}
		add(repoREADME, blob)
	}
	return out
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func sortedBoolKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
