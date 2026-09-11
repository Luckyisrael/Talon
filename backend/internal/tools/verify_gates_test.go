package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/luckyisrael/talon/backend/internal/knowledge"
)

// All G4 gate tools must report "unavailable" rather than fail when the
// supporting infrastructure is missing. These tests exercise that path only —
// running the real `npx`, `gradlew`, or `adb` would require a device and a JDK
// and would make the test suite host-dependent.

func TestRunBundleUnavailableWithoutPackageJSON(t *testing.T) {
	dir := t.TempDir()
	res, err := (&RunBundle{WorkspacePath: dir}).Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	vr := mustUnmarshalVerify(t, []byte(res.Content))
	if !vr.Passed {
		t.Fatalf("expected unavailable (passed=true), got fail: %s", vr.Summary)
	}
	if !strings.Contains(vr.Summary, "no package.json") {
		t.Fatalf("expected 'no package.json' summary, got: %s", vr.Summary)
	}
}

func TestRunGradleUnavailableWithoutAndroidDir(t *testing.T) {
	dir := t.TempDir()
	res, err := (&RunGradle{WorkspacePath: dir}).Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	vr := mustUnmarshalVerify(t, []byte(res.Content))
	if !vr.Passed {
		t.Fatalf("expected unavailable, got fail: %s", vr.Summary)
	}
	if !strings.Contains(vr.Summary, "no android/gradlew") {
		t.Fatalf("expected 'no android/gradlew' summary, got: %s", vr.Summary)
	}
}

func TestRunBootSkipsWhenNoADBClient(t *testing.T) {
	dir := t.TempDir()
	res, err := (&RunBoot{WorkspacePath: dir, APKPath: filepath.Join(dir, "a.apk"), PkgName: "com.x"}).Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	vr := mustUnmarshalVerify(t, []byte(res.Content))
	if !vr.Passed {
		t.Fatalf("expected unavailable, got fail: %s", vr.Summary)
	}
	if !strings.Contains(vr.Summary, "no adb client") {
		t.Fatalf("expected 'no adb client' summary, got: %s", vr.Summary)
	}
}

func TestRunBootSkipsWhenNoInputs(t *testing.T) {
	dir := t.TempDir()
	res, err := (&RunBoot{WorkspacePath: dir, ADBClient: &ADBRunner{Target: "localhost:5555"}}).Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	vr := mustUnmarshalVerify(t, []byte(res.Content))
	if !vr.Passed || !strings.Contains(vr.Summary, "no apk/pkg") {
		t.Fatalf("expected 'no apk/pkg' unavailable, got: passed=%v summary=%q", vr.Passed, vr.Summary)
	}
}

func TestRunVisionSkipsWhenNoIntended(t *testing.T) {
	dir := t.TempDir()
	res, err := (&RunVision{WorkspacePath: dir}).Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	vr := mustUnmarshalVerify(t, []byte(res.Content))
	if !vr.Passed || !strings.Contains(vr.Summary, "no intended") {
		t.Fatalf("expected 'no intended' unavailable, got: passed=%v summary=%q", vr.Passed, vr.Summary)
	}
}

func TestRunVisionSkipsWhenNoCapture(t *testing.T) {
	res, err := (&RunVision{WorkspacePath: t.TempDir(), IntendedScreen: "home"}).Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	vr := mustUnmarshalVerify(t, []byte(res.Content))
	if !vr.Passed || !strings.Contains(vr.Summary, "no capture") {
		t.Fatalf("expected 'no capture' unavailable, got: passed=%v summary=%q", vr.Passed, vr.Summary)
	}
}

func TestRunInteractPassesOnEmptyRouteList(t *testing.T) {
	res, err := (&RunInteract{WorkspacePath: t.TempDir()}).Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	vr := mustUnmarshalVerify(t, []byte(res.Content))
	if !vr.Passed || !strings.Contains(vr.Summary, "no routes") {
		t.Fatalf("expected 'no routes' pass, got: passed=%v summary=%q", vr.Passed, vr.Summary)
	}
}

