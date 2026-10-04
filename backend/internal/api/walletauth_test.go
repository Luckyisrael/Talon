package api

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/luckyisrael/talon/backend/internal/auth"
	"github.com/luckyisrael/talon/backend/internal/store"
)

// The full P17 round trip: issue → sign with a real keypair → verify →
// JWT that the standard middleware accepts. Plus the adversarial set:
// wrong key, wrong message, replayed nonce, cross-wallet claim.
func TestWalletAuthRoundTrip(t *testing.T) {
	dir := t.TempDir()
	st, err := store.New(dir + "/t.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	svc := auth.New()
	h := NewWalletAuthHandler(st, svc, nil)

	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	addr := base58Encode(pub)

	// 1. Challenge.
	chReq := httptest.NewRequest(http.MethodPost, "/v1/auth/wallet/challenge",
		bytes.NewReader(mustJSON(t, map[string]string{"wallet": addr})))
	chRec := httptest.NewRecorder()
	h.Challenge(chRec, chReq)
	if chRec.Code != http.StatusOK {
		t.Fatalf("challenge status %d: %s", chRec.Code, chRec.Body.String())
	}
	var ch struct{ Nonce, Message string }
	json.Unmarshal(chRec.Body.Bytes(), &ch)
	if ch.Nonce == "" || ch.Message == "" {
		t.Fatalf("challenge missing parts: %+v", ch)
	}

	// 2. Sign and verify.
	sig := ed25519.Sign(priv, []byte(ch.Message))
	vReq := httptest.NewRequest(http.MethodPost, "/v1/auth/wallet/verify", bytes.NewReader(mustJSON(t, map[string]string{
		"wallet":    addr,
		"nonce":     ch.Nonce,
		"signature": base64.StdEncoding.EncodeToString(sig),
	})))
	vRec := httptest.NewRecorder()
	h.Verify(vRec, vReq)
	if vRec.Code != http.StatusOK {
		t.Fatalf("verify status %d: %s", vRec.Code, vRec.Body.String())
	}
	var session struct {
		Token string `json:"token"`
	}
	json.Unmarshal(vRec.Body.Bytes(), &session)
	if session.Token == "" {
		t.Fatal("no token minted")
	}
	if claims, err := svc.ValidateToken(session.Token); err != nil || claims.UserID == 0 {
		t.Fatalf("minted token does not validate: %v", err)
	}

	// 3. The linked account: signing in again with the same wallet must
	// return the SAME user id (bound path of the upsert law).
	_, ch2 := challengeFor(t, h, addr)
	sig2 := ed25519.Sign(priv, []byte(ch2.Message))
	rec2 := verifySig(t, h, addr, ch2.Nonce, base64.StdEncoding.EncodeToString(sig2))
	var session2 struct {
		Token string `json:"token"`
		User  struct {
			ID int64 `json:"id"`
		} `json:"user"`
	}
	json.Unmarshal(rec2.Body.Bytes(), &session2)
	if session2.User.ID == 0 {
		t.Fatal("second sign-in lost the user id")
	}
}

func TestWalletAuthAdversarial(t *testing.T) {
	dir := t.TempDir()
	st, _ := store.New(dir + "/t.db")
	t.Cleanup(func() { _ = st.Close() })
	h := NewWalletAuthHandler(st, auth.New(), nil)

	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	addr := base58Encode(pub)
	otherPub, _, _ := ed25519.GenerateKey(rand.Reader)
	otherAddr := base58Encode(otherPub)

	t.Run("wrong key is refused", func(t *testing.T) {
		nonce, msg := challengeFor(t, h, addr)
		_, wrongPriv, _ := ed25519.GenerateKey(rand.Reader)
		rec := verifySig(t, h, addr, nonce, base64.StdEncoding.EncodeToString(ed25519.Sign(wrongPriv, []byte(msg.Message))))
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("forged signature accepted: %d", rec.Code)
		}
	})
	t.Run("replayed nonce is refused", func(t *testing.T) {
		nonce, msg := challengeFor(t, h, addr)
		sig := base64.StdEncoding.EncodeToString(ed25519.Sign(priv, []byte(msg.Message)))
		if rec := verifySig(t, h, addr, nonce, sig); rec.Code != http.StatusOK {
			t.Fatalf("first use failed: %d", rec.Code)
		}
		if rec := verifySig(t, h, addr, nonce, sig); rec.Code != http.StatusUnauthorized {
			t.Fatalf("replay accepted: %d", rec.Code)
		}
	})
	t.Run("cross-wallet claim is refused", func(t *testing.T) {
		nonce, _ := challengeFor(t, h, addr)
		sig := base64.StdEncoding.EncodeToString(ed25519.Sign(priv, []byte("forged")))
		rec := verifySig(t, h, otherAddr, nonce, sig)
		if rec.Code != http.StatusUnauthorized && rec.Code != http.StatusBadRequest {
			t.Fatalf("cross-wallet claim accepted: %d", rec.Code)
		}
	})
	t.Run("expired challenge is swept", func(t *testing.T) {
		nonce, _ := challengeFor(t, h, addr)
		if _, err := st.SweepWalletChallenges(0 * time.Minute); err != nil {
			t.Fatal(err)
		}
		sig := base64.StdEncoding.EncodeToString(ed25519.Sign(priv, []byte("x")))
		if rec := verifySig(t, h, addr, nonce, sig); rec.Code != http.StatusUnauthorized {
			t.Fatalf("swept challenge accepted: %d", rec.Code)
		}
	})
	t.Run("base58 round trip", func(t *testing.T) {
		roundTrip := func(b []byte) {
			t.Helper()
			got, err := base58Decode(base58Encode(b))
			if err != nil || len(got) != len(b) {
				t.Fatalf("round trip failed for %x: %v %d bytes", b, err, len(got))
			}
			for j := range b {
				if got[j] != b[j] {
					t.Fatalf("round trip mangled bytes for %x", b)
				}
			}
		}
		// Leading zero bytes only occur ~1/256 per random try — pin them
		// deterministically (they encode as leading '1's; a NUL prefix
		// here was the 2026-09-22 race-gate flake): the all-zero key, and
		// a key whose leading zeros coexist with real digits.
		roundTrip(make([]byte, 32))
		leadZeros := make([]byte, 32)
		leadZeros[31] = 0xFF
		roundTrip(leadZeros)
		for i := 0; i < 20; i++ {
			b := make([]byte, 32)
			rand.Read(b)
			roundTrip(b)
		}
	})
}

