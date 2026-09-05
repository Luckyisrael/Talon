package agent

import (
	"strings"
	"testing"
)

func TestNormalizeCollapsesNoise(t *testing.T) {
	a := NormalizeError("app/index.tsx(3,5): error TS2304: Cannot find name 'Foo'")
	b := NormalizeError("app/other.tsx(99,2): error TS2304: Cannot find name 'Bar'")
	// Line numbers, file paths, and case collapse; the identifier itself is
	// significant (it names the missing symbol) and must survive.
	for _, s := range []string{a, b} {
		if !strings.Contains(s, "cannot find name") {
			t.Errorf("lost the error kind: %q", s)
		}
		if strings.ContainsAny(s, "ABCDEFGHIJKLMNOPQRSTUVWXYZ") {
			t.Errorf("should be lowercase: %q", s)
		}
		if strings.Contains(s, "app/index") || strings.Contains(s, "(3,5)") {
			t.Errorf("paths and positions should collapse: %q", s)
		}
	}
}

func TestLookupHits(t *testing.T) {
	idx := DefaultFailureIndex()
	cases := []struct {
		tool, err, skill string
	}{
		{"run_typecheck", "app/x.tsx(1,1): error TS2322: Type 'string' is not assignable to type 'number'.", ""},
		{"run_typecheck", "error TS2304: Cannot find name 'Foo'", ""},
		{"run_command", "Error: Cannot find module 'left-pad'", ""},
		{"write_file", "ENOENT: no such file or directory, open 'app/nope.tsx'", ""},
		{"test_wallet_roundtrip", "user rejected the request", "wallet-mwa"},
		{"run_command", "429 too many requests", ""},
		{"build_apk", "Execution failed for task ':app:assembleRelease'", ""},
		{"install_package", "adb: device not found", ""},
	}
	for _, c := range cases {
		p, ok := Lookup(idx, c.tool, c.err)
		if !ok {
			t.Errorf("Lookup(%q, %q): no hit", c.tool, c.err)
			continue
		}
		if p.Skill != c.skill {
			t.Errorf("Lookup(%q, %q): skill %q, want %q", c.tool, c.err, p.Skill, c.skill)
		}
		if !strings.Contains(p.Reminder(), p.Fix) {
			t.Errorf("reminder should contain the fix: %q", p.Reminder())
		}
	}
}

func TestLookupToolScope(t *testing.T) {
	idx := DefaultFailureIndex()
	// TS2322 scoped to run_typecheck must not fire for other tools.
	if _, ok := Lookup(idx, "run_lint", "error TS2322: Type 'string' is not assignable"); ok {
		t.Error("tool-scoped pattern fired for the wrong tool")
	}
	if _, ok := Lookup(idx, "anything", "total gibberish with no meaning"); ok {
		t.Error("gibberish should not match")
	}
}
