package api

// FM-SKRa — a signature is not a payment. Every case below drives the real
// handler and the real ledger against a stub chain that speaks Solana's
// `getTransaction` JSON-RPC over HTTP, so what is asserted is the verdict the
// server derives for itself — never a claim the request carries.
//
// The seven cases are the failure modes written down before any source:
// the price must be server-stated; only the transfer we can actually prove
// grants; a real signature of the *wrong* transfer must not; a transaction
// that has not landed is not the same as a bad one; one signature grants
// once; junk never reaches the chain; and the exponent comes from the chain.

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/luckyisrael/talon/backend/internal/skr"
	"github.com/luckyisrael/talon/backend/internal/store"
)

const (
	skrMint     = "SKRmint111111111111111111111111111111111111"
	skrTreasury = "SKRtreasury111111111111111111111111111111111"
	skrSender   = "SKRsender1111111111111111111111111111111111"

	// A 64-byte signature is 87–88 base58 characters; '1' is in the alphabet,
	// so this passes a shape check while still being a fixture, not a real
	// transaction.
	skrGoodSig = "111111111111111111111111111111111111111111111111111111111111111111111111111111111111"
)

// stubRPC is a stand-in for the configured Solana endpoint. It answers
// `getTransaction` from a per-signature table — an absent signature returns
// `null`, exactly as a transaction that has not landed yet does — and counts
// every call so a test can prove junk never reached it.
type stubRPC struct {
	url     string
	calls   int
	mu      sync.Mutex
	results map[string]any
}

func newStubRPC(t *testing.T) *stubRPC {
	t.Helper()
	s := &stubRPC{results: map[string]any{}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		var req struct {
			ID     any               `json:"id"`
			Method string            `json:"method"`
			Params []json.RawMessage `json:"params"`
		}
		_ = json.Unmarshal(body, &req)

		s.mu.Lock()
		s.calls++
		var result any
		if req.Method == "getTransaction" && len(req.Params) > 0 {
			var sig string
			if err := json.Unmarshal(req.Params[0], &sig); err == nil {
				if v, ok := s.results[sig]; ok {
					result = v
				}
			}
		}
		s.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0",
			"id":      req.ID,
			"result":  result,
		})
	}))
	t.Cleanup(srv.Close)
	s.url = srv.URL
	return s
}

func (s *stubRPC) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

// skrHarness is an owner, a real store, and a handler wired to a stub chain.
// Config.Decimals is deliberately wrong (9 against the chain's 6): the
// exponent has to come from the transaction being verified, never from config,
// or a typo'd SKR_DECIMALS silently makes the pack unbuyable or half-priced.
func skrHarness(t *testing.T) (*SkrHandler, *stubRPC, int64) {
	t.Helper()
	st, err := store.New(filepath.Join(t.TempDir(), "skr.db"))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	u, err := st.CreateUser("skr@test.com", "hash")
	if err != nil {
		t.Fatal(err)
	}
	stub := newStubRPC(t)
	v := skr.New(skr.Config{
		RPCURL:   stub.url,
		Mint:     skrMint,
		Treasury: skrTreasury,
		Price:    100,
		Credits:  5,
		Decimals: 9,
		Timeout:  3 * time.Second,
	})
	return NewSkrHandler(st, v), stub, u.ID
}

func tokenBal(idx int, mint, owner, amount string, decimals int) map[string]any {
	return map[string]any{
		"accountIndex": idx,
		"mint":         mint,
		"owner":        owner,
		"programId":    "TokenkegQfeZyiNwAJbNbGKPFXCWuBvf9Ss623VQ5DA",
		"uiTokenAmount": map[string]any{
			"amount":         amount,
			"decimals":       decimals,
			"uiAmount":       nil,
			"uiAmountString": amount,
		},
	}
}

// transferTX is a `getTransaction` result crediting `amount` raw units of
// `mint` to a token account owned by `owner`.
func transferTX(mint, owner, amount string, decimals int, failed bool) map[string]any {
	var errVal any
	if failed {
		errVal = map[string]any{"InstructionError": []any{0, "Custom"}}
	}
	return map[string]any{
		"slot":      1000,
		"blockTime": 1700000000,
		"transaction": map[string]any{
			"signatures": []string{"sig"},
			"message":    map[string]any{"accountKeys": []any{}, "instructions": []any{}},
		},
		"meta": map[string]any{
			"err":               errVal,
			"fee":               5000,
			"preTokenBalances":  []any{tokenBal(0, mint, owner, "0", decimals)},
			"postTokenBalances": []any{tokenBal(0, mint, owner, amount, decimals)},
			"preBalances":       []any{1000000},
			"postBalances":      []any{995000},
		},
	}
}

