package api

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/luckyisrael/talon/backend/internal/store"
)

// FM-WALLb(ii) — the linkage state lives server-side where no surface can
// see it: /v1/me must state wallet_linked or every navigation surface
// invents its own probe and they disagree. The key must EXIST and be false
// while unlinked (absence is the failure), and flip true on link.
//
// Red today (observed): the profile JSON has no wallet_linked key at all.
func TestMeReportsWalletLinked(t *testing.T) {
	st, err := store.New(filepath.Join(t.TempDir(), "me-wallet.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	h := NewMeHandler(st)

	u, err := st.CreateUser("walletless@talon.dev", "hash")
	if err != nil {
		t.Fatal(err)
	}

	read := func() (map[string]any, string) {
		t.Helper()
		rec := httptest.NewRecorder()
		h.Get(rec, authedRequest(u.ID, ""))
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /v1/me status %d: %s", rec.Code, rec.Body.String())
		}
		var out map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode profile: %v", err)
		}
		return out, rec.Body.String()
	}

	out, body := read()
	v, ok := out["wallet_linked"]
	if !ok {
		t.Fatalf("FM-WALLb(ii): /v1/me does not report wallet_linked at all — every surface would invent its own probe; body = %s", body)
	}
	if v != false {
		t.Fatalf("an account with no wallet must report wallet_linked=false, got %v", v)
	}

	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.LinkWallet(u.ID, base58Encode(pub)); err != nil {
		t.Fatalf("LinkWallet: %v", err)
	}

	out, body = read()
	if out["wallet_linked"] != true {
		t.Fatalf("after linking, wallet_linked must flip true (reminder must vanish on link); got %v in %s", out["wallet_linked"], body)
	}
}
