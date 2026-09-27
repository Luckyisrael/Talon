package guardrails

import (
	"encoding/json"
	"strings"
	"testing"
)

// fakeState lets a test say which files were read and which exist.
type fakeState struct {
	read   map[string]bool
	exists map[string]bool
}

func (f fakeState) HasReadFile(p string) bool { return f.read[p] }
func (f fakeState) FileExists(p string) bool  { return f.exists[p] }

func args(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

// ── install denylist ──────────────────────────────────

func TestInstallDenylist(t *testing.T) {
	preinstalled := map[string]bool{"@solana/web3.js": true, "expo-router": true}

	t.Run("browser wallet adapter is refused", func(t *testing.T) {
		for _, pkg := range []string{
			"@solana/wallet-adapter-react",
			"@solana/wallet-adapter-base",
			"@solana/wallet-standard",
			"wallet-standard",
			"@wallet-standard/core",
			"@solflare-wallet/sdk",
			"@phantom/wallet-sdk",
		} {
			d := Check("install_package", args(t, map[string]any{"name": pkg}), nil, preinstalled)
			if d.Allowed || d.Rule != "browser-wallet-package" {
				t.Errorf("%s should be refused as a browser wallet package, got %+v", pkg, d)
			}
		}
	})

	// The whole point of the scope discriminator: the correct mobile packages
	// must sail through even though their names contain "wallet-adapter".
	t.Run("the mobile wallet adapter is allowed", func(t *testing.T) {
		for _, pkg := range []string{
			"@solana-mobile/mobile-wallet-adapter-protocol",
			"@solana-mobile/mobile-wallet-adapter-protocol-web3js",
		} {
			if d := Check("install_package", args(t, map[string]any{"name": pkg}), nil, preinstalled); !d.Allowed {
				t.Errorf("%s is the correct MWA package and must be allowed, got %+v", pkg, d)
			}
		}
	})

	t.Run("reinstalling a preinstalled package is refused", func(t *testing.T) {
		d := Check("install_package", args(t, map[string]any{"name": "expo-router"}), nil, preinstalled)
		if d.Allowed || d.Rule != "already-preinstalled" {
			t.Errorf("want already-preinstalled refusal, got %+v", d)
		}
		// A version suffix must not smuggle it past the base-name check.
		d = Check("install_package", args(t, map[string]any{"name": "expo-router@1.2.3"}), nil, preinstalled)
		if d.Allowed || d.Rule != "already-preinstalled" {
			t.Errorf("version-suffixed reinstall should still be refused, got %+v", d)
		}
	})

	t.Run("a genuinely new package is allowed", func(t *testing.T) {
		if d := Check("install_package", args(t, map[string]any{"name": "date-fns"}), nil, preinstalled); !d.Allowed {
			t.Errorf("date-fns is fine to install, got %+v", d)
		}
	})

	t.Run("nil preinstalled disables only the reinstall check", func(t *testing.T) {
		// Browser wallet is still refused with no preinstalled set...
		if d := Check("install_package", args(t, map[string]any{"name": "@solana/wallet-adapter-react"}), nil, nil); d.Allowed {
			t.Error("browser wallet must be refused even without a preinstalled set")
		}
		// ...but an ordinary package is allowed since we cannot know it is preinstalled.
		if d := Check("install_package", args(t, map[string]any{"name": "expo-router"}), nil, nil); !d.Allowed {
			t.Error("with no preinstalled set the reinstall check must not fire")
		}
	})
}

// ── write constraints: content scan ───────────────────

func TestContentConstraints(t *testing.T) {
	readAll := fakeState{read: map[string]bool{"app/x.tsx": true}}

	cases := []struct {
		name, content, wantRule string
	}{
		{"browser wallet import", "import { useWallet } from '@solana/wallet-adapter-react'", "browser-wallet-import"},
		{"wallet-standard import", "import x from 'wallet-standard'", "browser-wallet-import"},
		{"solflare sdk", "import Solflare from '@solflare-wallet/sdk'", "browser-wallet-import"},
		{"secret key in code", "const kp = Keypair.fromSecretKey(bytes)", "secret-key-in-code"},
		{"seed phrase handling", "import { mnemonicToSeed } from 'bip39'", "seed-phrase-in-code"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := Check("write_file", args(t, map[string]any{"path": "app/x.tsx", "content": c.content}), readAll, nil)
			if d.Allowed || d.Rule != c.wantRule {
				t.Errorf("want %s refusal, got %+v", c.wantRule, d)
			}
		})
	}

	t.Run("the correct MWA import is allowed", func(t *testing.T) {
		content := "import { transact } from '@solana-mobile/mobile-wallet-adapter-protocol-web3js'"
		d := Check("write_file", args(t, map[string]any{"path": "app/x.tsx", "content": content}), readAll, nil)
		if !d.Allowed {
			t.Errorf("the MWA import must be allowed, got %+v", d)
		}
	})

	t.Run("edit_file content is scanned too", func(t *testing.T) {
		d := Check("edit_file", args(t, map[string]any{
			"path": "app/x.tsx", "old_string": "a", "new_string": "import '@solana/wallet-adapter-base'",
		}), readAll, nil)
		if d.Allowed || d.Rule != "browser-wallet-import" {
			t.Errorf("an edit that introduces a banned import must be refused, got %+v", d)
		}
	})

	t.Run("multi_edit scans every new_string", func(t *testing.T) {
		d := Check("multi_edit", args(t, map[string]any{
			"path": "app/x.tsx",
			"edits": []map[string]any{
				{"old_string": "a", "new_string": "const ok = 1"},
				{"old_string": "b", "new_string": "Keypair.fromSecretKey(x)"},
			},
		}), readAll, nil)
		if d.Allowed || d.Rule != "secret-key-in-code" {
			t.Errorf("multi_edit must scan all edits, got %+v", d)
		}
	})
}