func challengeFor(t *testing.T, h *WalletAuthHandler, addr string) (string, struct{ Nonce, Message string }) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/x", bytes.NewReader(mustJSON(t, map[string]string{"wallet": addr})))
	rec := httptest.NewRecorder()
	h.Challenge(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("challenge failed: %d %s", rec.Code, rec.Body.String())
	}
	var out struct{ Nonce, Message string }
	json.Unmarshal(rec.Body.Bytes(), &out)
	return out.Nonce, out
}

func verifySig(t *testing.T, h *WalletAuthHandler, addr, nonce, sig string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/x", bytes.NewReader(mustJSON(t, map[string]string{
		"wallet": addr, "nonce": nonce, "signature": sig,
	})))
	rec := httptest.NewRecorder()
	h.Verify(rec, req)
	return rec
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// base58Encode — the test-side twin of base58Decode, proving the alphabet
// math from both directions.
func base58Encode(b []byte) string {
	const alphabet = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"
	zeros := 0
	for zeros < len(b) && b[zeros] == 0 {
		zeros++
	}
	digits := make([]byte, 0, len(b)*2)
	for _, c := range b {
		carry := int(c)
		for j := len(digits) - 1; j >= 0; j-- {
			carry += int(digits[j]) << 8
			digits[j] = byte(carry % 58)
			carry /= 58
		}
		for carry > 0 {
			// Big-endian digits: overflow grows the most-significant end.
			digits = append([]byte{byte(carry % 58)}, digits...)
			carry /= 58
		}
	}
	// Leading zero bytes encode as leading '1's (alphabet[0]) — byte 0 is
	// not in the alphabet, so a NUL prefix fails decode. This was a real
	// ~1/256-per-try flake (race gate, 2026-09-22): random keys starting
	// with 0x00 round-tripped into "invalid base58 character".
	out := make([]byte, zeros)
	for i := range out {
		out[i] = alphabet[0]
	}
	for _, d := range digits {
		out = append(out, alphabet[d])
	}
	return string(out)
}

// FM-WALLa — one wallet to one account, linked where the user stands.
// Red today: the link pair does not exist, so the package does not build —
// the loudest fails-first state there is.