func TestRunInteractSkipsWhenNoCapture(t *testing.T) {
	res, err := (&RunInteract{WorkspacePath: t.TempDir(), Routes: []string{"home", "settings"}}).Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	vr := mustUnmarshalVerify(t, []byte(res.Content))
	if !vr.Passed || !strings.Contains(vr.Summary, "no capture") {
		t.Fatalf("expected 'no capture' unavailable, got: passed=%v summary=%q", vr.Passed, vr.Summary)
	}
}

func TestRunGateReturnsEmptyWhenNoReport(t *testing.T) {
	res, err := (&RunGate{ReportProvider: func() *VerifyReport { return nil }}).Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	vr := mustUnmarshalVerify(t, []byte(res.Content))
	if !vr.Passed || !strings.Contains(vr.Summary, "no chain") {
		t.Fatalf("expected 'no chain' pass, got: passed=%v summary=%q", vr.Passed, vr.Summary)
	}
}

func TestRunGateReportsPassAndFail(t *testing.T) {
	passRes, _ := (&RunGate{ReportProvider: func() *VerifyReport {
		return &VerifyReport{OverallPassed: true, Gates: []VerifyReportGate{
			{Stage: "typecheck", Status: "pass", Note: "clean"},
			{Stage: "lint", Status: "pass", Note: "clean"},
		}}
	}}).Execute(context.Background(), json.RawMessage(`{}`))
	passVR := mustUnmarshalVerify(t, []byte(passRes.Content))
	if !passVR.Passed || !strings.Contains(passVR.Summary, "typecheck=pass") {
		t.Fatalf("expected pass summary to enumerate gates, got: %q", passVR.Summary)
	}

	failRes, _ := (&RunGate{ReportProvider: func() *VerifyReport {
		return &VerifyReport{OverallPassed: false, Gates: []VerifyReportGate{
			{Stage: "typecheck", Status: "pass", Note: "clean"},
			{Stage: "bundle", Status: "fail", Note: "metro error"},
		}}
	}}).Execute(context.Background(), json.RawMessage(`{}`))
	failVR := mustUnmarshalVerify(t, []byte(failRes.Content))
	if failVR.Passed {
		t.Fatalf("expected fail verdict, got pass: %s", failVR.Summary)
	}
	if !strings.Contains(failVR.Errors, "bundle: metro error") {
		t.Fatalf("expected fail errors to include the failing gate, got: %q", failVR.Errors)
	}
}

func TestVerifyReportSummaryAndFailingGates(t *testing.T) {
	r := &VerifyReport{OverallPassed: false, Gates: []VerifyReportGate{
		{Stage: "typecheck", Status: "pass", Note: "clean"},
		{Stage: "lint", Status: "fail", Note: "no-unused-vars on home.tsx"},
		{Stage: "bundle", Status: "fail", Note: "metro cannot resolve foo"},
	}}
	if !strings.Contains(r.summary(), "typecheck=pass") || !strings.Contains(r.summary(), "bundle=fail") {
		t.Fatalf("summary missing gate statuses: %q", r.summary())
	}
	failing := r.failingGates()
	if !strings.Contains(failing, "lint: no-unused-vars on home.tsx") {
		t.Fatalf("failing gates missing lint line: %q", failing)
	}
	if !strings.Contains(failing, "bundle: metro cannot resolve foo") {
		t.Fatalf("failing gates missing bundle line: %q", failing)
	}
	if strings.Contains(failing, "typecheck:") {
		t.Fatalf("passing gate leaked into failing gates: %q", failing)
	}
}

