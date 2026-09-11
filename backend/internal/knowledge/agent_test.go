package knowledge

import (
	"strings"
	"testing"
)

// testRegistry loads the real embedded content plus the real project template.
// If load fails the embedded content is malformed — a fatal authoring error, and
// exactly what this test exists to catch before it ships.
func testRegistry(t *testing.T) *Registry {
	t.Helper()
	r, err := Load("../../template/package.json")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return r
}

func TestLoadEmbeddedContent(t *testing.T) {
	r := testRegistry(t)

	if len(r.rules) == 0 {
		t.Fatal("no rules loaded")
	}
	var haveAlwaysApply bool
	for _, rule := range r.rules {
		if rule.AlwaysApply {
			haveAlwaysApply = true
		}
		if strings.TrimSpace(rule.Body) == "" {
			t.Errorf("rule %q has empty body", rule.Name)
		}
	}
	if !haveAlwaysApply {
		t.Error("expected at least one always-apply rule")
	}

	// Every skill we ship: the Phase-A internal ones plus the WS3-vendored
	// Expo/Vercel skills, plus the curated Talon bundle (Phase 1) and the
	// failure-pattern index. If one is renamed, update this list deliberately —
	// the manifest and triggers depend on these names.
	want := []string{
		"app-linking",
		"dapp-store",
		"expo-router-nav",
		"expo-ui",
		"expo-ui-jetpack-compose",
		"failure-patterns",
		"mobile-defaults",
		"reanimated-v4",
		"rn-best-practices",
		"scl",
		"talon-mobile",
		"talon-screen-design",
		"seed-vault",
		"wallet-mwa",
	}
	for _, name := range want {
		body, ok := r.SkillBody(name)
		if !ok {
			t.Errorf("skill %q not loaded", name)
			continue
		}
		if strings.TrimSpace(body) == "" {
			t.Errorf("skill %q has empty body", name)
		}
	}
}

func TestRulesBlockAndManifest(t *testing.T) {
	r := testRegistry(t)

	rb := r.RulesBlock()
	if rb == "" {
		t.Fatal("RulesBlock is empty")
	}
	// The preinstalled list is read from the real template, which ships these.
	for _, pkg := range []string{"expo-router", "react-native-reanimated"} {
		if !strings.Contains(rb, pkg) {
			t.Errorf("RulesBlock missing preinstalled package %q", pkg)
		}
	}

	m := r.Manifest()
	for _, name := range r.SkillNames() {
		if !strings.Contains(m, name) {
			t.Errorf("Manifest missing skill %q", name)
		}
	}
}

