// Package skr verifies the payment that unlocks release-export credits.
//
// Two rails exist. SKR (an SPL token) is the production rail. When SKR is
// unconfigured the paywall serves a devnet-SOL test rail instead — a plain
// System transfer of devnet SOL to a server-owned treasury — so judges can
// exercise the whole export flow without any token deployment. The rail is
// chosen once, from the environment, and never from a request.
//
// Nothing above this package decides a payment for itself. The API asks the
// chain and does what the chain says, because a signature is only a claim
// about the chain made by the party that wants the money.
//
// Three deployment facts come from the environment and are unreachable from
// any request — the RPC endpoint, the mint (or rail) and the treasury. A
// caller who could choose them would be choosing the oracle that judges their
// own payment, so they are read once at construction and never re-read.
package skr

import (
	"crypto/ed25519"
	"crypto/rand"
	"math/big"
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

// Mode is which rail pays for export credits. The zero value is SKR: every
// verifier built directly (tests, harnesses) verifies token payments exactly
// as before, and only ConfigFromEnv can select the devnet-SOL test rail.
type Mode string

const (
	ModeSKR       Mode = "skr"
	ModeDevnetSol Mode = "devnet-sol"
)

// SolDecimals is SOL's fixed exponent on every cluster — a chain constant
// like TokenProgram, not configuration. (SKR's decimals are deliberately NOT
// a constant: they come from the mint being judged.)
const SolDecimals = 9

// DefaultSolLamports is 0.1 SOL — the devnet-SOL pack price. Devnet SOL has
// no value, so the number exists to be visible and to clear the fee floor,
// not to be thrifty. EXPORT_SOL_LAMPORTS overrides it.
const DefaultSolLamports = 100_000_000

// DevnetRPC is the fallback oracle for the test rail: the public devnet
// endpoint. Operators may point SKR_RPC_URL at their own node; a caller of
// the API can never choose it.
const DevnetRPC = "https://api.devnet.solana.com"

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

	// Mode is the rail: SKR token (production) or devnet SOL (test).
	Mode Mode
	// SolLamports is the devnet-SOL pack price, already in base units, so
	// the client never multiplies a decimal it could get wrong.
	SolLamports uint64
}

// ConfigFromEnv builds the config the server runs on. It is the only place
// the SKR_* and EXPORT_SOL_* names are read. SKR_MINT set means the SKR
// rail; unset means the devnet-SOL test rail — dark-until-configured, the
// same law every other integration here follows, inverted: absence of config
// ADDS the free rail instead of disabling the feature.
func ConfigFromEnv() Config {
	cfg := Config{
		RPCURL:   strings.TrimSpace(os.Getenv("SKR_RPC_URL")),
		Mint:     strings.TrimSpace(os.Getenv("SKR_MINT")),
		Treasury: strings.TrimSpace(os.Getenv("SKR_TREASURY")),
		Price:    Price(),
		Credits:  Credits(),
		Decimals: envInt("SKR_DECIMALS", 0),
		Timeout:  8 * time.Second,
	}
	if cfg.Mint != "" {
		cfg.Mode = ModeSKR
		return cfg
	}
	cfg.Mode = ModeDevnetSol
	if cfg.RPCURL == "" {
		cfg.RPCURL = DevnetRPC
	}
	cfg.SolLamports = SolPackLamports()
	return cfg
}

// SolPackLamports is the devnet-SOL pack price in lamports
// (EXPORT_SOL_LAMPORTS, default 0.1 SOL). Zero and negative are never a
// price, so they are treated as unset rather than trusted — the same law
// envInt applies to the SKR price.
func SolPackLamports() uint64 {
	if n, err := strconv.ParseUint(strings.TrimSpace(os.Getenv("EXPORT_SOL_LAMPORTS")), 10, 64); err == nil && n > 0 {
		return n
	}
	return DefaultSolLamports
}

// DevnetSolActive reports whether the paywall is quoting the devnet-SOL test
// rail — the same decision ConfigFromEnv makes, from the same env. It exists
// for surfaces (the 402 refusal body) that need the rail without a verifier.
func DevnetSolActive() bool { return ConfigFromEnv().Mode == ModeDevnetSol }

// GenerateTreasury mints a fresh pubkey to receive devnet SOL. Only the
// PUBLIC half is kept: receiving needs no key, and a backend that could
// SPEND the treasury would be holding funds it only ever verifies. The
// caller persists the result — a treasury that vanished on restart would
// strand every payment already sent to it.
func GenerateTreasury() (string, error) {
	// GenerateKey returns (publicKey, privateKey, err) — PUBLIC first. Only
	// the public half is kept; the private key is discarded unused, because
	// receiving needs no key and a treasury the server could SPEND would be
	// holding funds it only ever verifies.
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", err
	}
	return base58Encode(pub), nil
}

// base58Encode is the Bitcoin/Solana base58 of b — no 0, O, I or l, which
// is why it inverts b58Alphabet rather than using encoding/hex or base32.
// Leading zero bytes encode as '1's, the convention every Solana address
// parser (including the app's) already expects.
func base58Encode(b []byte) string {
	zeros := 0
	for zeros < len(b) && b[zeros] == 0 {
		zeros++
	}
	n := new(big.Int).SetBytes(b)
	radix := big.NewInt(58)
	mod := new(big.Int)
	var out []byte
	for n.Sign() > 0 {
		n.DivMod(n, radix, mod)
		out = append(out, b58Alphabet[mod.Int64()])
	}
	for i := 0; i < zeros; i++ {
		out = append(out, b58Alphabet[0])
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return string(out)
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