// ── read-before-write ─────────────────────────────────

func TestReadBeforeWrite(t *testing.T) {
	t.Run("editing an unread file is refused", func(t *testing.T) {
		st := fakeState{read: map[string]bool{}, exists: map[string]bool{"app/x.tsx": true}}
		d := Check("edit_file", args(t, map[string]any{
			"path": "app/x.tsx", "old_string": "a", "new_string": "b",
		}), st, nil)
		if d.Allowed || d.Rule != "read-before-write" {
			t.Errorf("editing an unread file must be refused, got %+v", d)
		}
	})

	t.Run("editing a file read this turn is allowed", func(t *testing.T) {
		st := fakeState{read: map[string]bool{"app/x.tsx": true}}
		if d := Check("edit_file", args(t, map[string]any{
			"path": "app/x.tsx", "old_string": "a", "new_string": "b",
		}), st, nil); !d.Allowed {
			t.Errorf("a read file should be editable, got %+v", d)
		}
	})

	t.Run("creating a new file needs no prior read", func(t *testing.T) {
		st := fakeState{read: map[string]bool{}, exists: map[string]bool{}}
		if d := Check("write_file", args(t, map[string]any{
			"path": "app/new.tsx", "content": "export const x = 1",
		}), st, nil); !d.Allowed {
			t.Errorf("creating a new file must be allowed, got %+v", d)
		}
	})

	t.Run("overwriting an existing unread file is refused", func(t *testing.T) {
		st := fakeState{read: map[string]bool{}, exists: map[string]bool{"app/old.tsx": true}}
		d := Check("write_file", args(t, map[string]any{
			"path": "app/old.tsx", "content": "export const x = 1",
		}), st, nil)
		if d.Allowed || d.Rule != "read-before-write" {
			t.Errorf("overwriting an unread existing file must be refused, got %+v", d)
		}
	})

	t.Run("nil state disables the read check", func(t *testing.T) {
		if d := Check("edit_file", args(t, map[string]any{
			"path": "app/x.tsx", "old_string": "a", "new_string": "b",
		}), nil, nil); !d.Allowed {
			t.Errorf("a nil state must not block edits, got %+v", d)
		}
	})
}

// ── unrelated tools and malformed input ───────────────

func TestCheckPassthrough(t *testing.T) {
	if d := Check("read_file", args(t, map[string]any{"path": "x"}), nil, nil); !d.Allowed {
		t.Error("read_file is not guarded and must pass")
	}
	if d := Check("run_command", args(t, map[string]any{"command": "ls"}), nil, nil); !d.Allowed {
		t.Error("run_command is not guarded here and must pass")
	}
	// Malformed JSON is the tool's problem to report, not the guardrail's.
	if d := Check("install_package", json.RawMessage(`{not json`), nil, nil); !d.Allowed {
		t.Error("malformed args must not be turned into a guardrail rejection")
	}
}

func TestBasePackageName(t *testing.T) {
	cases := map[string]string{
		"@scope/pkg@^1.2.3": "@scope/pkg",
		"@scope/pkg":        "@scope/pkg",
		"lodash@4":          "lodash",
		"lodash":            "lodash",
		"  Expo-Router  ":   "expo-router",
	}
	for in, want := range cases {
		if got := basePackageName(in); got != want {
			t.Errorf("basePackageName(%q) = %q, want %q", in, got, want)
		}
	}
}

