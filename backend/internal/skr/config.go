// Package skr verifies SKR payments that unlock release-export credits.
//
// Nothing above this package decides a payment for itself. The API asks the
// chain and does what the chain says, because a signature is only a claim
// about the chain made by the party that wants the money.
//
// Three deployment facts come from the environment and are unreachable from
// any request — the RPC endpoint, the mint and the treasury. A caller who
// could choose them would be choosing the oracle that judges their own
// payment, so they are read once at construction and never re-read.
package skr

import (
	"os"
	"strconv"
	"strings"
	"time"
)

// Defaults for one credit pack. They live here rather than in the API layer
// so the price the paywall quotes and the price the chain is asked to confirm
// cannot drift apart: quota.go's three disagreeing limit copies are the
// cautionary tale for splitting one number across two packages.
const (
	DefaultPrice   = 100
	DefaultCredits = 5
)

// TokenProgram is the SPL token program a real SKR mint must be owned by.
const TokenProgram = "TokenkegQfeZyiNwAJbNbGKPFXCWuBvf9Ss623VQ5DA"

// b58Alphabet is Bitcoin/Solana base58 — no 0, O, I or l.
const b58Alphabet = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"

// Price is the whole-SKR cost of one pack, from SKR_PRICE_AMOUNT.
func Price() int { return envInt("SKR_PRICE_AMOUNT", DefaultPrice) }

// Credits is how many exports one pack unlocks, from SKR_PRICE_CREDITS.
func Credits() int { return envInt("SKR_PRICE_CREDITS", DefaultCredits) }

// envInt reads a positive integer or falls back. Zero and negative are never
// meaningful prices, so they are treated as unset rather than trusted.
func envInt(key string, fallback int) int {
	if n, err := strconv.Atoi(strings.TrimSpace(os.Getenv(key))); err == nil && n > 0 {
		return n
	}
	return fallback
}

// Config is the operator's deployment facts. Price and Credits are the offer;
// Mint, Treasury and RPCURL are the oracle. Decimals is a cross-check only —
// verification always takes its exponent from the transaction it is judging,
// so a mistyped SKR_DECIMALS cannot make a pack unbuyable or half-priced.
type Config struct {
	RPCURL   string
	Mint     string
	Treasury string
	Price    int
	Credits  int
	Decimals int
	Timeout  time.Duration
}

// ConfigFromEnv builds the config the server runs on. It is the only place
// the SKR_* names are read.
func ConfigFromEnv() Config {
	return Config{
		RPCURL:   strings.TrimSpace(os.Getenv("SKR_RPC_URL")),
		Mint:     strings.TrimSpace(os.Getenv("SKR_MINT")),
		Treasury: strings.TrimSpace(os.Getenv("SKR_TREASURY")),
		Price:    Price(),
		Credits:  Credits(),
		Decimals: envInt("SKR_DECIMALS", 0),
		Timeout:  8 * time.Second,
	}
}

// ValidSignature is the shape check that keeps junk off the oracle's wire. A
// Solana signature is 64 bytes, which is 87–88 base58 characters; the range is
// generous because a truncated or over-long paste should be refused before it
// is embedded in a request, not after. It is only a shape check — the
// authoritative test is whether the chain returns a transaction for it.
func ValidSignature(signature string) bool {
	sig := strings.TrimSpace(signature)
	if len(sig) < 80 || len(sig) > 100 {
		return false
	}
	for _, r := range sig {
		if !strings.ContainsRune(b58Alphabet, r) {
			return false
		}
	}
	return true
}