func TestRunBundleRespectsContextCancellation(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"name":"x","version":"1.0.0"}`), 0o644); err != nil {
		t.Fatalf("write package.json: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	res, err := (&RunBundle{WorkspacePath: dir}).Execute(ctx, json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	vr := mustUnmarshalVerify(t, []byte(res.Content))
	if !vr.Passed {
		t.Fatalf("expected unavailable on cancelled ctx, got fail: %s", vr.Summary)
	}
}

func mustUnmarshalVerify(t *testing.T, raw json.RawMessage) VerifyResult {
	t.Helper()
	var vr VerifyResult
	if err := json.Unmarshal([]byte(raw), &vr); err != nil {
		t.Fatalf("unmarshal verify result: %v (raw=%q)", err, string(raw))
	}
	return vr
}

func TestBootVerdictSilent(t *testing.T) {
	logs := "09-05 14:22:30.100   500   500 I boot_progress: boot complete\n" +
		"09-05 14:22:34.000   600   600 I other.tag: unrelated\n"
	vr := bootVerdict("com.test.app", logs, time.Now().Add(-time.Minute))
	if vr.Passed {
		t.Fatal("alive-but-silent must fail the boot gate")
	}
	if !strings.Contains(vr.Summary, "alive but silent") {
		t.Fatalf("expected silent summary, got %q", vr.Summary)
	}
}

func TestBootVerdictGap(t *testing.T) {
	now := time.Now()
	stamp := now.Add(-2 * time.Second).Format("01-02 15:04:05.000")
	logs := stamp + "  1234  1234 I com.test.app: Application onCreate\n"
	vr := bootVerdict("com.test.app", logs, now.Add(-time.Minute))
	if !vr.Passed {
		t.Fatalf("first-launch log should pass, got %q", vr.Summary)
	}
	if !strings.Contains(vr.Summary, "first log after") {
		t.Fatalf("summary should carry the boot gap, got %q", vr.Summary)
	}
}

// Five consecutive identical boot failures must all resolve to the same
// known fix — the suggestion is deterministic, never a fresh guess.
func TestBootFailureSuggestsSameFix(t *testing.T) {
	logs := "process alive but produced no log output:\n09-05 14:22:34.000   600   600 I other.tag: unrelated\n"
	idx := knowledge.DefaultFailureIndex()
	var first string
	for i := 0; i < 5; i++ {
		vr := bootVerdict("com.test.app", logs, time.Now().Add(-time.Minute))
		if vr.Passed {
			t.Fatal("silent boot must fail")
		}
		pat, ok := knowledge.Lookup(idx, "run_boot", vr.Errors)
		if !ok {
			t.Fatalf("round %d: silent boot should hit the failure index", i+1)
		}
		if i == 0 {
			first = pat.Reminder()
		} else if pat.Reminder() != first {
			t.Fatalf("round %d suggests a different fix:\n%q\nvs\n%q", i+1, pat.Reminder(), first)
		}
	}
}

type stubCapture struct{ png []byte }

func (s stubCapture) CaptureScreenshot(ctx context.Context, _, _ string) ([]byte, error) {
	return s.png, nil
}

type explodingVision struct{ called bool }

func (v *explodingVision) VisionCheck(ctx context.Context, _ []byte, _ string) (VisionGateResult, error) {
	v.called = true
	return VisionGateResult{}, nil
}

func solidTestPNG(w, h int, white bool) []byte {
	c := color.Black
	if white {
		c = color.White
	}
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, c)
		}
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}

// A blank screenshot fails the vision gate without spending a model call.
func TestRunVisionBlankFailsFast(t *testing.T) {
	vis := &explodingVision{}
	rv := &RunVision{
		Capture:        stubCapture{png: solidTestPNG(100, 200, true)},
		Vision:         vis,
		IntendedScreen: "Wallet",
		ADBClient:      &ADBRunner{Target: "localhost:5555"},
	}
	res, err := rv.Execute(context.Background(), json.RawMessage(`{"intended_screen":"Wallet"}`))
	if err != nil {
		t.Fatal(err)
	}
	vr := mustUnmarshalVerify(t, []byte(res.Content))
	if vr.Passed {
		t.Fatal("blank screenshot should fail the gate")
	}
	if vis.called {
		t.Fatal("blank screen must not spend a vision call")
	}
	if !strings.Contains(vr.Summary, "vision [Wallet]") {
		t.Fatalf("summary should name gate+screen, got %q", vr.Summary)
	}
}
