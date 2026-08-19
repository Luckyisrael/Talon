package evals

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// cleanWalletSample is a minimal but correct MWA app: router scaffold intact, RN
// components only, wallet work through Mobile Wallet Adapter, and a build that
// verified. It should score perfectly against a wallet eval.
func cleanWalletSample() Sample {
	return Sample{
		Verified: true,
		Rounds:   5,
		Files: map[string]string{
			"index.js":        "import 'expo-router/entry';\n",
			"app/_layout.tsx": "import { Stack } from 'expo-router';\nexport default function Layout() { return <Stack />; }\n",
			"app/index.tsx": "import { transact } from '@solana-mobile/mobile-wallet-adapter-protocol-web3js';\n" +
				"import { View, Text, Pressable } from 'react-native';\n" +
				"export default function Home() { return <View><Text>Balance</Text></View>; }\n",
			"package.json": `{"dependencies":{"@solana-mobile/mobile-wallet-adapter-protocol-web3js":"^2.0.0"}}`,
		},
	}
}

func result(c Scorecard, name string) (Result, bool) {
	for _, r := range c.Results {
		if r.Name == name {
			return r, true
		}
	}
	return Result{}, false
}

func mustFail(t *testing.T, c Scorecard, name string, wantCritical bool) {
	t.Helper()
	r, ok := result(c, name)
	if !ok {
		t.Fatalf("expected a %q check to have run; results: %+v", name, c.Results)
	}
	if r.Passed {
		t.Errorf("expected %q to fail", name)
	}
	if r.Critical != wantCritical {
		t.Errorf("%q critical = %v, want %v", name, r.Critical, wantCritical)
	}
}

func walletEval() Eval {
	return Eval{ID: "balance-viewer", Prompt: "…", Expect: Expectations{UsesMWA: true}}
}

func TestGradeCleanBuildPasses(t *testing.T) {
	c := Grade("test-model", cleanWalletSample(), walletEval())
	if !c.Passed() {
		t.Fatalf("a clean wallet build should pass; failures: %+v", c.Failures())
	}
	if c.Score() != 1.0 {
		t.Errorf("a clean build should score 1.0, got %.2f (failures %+v)", c.Score(), c.Failures())
	}
	if r, _ := result(c, "uses-mwa"); !r.Passed {
		t.Error("the MWA usage check should pass for a wallet app that imports MWA")
	}
}

func TestGradeBrowserWalletDisqualifies(t *testing.T) {
	s := cleanWalletSample()
	s.Files["app/index.tsx"] = "import { useWallet } from '@solana/wallet-adapter-react';\n"
	c := Grade("test-model", s, walletEval())

	mustFail(t, c, "no-browser-wallet", true)
	if c.Passed() {
		t.Error("a browser-wallet import must disqualify the build however good the rest is")
	}
}

func TestGradeSecretMaterialDisqualifies(t *testing.T) {
	s := cleanWalletSample()
	s.Files["lib/wallet.ts"] = "const kp = Keypair.fromSecretKey(bytes);\n"
	c := Grade("test-model", s, walletEval())

	mustFail(t, c, "no-secret-material", true)
	if c.Passed() {
		t.Error("handling a secret key must disqualify the build")
	}
}

func TestGradeBrokenRouterIsNonCritical(t *testing.T) {
	s := cleanWalletSample()
	delete(s.Files, "index.js") // the entry file the native build needs
	c := Grade("test-model", s, walletEval())

	mustFail(t, c, "router-structure", false)
	// Router breakage is a correctness ding, not a disqualification: the criticals
	// still passed, so the build is "acceptable but flawed", and the score reflects it.
	if !c.Passed() {
		t.Error("a broken router alone should not flip Passed() — it is not a critical check")
	}
	if c.Score() == 1.0 {
		t.Error("a broken router should cost score")
	}
}

func TestGradeWebPrimitivesFail(t *testing.T) {
	s := cleanWalletSample()
	s.Files["app/index.tsx"] = "export default function Home() { return <div className=\"x\">hi</div>; }\n"
	c := Grade("test-model", s, walletEval())
	mustFail(t, c, "native-components-only", false)
}

func TestGradeErroredBuildFailsFinished(t *testing.T) {
	s := Sample{Err: "stopped after 40 rounds"}
	c := Grade("test-model", s, walletEval())
	mustFail(t, c, "build-finished", true)
	if c.Passed() {
		t.Error("a build that never finished cannot pass")
	}
}