type verifyResponse struct {
	Verified     bool   `json:"verified"`
	Reason       string `json:"reason"`
	CreditsAdded int    `json:"credits_added"`
	SpareCredits int    `json:"spare_credits"`
}

func skrVerify(t *testing.T, h *SkrHandler, userID int64, signature string) (*httptest.ResponseRecorder, verifyResponse) {
	t.Helper()
	body, err := json.Marshal(map[string]string{"signature": signature})
	if err != nil {
		t.Fatal(err)
	}
	req := authedRequest(userID, string(body))
	rec := httptest.NewRecorder()
	h.Verify(rec, req)
	var out verifyResponse
	if rec.Code == http.StatusOK || rec.Code == http.StatusUnprocessableEntity {
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("verify body must be machine-readable: %v (%s)", err, rec.Body.String())
		}
	}
	return rec, out
}

// FM-SKRa — the price is server-stated. If the pack price could come from the
// request, the client would decide what it owes.
func TestSkrQuoteStatesThePriceThePackCosts(t *testing.T) {
	h, _, userID := skrHarness(t)

	req := authedRequest(userID, "")
	rec := httptest.NewRecorder()
	h.Quote(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("quote: status %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	var quote struct {
		SKRAmount    int    `json:"skr_amount"`
		Credits      int    `json:"credits"`
		Mint         string `json:"mint"`
		Treasury     string `json:"treasury"`
		SpareCredits int    `json:"spare_credits"`
		CreditsPacks int    `json:"credits_per_pack"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &quote); err != nil {
		t.Fatalf("quote must be machine-readable: %v (%s)", err, rec.Body.String())
	}
	if quote.SKRAmount != 100 || quote.Credits != 5 {
		t.Errorf("quote = %d SKR for %d credits, want 100 for 5", quote.SKRAmount, quote.Credits)
	}
	if quote.Mint != skrMint || quote.Treasury != skrTreasury {
		t.Errorf("quote names mint %q / treasury %q, want the configured ones", quote.Mint, quote.Treasury)
	}
	if quote.SpareCredits != 0 {
		t.Errorf("new owner's balance = %d, want 0", quote.SpareCredits)
	}
}

// FM-SKRa(i)/(ii) — the server grants only what it re-derived from its own
// `getTransaction`: a succeeded transaction crediting the exact pack price in
// the configured mint to the configured treasury.
func TestSkrVerifyGrantsOnlyWhatTheChainShows(t *testing.T) {
	h, stub, userID := skrHarness(t)
	stub.results[skrGoodSig] = transferTX(skrMint, skrTreasury, "100000000", 6, false)

	rec, out := skrVerify(t, h, userID, skrGoodSig)
	if rec.Code != http.StatusOK {
		t.Fatalf("verified payment: status %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	if !out.Verified {
		t.Fatalf("chain shows a correct transfer but verify said %q", out.Reason)
	}
	if out.CreditsAdded != 5 || out.SpareCredits != 5 {
		t.Errorf("credits_added = %d, spare = %d; want 5 and 5", out.CreditsAdded, out.SpareCredits)
	}
	if stub.callCount() == 0 {
		t.Error("the verdict was never taken to the chain")
	}
}

// FM-SKRa(ii) — a real, successful signature of the *wrong* transfer must not
// grant. Each row is a distinct way to be wrong: too little paid, paid in a
// look-alike token, paid to somebody else, or failed on-chain while still
// returning a transaction.
func TestSkrVerifyRefusesAPaymentItCannotProve(t *testing.T) {
	cases := []struct {
		name   string
		tx     map[string]any
		reason string
	}{
		{"too little paid", transferTX(skrMint, skrTreasury, "99000000", 6, false), "wrong_amount"},
		{"look-alike mint", transferTX("FAKEmint11111111111111111111111111111111111", skrTreasury, "100000000", 6, false), "wrong_mint"},
		{"paid to someone else", transferTX(skrMint, skrSender, "100000000", 6, false), "wrong_recipient"},
		{"failed on chain", transferTX(skrMint, skrTreasury, "100000000", 6, true), "tx_failed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, stub, userID := skrHarness(t)
			stub.results[skrGoodSig] = tc.tx

			rec, out := skrVerify(t, h, userID, skrGoodSig)
			if out.Verified {
				t.Fatalf("%s was accepted as payment (status %d)", tc.name, rec.Code)
			}
			if out.Reason != tc.reason {
				t.Errorf("reason = %q, want %q", out.Reason, tc.reason)
			}
			if out.CreditsAdded != 0 || out.SpareCredits != 0 {
				t.Errorf("a refused transfer moved the balance: added %d, spare %d", out.CreditsAdded, out.SpareCredits)
			}
			if rec.Code != http.StatusUnprocessableEntity {
				t.Errorf("status %d, want %d — a definitive non-payment is not a success", rec.Code, http.StatusUnprocessableEntity)
			}
		})
	}
}

// FM-SKRa(v) — a transaction that has not landed is not a bad one. Collapsing
// both into "unverified" teaches the client to abandon a payment that is
// still confirming, so pending answers 200 and retries, while a proven
// non-payment answers 422 and does not.
func TestSkrVerifyDistinguishesPendingFromUnpayable(t *testing.T) {
	h, stub, userID := skrHarness(t)

	rec, out := skrVerify(t, h, userID, skrGoodSig)
	if rec.Code != http.StatusOK {
		t.Fatalf("an unlanded transaction answered %d, want 200 so the client retries", rec.Code)
	}
	if out.Verified || out.Reason != "pending" {
		t.Errorf("verified=%v reason=%q, want false/pending", out.Verified, out.Reason)
	}
	if out.CreditsAdded != 0 || out.SpareCredits != 0 {
		t.Errorf("pending must not grant: added %d, spare %d", out.CreditsAdded, out.SpareCredits)
	}

	// The same signature once it lands is the ordinary success path — the
	// client's retry has to be able to succeed.
	stub.results[skrGoodSig] = transferTX(skrMint, skrTreasury, "100000000", 6, false)
	_, out = skrVerify(t, h, userID, skrGoodSig)
	if !out.Verified || out.SpareCredits != 5 {
		t.Errorf("retry after landing: verified=%v spare=%d, want true/5", out.Verified, out.SpareCredits)
	}
}

// FM-SKRa(iv) — one signature grants once. `tx_signature UNIQUE` is the
// backstop; the handler must answer the duplicate with the balance the owner
// already holds rather than an error the client would keep retrying.
func TestSkrVerifyIsIdempotentOnReplay(t *testing.T) {
	h, stub, userID := skrHarness(t)
	stub.results[skrGoodSig] = transferTX(skrMint, skrTreasury, "100000000", 6, false)

	rec, first := skrVerify(t, h, userID, skrGoodSig)
	if rec.Code != http.StatusOK || !first.Verified {
		t.Fatalf("first verify: status %d verified %v (%s)", rec.Code, first.Verified, rec.Body.String())
	}
	if first.CreditsAdded != 5 || first.SpareCredits != 5 {
		t.Fatalf("first grant: added %d spare %d, want 5 and 5", first.CreditsAdded, first.SpareCredits)
	}

	rec, second := skrVerify(t, h, userID, skrGoodSig)
	if rec.Code != http.StatusOK || !second.Verified {
		t.Fatalf("replay must still read as verified, not as an error: status %d verified %v reason %q",
			rec.Code, second.Verified, second.Reason)
	}
	if second.CreditsAdded != 0 {
		t.Errorf("replay added %d credits — one signature bought the pack twice", second.CreditsAdded)
	}
	if second.SpareCredits != 5 {
		t.Errorf("balance after replay = %d, want 5", second.SpareCredits)
	}
}

// FM-SKRa(i)/(vi) — junk is rejected before it can be put on the oracle's
// wire. The assertion is on the chain's own call count: zero.
func TestSkrVerifyRejectsAMalformedSignatureBeforeCallingTheChain(t *testing.T) {
	h, stub, userID := skrHarness(t)

	rec, out := skrVerify(t, h, userID, "not a base58 signature!!")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("malformed signature: status %d, want 400 (body %s)", rec.Code, rec.Body.String())
	}
	if out.Verified {
		t.Error("a malformed signature was taken as a payment")
	}
	if stub.callCount() != 0 {
		t.Errorf("the chain was called %d times with a signature that is not base58", stub.callCount())
	}
}

// FM-SKRa(iii) — the exponent comes from the chain. Config says 9 decimals,
// the chain says 6, and a transfer of 100 SKR at 6 decimals must still be
// recognised; using the configured value would expect 100000000000 and the
// pack would be permanently unbuyable while looking correctly priced.
func TestSkrVerificationUsesTheChainsOwnDecimals(t *testing.T) {
	h, stub, userID := skrHarness(t)
	stub.results[skrGoodSig] = transferTX(skrMint, skrTreasury, "100000000", 6, false)

	_, out := skrVerify(t, h, userID, skrGoodSig)
	if !out.Verified {
		t.Fatalf("100 SKR at the chain's 6 decimals was rejected: reason %q", out.Reason)
	}
	if out.SpareCredits != 5 {
		t.Errorf("spare = %d, want 5", out.SpareCredits)
	}
	if !strings.HasPrefix(out.Reason, "verified") && out.Reason != "" {
		t.Errorf("reason = %q on a successful verification", out.Reason)
	}
}
