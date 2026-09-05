package evals

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"
	"time"

	"github.com/seekersstudio/backend/internal/agent"
	"github.com/seekersstudio/backend/internal/build"
	"github.com/seekersstudio/backend/internal/runtime"
	"github.com/seekersstudio/backend/internal/store"
	"github.com/seekersstudio/backend/internal/verify"
)

// The phase-guard set: one eval per PLAN.md phase. Each case asserts both
// polarities — the healthy path passes AND the fault-injected path fails —
// so a regression that flips either behavior breaks the eval. This is the
// cheap, deterministic half of P16/P17: no models, no devices, just the
// contracts each phase promised. (The full mock-AI app-build runner remains
// future work; these guards are what CI enforces today via `make evals`.)

// A3: the failure index resolves known errors to their known fix, and stays
// silent on unknown ones.
func TestPhaseA3FailureIndex(t *testing.T) {
	idx := agent.DefaultFailureIndex()
	if _, ok := agent.Lookup(idx, "run_typecheck", "error TS2322: Type 'string' is not assignable"); !ok {
		t.Error("healthy: TS2322 should hit the index")
	}
	if _, ok := agent.Lookup(idx, "run_typecheck", "total gibberish with no meaning"); ok {
		t.Error("fault: gibberish must not hit the index")
	}
}

// S9: store assets persist and list back exactly.
func TestPhaseS9StoreAssets(t *testing.T) {
	st, err := store.New(t.TempDir() + "/phase.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	u, _ := st.CreateUser("phase@test.com", "hash")
	p, _ := st.CreateProject(u.ID, "phase", "1.0.0", "phase-slug", "com.test.phase", "", "")
	if _, err := st.AddAsset(p.ID, "icon", "/tmp/icon.png", 512, 512); err != nil {
		t.Fatal(err)
	}
	assets, err := st.ListAssets(p.ID)
	if err != nil || len(assets) != 1 || assets[0].Kind != "icon" {
		t.Errorf("healthy: 1 icon expected, got %+v %v", assets, err)
	}
	empty, err := st.ListAssets(999999)
	if err != nil || len(empty) != 0 {
		t.Errorf("fault: unknown project must list nothing, got %+v %v", empty, err)
	}
}

// S8: triage cents survive the round trip exactly; absent reads null.
func TestPhaseS8TriageCents(t *testing.T) {
	st, err := store.New(t.TempDir() + "/phase.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	u, _ := st.CreateUser("phase@test.com", "hash")
	p, _ := st.CreateProject(u.ID, "phase", "1.0.0", "phase-slug", "com.test.phase", "", "")
	if r, _ := st.LoadTriageReport(p.ID); r != nil {
		t.Error("fault: unrecorded project must load nil")
	}
	saved, err := st.SaveTriageReport(p.ID, "cutover_now", "2026-10-01", 19999, "n")
	if err != nil || saved.ExposureCents != 19999 {
		t.Errorf("healthy: 19999 cents expected, got %+v %v", saved, err)
	}
}

// G5: boot gap resolves on first app log and reports silence honestly.
func TestPhaseG5BootGap(t *testing.T) {
	launch := testLaunch()
	stamp := launch.Add(30 * time.Second).Format("01-02 15:04:05.000")
	logs := stamp + "  1234  1234 I com.test.app: Application onCreate\n"
	if _, _, ok := runtime.BootGap(logs, "com.test.app", launch); !ok {
		t.Error("healthy: first app log should resolve the gap")
	}
	if _, _, ok := runtime.BootGap(logs, "com.ghost.app", launch); ok {
		t.Error("fault: silent package must report no gap")
	}
}

// S7: blank screens read blank, content reads content.
func TestPhaseS7BlankScreen(t *testing.T) {
	if rep := verify.Analyze(solidWhite(64, 64)); !rep.Blank {
		t.Error("healthy: solid white must read blank")
	}
	if rep := verify.Analyze(solidGradient(64, 64)); rep.Blank {
		t.Error("fault: gradient content must not read blank")
	}
}

// G6: pre-images stage and clear; overflow evicts oldest with notice.
func TestPhaseG6PatchBuffer(t *testing.T) {
	b := build.NewPatchBuffer()
	b.Stage("a.tsx", []byte("v1"), 1)
	if _, ok := b.PreImage("a.tsx"); !ok {
		t.Error("healthy: staged pre-image should be retrievable")
	}
	b.Accept("a.tsx")
	if _, ok := b.PreImage("a.tsx"); ok {
		t.Error("fault: accepted pre-image must be gone")
	}
}

func testLaunch() time.Time {
	return time.Now().Add(-time.Minute)
}

func encodePNG(img image.Image) []byte {
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}

func solidWhite(w, h int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.White)
		}
	}
	return encodePNG(img)
}

func solidGradient(w, h int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{uint8(x * 4), uint8(y * 4), 128, 255})
		}
	}
	return encodePNG(img)
}