func TestAutoAttachFor(t *testing.T) {
	r := testRegistry(t)

	cases := []struct {
		name       string
		tool       string
		input      string
		want       string // skill expected in the result, or "" for none
		wantDesign bool   // talon-screen-design auto-attaches on app/** and components/** writes
	}{
		{
			name:       "reanimated import in written file",
			tool:       "write_file",
			input:      `{"path":"components/Card.tsx","content":"import Animated from 'react-native-reanimated';"}`,
			want:       "reanimated-v4",
			wantDesign: true, // components/** path
		},
		{
			name:       "route file by path",
			tool:       "write_file",
			input:      `{"path":"app/token/[id].tsx","content":"export default function T(){}"}`,
			want:       "expo-router-nav",
			wantDesign: true, // app/** path
		},
		{
			name:  "install mobile wallet adapter",
			tool:  "install_package",
			input: `{"name":"@solana-mobile/mobile-wallet-adapter-protocol-web3js"}`,
			want:  "wallet-mwa",
		},
		{
			name:  "plain view, no trigger",
			tool:  "write_file",
			input: `{"path":"lib/format.ts","content":"export const f = () => 1;"}`,
			want:  "", // lib/ is neither app/ nor components/ — no design skill either
		},
		{
			name:       "screen file pulls the design skill",
			tool:       "write_file",
			input:      `{"path":"app/home.tsx","content":"// SafeAreaView AMOLED FlatList touch target"}`,
			want:       "talon-screen-design", // now autoAttach:true on app/**; expo-router-nav co-attaches
			wantDesign: true,
		},
		{
			name:       "expo/ui import pulls expo-ui",
			tool:       "write_file",
			input:      `{"path":"components/MapSheet.tsx","content":"import { BottomSheet } from '@expo/ui';"}`,
			want:       "expo-ui",
			wantDesign: true, // components/** path
		},
		{
			name:       "gorhom bottom-sheet import redirects to expo-ui",
			tool:       "write_file",
			input:      `{"path":"components/Sheet.tsx","content":"import BottomSheet from '@gorhom/bottom-sheet';"}`,
			want:       "expo-ui", // expo-ui claims the gorhom import to steer toward the native sheet
			wantDesign: true,
		},
		{
			name:       "jetpack-compose import pulls the platform-specific skill",
			tool:       "write_file",
			input:      `{"path":"components/ProductList.android.tsx","content":"import { Column } from '@expo/ui/jetpack-compose';"}`,
			want:       "expo-ui-jetpack-compose", // expo-ui also co-attaches (@expo/ui is a substring); both are autoAttach
			wantDesign: true,
		},
		{
			name:       "FlatList in written content pulls rn-best-practices",
			tool:       "write_file",
			input:      `{"path":"components/Feed.tsx","content":"<FlatList data={items} renderItem={renderItem} />"}`,
			want:       "rn-best-practices",
			wantDesign: true,
		},
		{
			name:  "installing flash-list pulls rn-best-practices",
			tool:  "install_package",
			input: `{"name":"@shopify/flash-list"}`,
			want:  "rn-best-practices",
		},
		{
			name:  "kit import pulls solana-dev",
			tool:  "write_file",
			input: `{"path":"lib/wallet.ts","content":"import { address } from '@solana/kit';"}`,
			want:  "solana-dev",
		},
		{
			name:       "wallet-ui import pulls solana-dev",
			tool:       "write_file",
			input:      `{"path":"app/auth.tsx","content":"import { useMobileWallet } from '@wallet-ui/react-native-kit';"}`,
			want:       "solana-dev",
			wantDesign: true, // app/** path
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := r.AutoAttachFor(tc.tool, []byte(tc.input))
			names := map[string]bool{}
			for _, s := range got {
				names[s.Name] = true
				if !s.AutoAttach {
					t.Errorf("auto-attached a non-autoAttach skill: %q", s.Name)
				}
			}
			if tc.want != "" && !names[tc.want] {
				t.Errorf("expected skill %q, got %v", tc.want, keys(names))
			}
			// talon-screen-design auto-attaches on app/** and components/** writes
			// and must stay out everywhere else (lib/ files, package installs).
			if names["talon-screen-design"] != tc.wantDesign {
				t.Errorf("talon-screen-design auto-attach = %v, want %v (got %v)",
					names["talon-screen-design"], tc.wantDesign, keys(names))
			}
		})
	}
}

func TestGlobMatch(t *testing.T) {
	cases := []struct {
		pattern, path string
		want          bool
	}{
		{"app/**", "app/index.tsx", true},
		{"app/**", "app/token/[id].tsx", true},
		{"app/**", "app", true},
		{"app/**", "./app/index.tsx", true},
		{"app/**", "components/Card.tsx", false},
		{"app/**", "lib/app/x.ts", false},
		{"*", "anything.ts", true},
		{"**/babel.config.js", "babel.config.js", true},
	}
	for _, tc := range cases {
		if got := globMatch(tc.pattern, tc.path); got != tc.want {
			t.Errorf("globMatch(%q, %q) = %v, want %v", tc.pattern, tc.path, got, tc.want)
		}
	}
}

