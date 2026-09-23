package composition

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeDoc puts content in a temp dir and returns the file's path.
func writeDoc(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "talon.composition.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write doc: %v", err)
	}
	return path
}

// overlayDoc exercises every overlay rule at once: a base pair, a profile
// override, and a preset override — different configs for the same id in
// each layer, plus an untouched base row.
const overlayDoc = `
base:
  - id: loop
    component: harness/loop
    config:
      maxRounds: 60
      note: seed-note
  - id: sandbox
    component: harness/sandbox
    config:
      posture: readonly
profiles:
  server:
    - id: loop
      component: harness/loop
      config:
        maxRounds: 6
presets:
  expert:
    - id: loop
      component: harness/loop
      config:
        maxRounds: 120
`

// The overlay contract (H12): base → profile → preset, last-write-wins per
// id with WHOLE-config replacement — a later layer's row replaces the
// earlier one completely (no key from the lower layer survives), and an
// override keeps its id's original position so apply order stays stable.
func TestLoadOverlayPrecedence(t *testing.T) {
	path := writeDoc(t, overlayDoc)

	t.Run("base only (unknown layers)", func(t *testing.T) {
		c, err := Load(path, "serverless", "")
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		rows := c.Rows()
		if len(rows) != 2 || rows[0].ID != "loop" || rows[1].ID != "sandbox" {
			t.Fatalf("rows = %+v, want [loop sandbox]", rows)
		}
		if rows[0].Config["maxRounds"] != 60 || rows[0].Config["note"] != "seed-note" {
			t.Errorf("base loop config = %+v, want both keys intact", rows[0].Config)
		}
	})

	t.Run("profile replaces the row whole", func(t *testing.T) {
		c, err := Load(path, "server", "")
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		loop := c.Rows()[0]
		if loop.Config["maxRounds"] != 6 {
			t.Errorf("profile maxRounds = %v, want 6", loop.Config["maxRounds"])
		}
		if _, leaked := loop.Config["note"]; leaked {
			t.Errorf("whole-config replacement leaked base key note=%v", loop.Config)
		}
		if c.Profile != "server" || c.Preset != "" {
			t.Errorf("layers recorded as profile=%q preset=%q", c.Profile, c.Preset)
		}
	})

	t.Run("preset wins and keeps position", func(t *testing.T) {
		c, err := Load(path, "server", "expert")
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		rows := c.Rows()
		if len(rows) != 2 || rows[0].ID != "loop" || rows[1].ID != "sandbox" {
			t.Fatalf("order after preset override = %+v, want [loop sandbox] (first-appearance order)", rows)
		}
		if rows[0].Config["maxRounds"] != 120 {
			t.Errorf("preset maxRounds = %v, want 120", rows[0].Config["maxRounds"])
		}
		if rows[1].Config["posture"] != "readonly" {
			t.Errorf("base sandbox row moved or merged: %+v", rows[1].Config)
		}
	})
}

// A tree without a composition boots dark: (nil, nil), no error — the
// file is opt-in wiring, not a new mandatory contract.
func TestLoadMissingFileIsDark(t *testing.T) {
	c, err := Load(filepath.Join(t.TempDir(), "talon.composition.yaml"), "server", "")
	if err != nil {
		t.Fatalf("Load(missing) error: %v", err)
	}
	if c != nil {
		t.Fatalf("Load(missing) = %+v, want nil (dark)", c)
	}
}

// Untrusted wiring must not boot: a parse error or a structurally invalid
// row fails Load — the caller's boot treats that as fatal.
func TestLoadErrorsFailLoud(t *testing.T) {
	cases := []struct {
		name string
		doc  string
		want string
	}{
		{"malformed yaml", "base: [", "parse"},
		{"row without id", "base:\n  - component: harness/loop\n", "has no id"},
		{"row without component", "base:\n  - id: loop\n", "has no component"},
		{"whitespace id", "base:\n  - id: '  '\n    component: harness/loop\n", "has no id"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Load(writeDoc(t, c.doc), "server", "")
			if err == nil {
				t.Fatal("Load = nil error, want failure")
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("error %q does not mention %q", err, c.want)
			}
		})
	}
}

// The dump (--dump-config, W14.4) is the golden diff's subject: stable
// bytes across marshals, the file's base name (never a machine path), the
// selected layers, rows in apply order — and an empty composition renders
// as rows: [] so the diff has bytes in every state.
func TestMarshalJSONDumpShape(t *testing.T) {
	c, err := Load(writeDoc(t, overlayDoc), "server", "expert")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	first, err := json.Marshal(c)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	second, err := json.Marshal(c)
	if err != nil {
		t.Fatalf("Marshal(2): %v", err)
	}
	if string(first) != string(second) {
		t.Fatalf("dump not deterministic:\n1: %s\n2: %s", first, second)
	}

	var got struct {
		Path    string `json:"path"`
		Profile string `json:"profile"`
		Preset  string `json:"preset"`
		Rows    []Row  `json:"rows"`
	}
	if err := json.Unmarshal(first, &got); err != nil {
		t.Fatalf("unmarshal dump: %v", err)
	}
	if got.Path != "talon.composition.yaml" {
		t.Errorf("path = %q, want the base name (a golden must not bake machine paths)", got.Path)
	}
	if got.Profile != "server" || got.Preset != "expert" {
		t.Errorf("layers = %q/%q, want server/expert", got.Profile, got.Preset)
	}
	if len(got.Rows) != 2 || got.Rows[0].ID != "loop" || got.Rows[1].ID != "sandbox" {
		t.Errorf("rows = %+v, want apply order [loop sandbox]", got.Rows)
	}

	empty, err := json.Marshal(&Composition{Profile: "server"})
	if err != nil {
		t.Fatalf("Marshal(empty): %v", err)
	}
	if !strings.Contains(string(empty), `"rows":[]`) {
		t.Errorf("empty dump = %s, want rows:[] (never null)", empty)
	}
}