// A rejection message must tell the model what to do next, not just refuse —
// otherwise it retries the same call.
func TestRejectionMessagesAreActionable(t *testing.T) {
	d := Check("install_package", args(t, map[string]any{"name": "@solana/wallet-adapter-react"}), nil, nil)
	if d.Allowed || !strings.Contains(d.Message, "transact()") {
		t.Errorf("the browser-wallet refusal should point at transact(), got %q", d.Message)
	}
}

// ── L10: node_modules survey guardrail ───────────────────────────────
//
// Failure mode, written first (ENGINEERING_STANDARDS §6, FM-L10a): a
// blanket node_modules read ban breaks legitimate reads — package configs
// and root files — while the survey it must stop keeps its own failure
// mode: Run 2 burned ~16 minutes reading .d.ts archaeology under a prompt
// that already said "use inspect_package". The refusal must cover the
// survey-style paths (deep reads, directory targets, glob/path targeting),
// exempt configs and root files by exact segment scope (a false positive
// on package.json at the project root would be worse than the disease),
// and never be silent — it answers with inspect_package.
func TestNodeModulesSurveyRefused(t *testing.T) {
	refused := []struct {
		tool string
		in   map[string]any
		why  string
	}{
		{"read_file", map[string]any{"path": "node_modules/expo-notifications/build/Notifications.types.d.ts"}, "deep .d.ts read (Run-2's actual archaeology)"},
		{"read_file", map[string]any{"path": "node_modules/@expo/vector-icons/build/Ionicons.d.ts", "limit": 20}, "scoped deep .d.ts read"},
		{"read_file", map[string]any{"path": "/abs/ws/node_modules/typescript/lib/lib.es5.d.ts"}, "absolute deep read"},
		{"find_files", map[string]any{"path": "node_modules/expo-notifications", "pattern": "*.d.ts"}, "find targeting a package tree (Run-2)"},
		{"find_files", map[string]any{"path": "node_modules", "pattern": "*.d.ts"}, "walk of node_modules itself"},
		{"grep_codebase", map[string]any{"path": "node_modules/expo-notifications", "pattern": "export (type|interface) X"}, "grep with a path arg (Run-2 sent this although the schema never declared it)"},
		{"grep_codebase", map[string]any{"glob": "node_modules/expo-notifications/**/*.d.ts", "pattern": "X"}, "grep with a node_modules glob (Run-2)"},
		{"search_files", map[string]any{"glob": "node_modules/**"}, "the search_files alias is the same tool"},
	}
	for _, c := range refused {
		d := Check(c.tool, args(t, c.in), nil, nil)
		if d.Allowed {
			t.Errorf("%s %s: not refused", c.tool, c.why)
			continue
		}
		if d.Rule != "node-modules-survey" {
			t.Errorf("%s %s: rule = %q, want node-modules-survey", c.tool, c.why, d.Rule)
		}
		if !strings.Contains(d.Message, "inspect_package") {
			t.Errorf("%s %s: refusal must answer with inspect_package, got %q", c.tool, c.why, d.Message)
		}
	}
}

func TestNodeModulesSurveyAllowsConfigsRootsAndProjectFiles(t *testing.T) {
	allowed := []struct {
		tool string
		in   map[string]any
		why  string
	}{
		{"read_file", map[string]any{"path": "node_modules/expo-notifications/package.json"}, "a package config (FM-L10a)"},
		{"read_file", map[string]any{"path": "node_modules/@solana-mobile/mobile-wallet-adapter-protocol-web3js/package.json"}, "a scoped package config"},
		{"read_file", map[string]any{"path": "node_modules/.package-lock.json"}, "a node_modules root file (FM-L10a)"},
		{"read_file", map[string]any{"path": "package.json"}, "the project root config — segment scope must not catch it"},
		{"read_file", map[string]any{"path": "tsconfig.json"}, "a root config"},
		{"read_file", map[string]any{"path": "src/node_modules.config.ts"}, "a project file whose NAME contains node_modules"},
		{"read_file", map[string]any{"path": "app/index.tsx"}, "project source"},
		{"grep_codebase", map[string]any{"pattern": "from ['\"]node_modules/"}, "grep CONTENT referencing node_modules — the walk already excludes the tree"},
		{"find_files", map[string]any{"path": "app", "pattern": "*.tsx"}, "an ordinary find"},
	}
	for _, c := range allowed {
		d := Check(c.tool, args(t, c.in), nil, nil)
		if !d.Allowed {
			t.Errorf("%s %s: wrongly refused (%s)", c.tool, c.why, d.Message)
		}
	}
}
