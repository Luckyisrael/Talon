package api

import (
	"encoding/json"
	"log"
	"net/http"
	"strings"

	"github.com/luckyisrael/talon/backend/internal/skr"
	"github.com/luckyisrael/talon/backend/internal/store"
)

// SkrHandler owns the two SKR endpoints: quoting what a pack costs, and
// re-deriving a payment from the chain before the ledger is touched.
//
// The division of labour is deliberate. The verifier holds the deployment
// facts (endpoint, mint, treasury) and knows nothing about users; the handler
// holds the user and knows nothing about the chain. The only place the two
// meet is here, after a verdict has already been returned — so neither a
// request nor a handler can settle what counts as a payment.
type SkrHandler struct {
	store    *store.Store
	verifier *skr.Verifier
}

// NewSkrHandler wires the ledger to the chain verifier.
func NewSkrHandler(s *store.Store, v *skr.Verifier) *SkrHandler {
	return &SkrHandler{store: s, verifier: v}
}

// Quote is POST /v1/skr/quote. It states the price, the mint and the treasury
// the client must pay — all of it server-side, because a price the client
// names is a price the client chooses.
func (h *SkrHandler) Quote(w http.ResponseWriter, r *http.Request) {
	cfg := h.verifier.Config()
	spare, err := h.store.SpareCredits(GetUserID(r.Context()))
	if err != nil {
		log.Printf("api: spare credits read failed: %v", err)
		writeError(w, http.StatusInternalServerError, "could not read your export credits")
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"skr_amount":    cfg.Price,
		"credits":       cfg.Credits,
		"mint":          cfg.Mint,
		"treasury":      cfg.Treasury,
		"spare_credits": spare,
	})
}

// Verify is POST /v1/skr/verify, body {"signature": "..."}.
//
// Answers:
//
//	200 verified   the chain showed the payment; credits granted (or already held)
//	200 pending    the transaction has not landed yet — ask again shortly
//	422 rejected   the transaction exists and is definitively not this payment
//	400            the input was not a signature at all
//	502            the configured oracle could not be reached — fail closed
//
// A completed verdict is never an error: "we looked, here is what we found"
// lets the client branch on `reason` instead of parsing prose, while 422 keeps
// a proven non-payment out of the success path.
func (h *SkrHandler) Verify(w http.ResponseWriter, r *http.Request) {
	userID := GetUserID(r.Context())

	var req struct {
		Signature string `json:"signature"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "could not read the request body")
		return
	}
	req.Signature = strings.TrimSpace(req.Signature)
	// Refused before the oracle is consulted, so junk never appears on the
	// RPC's wire at all.
	if !skr.ValidSignature(req.Signature) {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"error":    "that is not a transaction signature",
			"code":     "bad_signature",
			"verified": false,
		})
		return
	}

	verdict, err := h.verifier.Verify(r.Context(), req.Signature)
	if err != nil {
		// Fail closed: an oracle we cannot reach proves nothing, and proving
		// nothing must not pay.
		writeJSON(w, http.StatusBadGateway, map[string]interface{}{
			"error":    "could not reach the configured Solana RPC",
			"code":     "rpc_unavailable",
			"verified": false,
			"reason":   "unavailable",
		})
		return
	}

	if verdict.Status != skr.StatusVerified {
		spare, err := h.store.SpareCredits(userID)
		if err != nil {
			log.Printf("api: spare credits read failed after chain verdict: %v", err)
			writeError(w, http.StatusInternalServerError, "could not read your export credits")
			return
		}
		status := http.StatusOK
		if verdict.Status == skr.StatusRejected {
			status = http.StatusUnprocessableEntity
		}
		writeJSON(w, status, map[string]interface{}{
			"verified":      false,
			"reason":        verdict.Reason,
			"credits_added": 0,
			"spare_credits": spare,
		})
		return
	}

	// The ledger's UNIQUE tx_signature is what makes a replay a no-op: a
	// second verify of the same signature reports the balance the owner
	// already holds rather than an error the client would keep retrying.
	cfg := h.verifier.Config()
	added := 0
	granted, err := h.store.GrantCredits(userID, cfg.Credits, "skr", req.Signature)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not record your credits")
		return
	}
	if granted {
		added = cfg.Credits
	}
	spare, err := h.store.SpareCredits(userID)
	if err != nil {
		log.Printf("api: spare credits read failed after grant: %v", err)
		writeError(w, http.StatusInternalServerError, "could not read your export credits")
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"verified":      true,
		"reason":        verdict.Reason,
		"credits_added": added,
		"spare_credits": spare,
	})
}
