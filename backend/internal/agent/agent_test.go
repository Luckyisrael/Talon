package agent

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
	// Expo/Vercel skills. If one is renamed, update this list deliberately —
	// the manifest and triggers depend on these names.
	want := []string{
		"expo-router-nav",
		"expo-ui",
		"expo-ui-jetpack-compose",
		"reanimated-v4",
		"rn-best-practices",
		"seeker-screen-design",
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
		name  string
		tool  string
		input string
		want  string // skill expected in the result, or "" for none
	}{
		{
			name:  "reanimated import in written file",
			tool:  "write_file",
			input: `{"path":"components/Card.tsx","content":"import Animated from 'react-native-reanimated';"}`,
			want:  "reanimated-v4",
		},
		{
			name:  "route file by path",
			tool:  "write_file",
			input: `{"path":"app/token/[id].tsx","content":"export default function T(){}"}`,
			want:  "expo-router-nav",
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
			want:  "",
		},
		{
			name:  "screen-design content never auto-attaches",
			tool:  "write_file",
			input: `{"path":"app/home.tsx","content":"// SafeAreaView AMOLED FlatList touch target"}`,
			want:  "", // seeker-screen-design is autoAttach:false. The path trigger still
			// pulls expo-router-nav, and "FlatList" now pulls rn-best-practices — both
			// autoAttach:true — but seeker-screen-design must stay out (asserted below).
		},
		{
			name:  "expo/ui import pulls expo-ui",
			tool:  "write_file",
			input: `{"path":"components/MapSheet.tsx","content":"import { BottomSheet } from '@expo/ui';"}`,
			want:  "expo-ui",
		},
		{
			name:  "gorhom bottom-sheet import redirects to expo-ui",
			tool:  "write_file",
			input: `{"path":"components/Sheet.tsx","content":"import BottomSheet from '@gorhom/bottom-sheet';"}`,
			want:  "expo-ui", // expo-ui claims the gorhom import to steer toward the native sheet
		},
		{
			name:  "jetpack-compose import pulls the platform-specific skill",
			tool:  "write_file",
			input: `{"path":"components/ProductList.android.tsx","content":"import { Column } from '@expo/ui/jetpack-compose';"}`,
			want:  "expo-ui-jetpack-compose", // expo-ui also co-attaches (@expo/ui is a substring); both are autoAttach
		},
		{
			name:  "FlatList in written content pulls rn-best-practices",
			tool:  "write_file",
			input: `{"path":"components/Feed.tsx","content":"<FlatList data={items} renderItem={renderItem} />"}`,
			want:  "rn-best-practices",
		},
		{
			name:  "installing flash-list pulls rn-best-practices",
			tool:  "install_package",
			input: `{"name":"@shopify/flash-list"}`,
			want:  "rn-best-practices",
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
			// seeker-screen-design must never come through auto-attach.
			if names["seeker-screen-design"] {
				t.Error("seeker-screen-design should never auto-attach")
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