func TestSkillBodyUnknown(t *testing.T) {
	r := testRegistry(t)
	if _, ok := r.SkillBody("does-not-exist"); ok {
		t.Error("expected unknown skill to return ok=false")
	}
	if _, ok := r.SkillBody(""); ok {
		t.Error("expected empty name to return ok=false")
	}
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// TestSkillsForSymptom covers the new (Phase-1) post-tool-error dispatch. A
// known TS2322 / "Cannot find module" / "adb: device not found" message must
// pull failure-patterns; an unrelated message must not. Case is normalised so
// the model cannot trip it with "adb: DEVICE NOT FOUND".
func TestSkillsForSymptom(t *testing.T) {
	r := testRegistry(t)
	cases := []struct {
		name    string
		errText string
		want    string
	}{
		{"ts2322", "app/index.tsx(12,3): error TS2322: Type 'number' is not assignable to type 'string'.", "failure-patterns"},
		{"metro cannot find module", "Error: Cannot find module 'foo' from 'app/index.ts'", "failure-patterns"},
		{"adb no devices", "adb: device not found", "failure-patterns"},
		{"adb uppercase", "ADB: DEVICE NOT FOUND", "failure-patterns"},
		{"unrelated", "the user closed the modal", ""},
		{"empty", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := r.SkillsForSymptom(tc.errText)
			names := map[string]bool{}
			for _, s := range got {
				names[s.Name] = true
			}
			if tc.want == "" {
				if names["failure-patterns"] {
					t.Errorf("unexpected failure-patterns match for %q", tc.errText)
				}
				return
			}
			if !names[tc.want] {
				t.Errorf("expected skill %q for %q, got %v", tc.want, tc.errText, keys(names))
			}
		})
	}
}

// TestSkillsForPhase covers the new (Phase-1) per-round seeding. The
// failure-patterns skill is phase:PhaseHeal; asking for PhaseGenerate or any
// unknown phase must not pull it. The orchestrator calls this once per round
// and the contract is "the named skills are present, not all of them".
func TestSkillsForPhase(t *testing.T) {
	r := testRegistry(t)
	t.Run("PhaseHeal pulls failure-patterns", func(t *testing.T) {
		got := r.SkillsForPhase("PhaseHeal")
		var found bool
		for _, s := range got {
			if s.Name == "failure-patterns" {
				found = true
			}
		}
		if !found {
			t.Errorf("PhaseHeal should pull failure-patterns, got %v", namesOf(got))
		}
	})
	t.Run("PhaseGenerate pulls nothing (no skill advertises it yet)", func(t *testing.T) {
		got := r.SkillsForPhase("PhaseGenerate")
		if len(got) != 0 {
			t.Errorf("PhaseGenerate should pull nothing today, got %v", namesOf(got))
		}
	})
	t.Run("unknown phase is empty, not an error", func(t *testing.T) {
		if got := r.SkillsForPhase("PhasePretend"); len(got) != 0 {
			t.Errorf("expected empty, got %v", namesOf(got))
		}
	})
	t.Run("empty phase is empty", func(t *testing.T) {
		if got := r.SkillsForPhase(""); len(got) != 0 {
			t.Errorf("expected empty, got %v", namesOf(got))
		}
	})
}

func namesOf(ss []*Skill) []string {
	out := make([]string, 0, len(ss))
	for _, s := range ss {
		out = append(out, s.Name)
	}
	return out
}

// TestSymptomTriggerIsNotAutoAttached is the small invariant that prevents the
// new trigger kinds from regressing AutoAttachFor's contract. A `symptom:`-only
// skill must not auto-attach on a tool input; it is loaded by the orchestrator
// post-tool, not by the trigger engine.
func TestSymptomTriggerIsNotAutoAttached(t *testing.T) {
	r := testRegistry(t)
	got := r.AutoAttachFor("write_file", []byte(`{"path":"app/index.tsx","content":"x"}`))
	for _, s := range got {
		if s.Name == "failure-patterns" {
			t.Errorf("failure-patterns auto-attached on tool input; should be orchestrator-driven only")
		}
	}
}