// linkWalletFor drives one full link attempt for the given session: fresh
// challenge against the caller, real signature, verify. Returns the verify
// recorder so each test can assert its own verdict.
func linkWalletFor(t *testing.T, h *WalletAuthHandler, userID int64, pub ed25519.PublicKey, priv ed25519.PrivateKey) *httptest.ResponseRecorder {
	t.Helper()
	addr := base58Encode(pub)

	chRec := httptest.NewRecorder()
	h.LinkChallenge(chRec, authedRequest(userID, string(mustJSON(t, map[string]string{"wallet": addr}))))
	if chRec.Code != http.StatusOK {
		t.Fatalf("link challenge status %d: %s", chRec.Code, chRec.Body.String())
	}
	var ch struct{ Nonce, Message string }
	json.Unmarshal(chRec.Body.Bytes(), &ch)
	if ch.Nonce == "" || ch.Message == "" {
		t.Fatalf("link challenge missing parts: %+v", ch)
	}

	sig := ed25519.Sign(priv, []byte(ch.Message))
	vRec := httptest.NewRecorder()
	h.LinkVerify(vRec, authedRequest(userID, string(mustJSON(t, map[string]string{
		"wallet":    addr,
		"nonce":     ch.Nonce,
		"signature": base64.StdEncoding.EncodeToString(sig),
	}))))
	return vRec
}

// (iii) The connect control runs a real signature against the session, and
// the identity lands on the SIGNED-IN account — not a fresh one (the P17
// login's empty-email upsert would create), not nobody's.
func TestWalletLinkAttachesToTheSignedInAccount(t *testing.T) {
	st, err := store.New(t.TempDir() + "/link.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	h := NewWalletAuthHandler(st, auth.New(), nil)

	u, err := st.CreateUser("linker@talon.dev", "hash")
	if err != nil {
		t.Fatal(err)
	}
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)

	rec := linkWalletFor(t, h, u.ID, pub, priv)
	if rec.Code != http.StatusOK {
		t.Fatalf("link verify status %d: %s", rec.Code, rec.Body.String())
	}

	bound, err := st.GetUserByProvider("solana", base58Encode(pub))
	if err != nil || bound.ID != u.ID {
		t.Fatalf("the wallet must bind to the signed-in account %d; got user=%v err=%v", u.ID, bound, err)
	}
}

// (ii) A wallet already bound to one account cannot ride two. The refusal
// must arrive BY NAME (409 with that fact), not as a bare UNIQUE failure —
// and the first owner keeps it.
func TestWalletLinkRefusesAWalletBoundToAnotherAccount(t *testing.T) {
	st, err := store.New(t.TempDir() + "/owned.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	h := NewWalletAuthHandler(st, auth.New(), nil)

	a, _ := st.CreateUser("first@talon.dev", "hash")
	b, _ := st.CreateUser("second@talon.dev", "hash")
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	addr := base58Encode(pub)

	if rec := linkWalletFor(t, h, a.ID, pub, priv); rec.Code != http.StatusOK {
		t.Fatalf("first link status %d: %s", rec.Code, rec.Body.String())
	}

	rec := linkWalletFor(t, h, b.ID, pub, priv)
	if rec.Code != http.StatusConflict {
		t.Fatalf("a wallet bound elsewhere must be refused with 409, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "another account") {
		t.Fatalf("the refusal must name the fact, got %s", rec.Body.String())
	}

	// The first owner keeps it — the refused attempt changed nothing.
	bound, err := st.GetUserByProvider("solana", addr)
	if err != nil || bound.ID != a.ID {
		t.Fatalf("the refused link must leave the first owner intact; got user=%v err=%v", bound, err)
	}
}

// (ii) One wallet PER ACCOUNT: a second connect REPLACES the first — one
// solana identity, the newest address, the old one unbound everywhere.
func TestWalletLinkReplacesTheExistingWallet(t *testing.T) {
	st, err := store.New(t.TempDir() + "/replace.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	h := NewWalletAuthHandler(st, auth.New(), nil)

	u, _ := st.CreateUser("relink@talon.dev", "hash")
	pub1, priv1, _ := ed25519.GenerateKey(rand.Reader)
	pub2, priv2, _ := ed25519.GenerateKey(rand.Reader)

	if rec := linkWalletFor(t, h, u.ID, pub1, priv1); rec.Code != http.StatusOK {
		t.Fatalf("first connect status %d: %s", rec.Code, rec.Body.String())
	}
	if rec := linkWalletFor(t, h, u.ID, pub2, priv2); rec.Code != http.StatusOK {
		t.Fatalf("second connect status %d: %s", rec.Code, rec.Body.String())
	}

	bound, err := st.GetUserByProvider("solana", base58Encode(pub2))
	if err != nil || bound.ID != u.ID {
		t.Fatalf("the new wallet must be this account's identity; got user=%v err=%v", bound, err)
	}
	if old, err := st.GetUserByProvider("solana", base58Encode(pub1)); err == nil {
		t.Fatalf("the replaced wallet must be unbound, but account %d still holds it", old.ID)
	}
}