func TestGradeMissingMWAForWalletTask(t *testing.T) {
	s := cleanWalletSample()
	// A wallet screen that forgot the wallet: strip the MWA import.
	s.Files["app/index.tsx"] = "import { View, Text } from 'react-native';\nexport default function Home() { return <View><Text>Balance</Text></View>; }\n"
	c := Grade("test-model", s, walletEval())

	mustFail(t, c, "uses-mwa", false)
	// It is a correctness failure, not a security one — nothing forbidden was done.
	if !c.Passed() {
		t.Error("a missing MWA import is a correctness ding, not a disqualification")
	}
}

// A non-wallet eval must not sprout a uses-mwa check — expectations are per-eval.
func TestGradeNoMWACheckWhenNotExpected(t *testing.T) {
	counter := Eval{ID: "counter", Prompt: "…"}
	s := Sample{
		Verified: true,
		Files: map[string]string{
			"index.js":      "import 'expo-router/entry';\n",
			"app/index.tsx": "import { View, Text } from 'react-native';\nexport default function C() { return <View><Text>0</Text></View>; }\n",
		},
	}
	c := Grade("test-model", s, counter)
	if _, ok := result(c, "uses-mwa"); ok {
		t.Error("a non-wallet eval should not run the MWA check")
	}
	if !c.Passed() || c.Score() != 1.0 {
		t.Errorf("a clean counter should pass perfectly, got score %.2f failures %+v", c.Score(), c.Failures())
	}
}

func TestRunRecordsBuildErrorsWithoutAborting(t *testing.T) {
	suite := Suite()
	// A build func that fails the second eval but succeeds the others.
	build := func(_ context.Context, ev Eval) (Sample, error) {
		if ev.ID == "balance-viewer" {
			return Sample{}, context.DeadlineExceeded
		}
		return cleanWalletSample(), nil
	}
	cards := Run(context.Background(), "flaky-model", suite, build)

	if len(cards) != len(suite) {
		t.Fatalf("Run should produce one card per eval, got %d for %d evals", len(cards), len(suite))
	}
	for _, c := range cards {
		if c.EvalID == "balance-viewer" {
			if r, _ := result(c, "build-finished"); r.Passed {
				t.Error("the failed eval's build-finished check should have failed")
			}
			if !strings.Contains(mustDetail(t, c, "build-finished"), "deadline") {
				t.Error("the build error should be recorded in the detail")
			}
		}
	}
}

func mustDetail(t *testing.T, c Scorecard, name string) string {
	t.Helper()
	r, ok := result(c, name)
	if !ok {
		t.Fatalf("no %q check", name)
	}
	return r.Detail
}

func TestRenderScorecardsIsDeterministicAndGrouped(t *testing.T) {
	cards := []Scorecard{
		Grade("model-b", cleanWalletSample(), walletEval()),
		Grade("model-a", cleanWalletSample(), Eval{ID: "counter", Prompt: "…"}),
		Grade("model-a", cleanWalletSample(), walletEval()),
	}
	out := RenderScorecards(cards)

	// Stable across calls — the report is meant to be diffed run over run.
	if out != RenderScorecards(cards) {
		t.Error("RenderScorecards output should be deterministic")
	}
	// model-a sorts before model-b.
	if strings.Index(out, "Model: model-a") > strings.Index(out, "Model: model-b") {
		t.Error("models should be rendered in sorted order")
	}
	for _, want := range []string{"Model: model-a", "Model: model-b", "PASS", "summary", "balance-viewer", "counter"} {
		if !strings.Contains(out, want) {
			t.Errorf("report missing %q:\n%s", want, out)
		}
	}
}

func TestSnapshotSelectsSourceOnly(t *testing.T) {
	root := t.TempDir()
	write := func(rel, content string) {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	write("index.js", "import 'expo-router/entry';")
	write("app/index.tsx", "export default function X() { return null; }")
	write("package.json", `{"name":"x"}`)
	write("README.md", "# not source")
	write("node_modules/foo/index.js", "module.exports = 1;")
	write("android/build.gradle", "// native")
	write("big.json", "{\"x\":\""+strings.Repeat("a", 200*1024)+"\"}")

	files, err := Snapshot(root)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	for _, want := range []string{"index.js", "app/index.tsx", "package.json"} {
		if _, ok := files[want]; !ok {
			t.Errorf("Snapshot should include %q", want)
		}
	}
	for _, notWant := range []string{"README.md", "node_modules/foo/index.js", "android/build.gradle", "big.json"} {
		if _, ok := files[notWant]; ok {
			t.Errorf("Snapshot should exclude %q", notWant)
		}
	}

	// And the snapshot must be gradeable end to end.
	c := Grade("snap", Sample{Files: files, Verified: true}, Eval{ID: "counter", Prompt: "…"})
	if r, _ := result(c, "router-structure"); !r.Passed {
		t.Error("the snapshotted workspace should have an intact router")
	}
}
