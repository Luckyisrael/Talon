package evals

import "testing"

// TestFixtureScorecard is W6.3's named scorecard stage (make scorecard):
// grader correctness gates merges on the four fixture cases. Each case is a
// planted artifact mutation of the clean wallet sample — the two criticals
// must disqualify, the non-criticals must ding without disqualifying, and
// the clean sample must stay perfect. This is what stops the grader from
// quietly agreeing with whatever the model shipped.
func TestFixtureScorecard(t *testing.T) {
	t.Run("clean", func(t *testing.T) {
		c := Grade("scorecard", cleanWalletSample(), walletEval())
		if !c.Passed() {
			t.Fatalf("clean wallet sample must pass; failures: %+v", c.Failures())
		}
		if c.Score() != 1.0 {
			t.Errorf("clean wallet sample must score 1.0, got %.2f (failures %+v)", c.Score(), c.Failures())
		}
	})

	t.Run("browser-wallet", func(t *testing.T) {
		s := cleanWalletSample()
		s.Files["app/index.tsx"] = "import { useWallet } from '@solana/wallet-adapter-react';\n"
		c := Grade("scorecard", s, walletEval())
		mustFail(t, c, "no-browser-wallet", true)
		if c.Passed() {
			t.Error("a browser-wallet import must disqualify the build however good the rest is")
		}
	})

	t.Run("secret-material", func(t *testing.T) {
		s := cleanWalletSample()
		s.Files["lib/wallet.ts"] = "const kp = Keypair.fromSecretKey(bytes);\n"
		c := Grade("scorecard", s, walletEval())
		mustFail(t, c, "no-secret-material", true)
		if c.Passed() {
			t.Error("handling a secret key must disqualify the build")
		}
	})

	t.Run("web-primitive", func(t *testing.T) {
		s := cleanWalletSample()
		s.Files["app/index.tsx"] = "export default function Home() { return <div className=\"x\">hi</div>; }\n"
		c := Grade("scorecard", s, walletEval())
		mustFail(t, c, "native-components-only", false)
		if !c.Passed() {
			t.Error("web primitives are a non-critical ding — the criticals still pass, so Passed() holds")
		}
	})
}
