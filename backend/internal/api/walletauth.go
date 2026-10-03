package api

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/luckyisrael/talon/backend/internal/auth"
	"github.com/luckyisrael/talon/backend/internal/notify"
	"github.com/luckyisrael/talon/backend/internal/store"
)

// WalletAuthHandler — P17: sign in with the wallet the product already
// assumes its users have. A Solana wallet owns an ed25519 keypair; the
// backend proves ownership the only way that means anything on-chain:
// issue a nonce-bearing challenge, verify the wallet's signature over
// its exact bytes, and mint the same JWT the password and OAuth paths
// mint. Account linking follows UpsertOAuthUser's law — a wallet is a
// provider identity, (provider='solana', provider_id=pubkey), linked to
// the existing account when one exists and creating one when none does.
type WalletAuthHandler struct {
	store    *store.Store
	auth     *auth.Service
	notifier notify.Notifier
}

func NewWalletAuthHandler(s *store.Store, svc *auth.Service, n notify.Notifier) *WalletAuthHandler {
	if n == nil {
		n = notify.Noop{}
	}
	return &WalletAuthHandler{store: s, auth: svc, notifier: n}
}

// Challenge issues the message the wallet must sign. Public: the whole
// point is that the caller has no credentials yet.
func (h *WalletAuthHandler) Challenge(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Wallet string `json:"wallet"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Wallet == "" {
		writeError(w, http.StatusBadRequest, "wallet address required")
		return
	}

	// The message names the act, the site, and the one-time nonce — a
	// signature over it cannot be replayed anywhere else or twice here.
	nonceBytes := make([]byte, 16)
	if _, err := rand.Read(nonceBytes); err != nil {
		writeError(w, http.StatusInternalServerError, "could not mint challenge")
		return
	}
	nonce := hex.EncodeToString(nonceBytes)
	message := "Talon sign-in\nwallet: " + req.Wallet + "\nnonce: " + nonce + "\n\nSigning proves you own this wallet. This request will not trigger any blockchain transaction."

	if err := h.store.StoreWalletChallenge(nonce, message); err != nil {
		writeError(w, http.StatusInternalServerError, "could not store challenge")
		return
	}
	// Opportunistic sweep — abandoned QR screens shouldn't pile rows.
	if n, err := h.store.SweepWalletChallenges(10 * time.Minute); err == nil && n > 0 {
		// Logged by the store layer if it cares; not user-visible.
		_ = n
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"nonce":   nonce,
		"message": message,
	})
}

// Verify exchanges the signed challenge for the session JWT. The
// signature must be over the EXACT challenge bytes the nonce names, by
// the keypair the claimed pubkey represents. Anything else is 401 —
// and the challenge is consumed either way (the read deletes), so a
// failed attempt cannot be retried with a corrected guess.
func (h *WalletAuthHandler) Verify(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Wallet    string `json:"wallet"`
		Nonce     string `json:"nonce"`
		Signature string `json:"signature"` // base64, as Phantom/Backpack signMessage return
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil ||
		req.Wallet == "" || req.Nonce == "" || req.Signature == "" {
		writeError(w, http.StatusBadRequest, "wallet, nonce, and signature required")
		return
	}

	message, err := h.store.ConsumeWalletChallenge(req.Nonce)
	if err != nil {
		// Unknown, used, or expired — identical answers, on purpose.
		writeError(w, http.StatusUnauthorized, "challenge not found or expired")
		return
	}
	// The challenge was minted for a wallet; the presenter must present
	// as the same one. A challenge can't be signed over and claimed by a
	// different address.
	if !strings.Contains(message, "wallet: "+req.Wallet+"\n") {
		writeError(w, http.StatusUnauthorized, "challenge was issued for a different wallet")
		return
	}

	pub, err := base58Decode(req.Wallet)
	if err != nil || len(pub) != ed25519.PublicKeySize {
		writeError(w, http.StatusBadRequest, "invalid wallet address")
		return
	}
	sig, err := base64.StdEncoding.DecodeString(req.Signature)
	if err != nil {
		writeError(w, http.StatusBadRequest, "signature must be base64")
		return
	}
	if !ed25519.Verify(ed25519.PublicKey(pub), []byte(message), sig) {
		writeError(w, http.StatusUnauthorized, "signature does not verify")
		return
	}

	// Verified owner. The account-linking law of UpsertOAuthUser, with the
	// wallet as the identity: bind if bound by address, else create fresh.
	// A wallet presents no email (none "until they add one"), and an absent
	// email links nothing — two wallets are two accounts (FM-IDENTa).
	user, err := h.store.UpsertOAuthUser("solana", req.Wallet, "", "", "")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not link wallet account")
		return
	}

	token, refresh, _, err := issueSession(h.store, h.auth, user.ID, user.Email, deviceLabel(r))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not mint session")
		return
	}
	notify.Dispatch(h.store, h.notifier, user.ID, notify.KindSignIn,
		"New sign-in", "A new session started on your account. If this was not you, sign out everywhere from Profile.")
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"token":         token,
		"refresh_token": refresh,
		"user": map[string]interface{}{
			"id":           user.ID,
			"email":        user.Email,
			"display_name": user.DisplayName,
		},
	})
}

// base58Decode — Solana addresses are base58. Minimal alphabet decode;
// sufficient for the 32-byte public keys the address format carries.
func base58Decode(s string) ([]byte, error) {
	const alphabet = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"
	idx := make([]byte, 256)
	for i := range idx {
		idx[i] = 0xFF
	}
	for i := 0; i < len(alphabet); i++ {
		idx[alphabet[i]] = byte(i)
	}

	out := make([]byte, 0, 25)
	for _, c := range []byte(s) {
		if idx[c] == 0xFF {
			return nil, errBase58Char
		}
		carry := int64(idx[c])
		for j := len(out) - 1; j >= 0; j-- {
			carry += int64(out[j]) * 58
			out[j] = byte(carry & 0xFF)
			carry >>= 8
		}
		for carry > 0 {
			out = append([]byte{byte(carry & 0xFF)}, out...)
			carry >>= 8
		}
	}
	// Leading '1's are leading zero bytes.
	ones := 0
	for ones < len(s) && s[ones] == '1' {
		ones++
	}
	if ones > 0 {
		out = append(make([]byte, ones), out...)
	}
	return out, nil
}

type base58Error string

func (e base58Error) Error() string { return string(e) }

const errBase58Char = base58Error("invalid base58 character")
