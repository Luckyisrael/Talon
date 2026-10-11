package skr

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"
	"time"
)

// Status is the three-way answer a verification can give. Splitting pending
// out from rejected is the difference between a client retrying a payment
// that is still confirming and abandoning one that will never be valid.
type Status string

const (
	StatusVerified Status = "verified"
	StatusPending  Status = "pending"
	StatusRejected Status = "rejected"
)

// ErrMalformed is what junk looks like: the caller passed something that is
// not a signature at all, so it never reached the chain.
var ErrMalformed = errors.New("that is not a transaction signature")

// Verdict is what the chain itself showed. Reason is stable and machine
// readable — a client branches on it instead of parsing prose.
type Verdict struct {
	Status   Status
	Reason   string
	Decimals int
	Expected *big.Int
}

// Verifier re-derives a payment from the configured cluster. It holds no
// mutable state, so one instance serves every request.
type Verifier struct {
	cfg    Config
	client *http.Client
}

// New builds a verifier over the given deployment facts.
func New(cfg Config) *Verifier {
	if cfg.Timeout <= 0 {
		cfg.Timeout = 8 * time.Second
	}
	return &Verifier{cfg: cfg, client: &http.Client{Timeout: cfg.Timeout}}
}

// Config exposes the deployment facts the API quotes back (the paywall has to
// tell the client where to pay) without exposing a way to change them.
func (v *Verifier) Config() Config { return v.cfg }

// Verify answers whether this signature is a payment of the pack price to our
// treasury, on the configured rail. It never takes the request's word for
// anything: the verdict is derived entirely from a getTransaction this method
// issues, and a signature that is not well-formed never reaches that call.
func (v *Verifier) Verify(ctx context.Context, signature string) (Verdict, error) {
	sig := strings.TrimSpace(signature)
	if !ValidSignature(sig) {
		return Verdict{}, ErrMalformed
	}
	if v.cfg.RPCURL == "" {
		return Verdict{}, errors.New("no Solana RPC configured (SKR_RPC_URL)")
	}
	if v.cfg.Mode == ModeDevnetSol {
		if v.cfg.Treasury == "" {
			return Verdict{}, errors.New("no devnet SOL treasury configured")
		}
	} else if v.cfg.Mint == "" || v.cfg.Treasury == "" {
		return Verdict{}, errors.New("SKR_MINT / SKR_TREASURY not configured")
	}

	tx, interim, err := v.fetchSettled(ctx, sig)
	if err != nil || interim != (Verdict{}) {
		return interim, err
	}
	if v.cfg.Mode == ModeDevnetSol {
		return v.solVerdict(tx), nil
	}
	return v.skrVerdict(tx), nil
}

// fetchSettled is the rail-independent head of every verification: it either
// returns a settled, successful transaction, or the interim verdict that
// explains why there isn't one (pending / incomplete / tx_failed). A failed
// transaction still RETURNS a transaction, so err is checked before anything
// about its contents is believed.
func (v *Verifier) fetchSettled(ctx context.Context, sig string) (*txResult, Verdict, error) {
	tx, err := v.getTransaction(ctx, sig)
	if err != nil {
		return nil, Verdict{}, err
	}
	// A transaction that is not there yet is not a transaction that failed.
	if tx == nil {
		return nil, Verdict{Status: StatusPending, Reason: "pending"}, nil
	}
	if tx.Meta == nil {
		return nil, Verdict{Status: StatusRejected, Reason: "incomplete"}, nil
	}
	if tx.Meta.Err != nil {
		return nil, Verdict{Status: StatusRejected, Reason: "tx_failed"}, nil
	}
	return tx, Verdict{}, nil
}

// skrVerdict judges a settled transaction on the SKR rail: the configured
// mint must be present, credited to the configured treasury, by exactly the
// pack price — with the exponent taken from the very balances being judged.
func (v *Verifier) skrVerdict(tx *txResult) Verdict {
	hasMint := false
	var credited []tokenBalance
	for _, b := range tx.Meta.PostTokenBalances {
		if b.Mint != v.cfg.Mint {
			continue
		}
		hasMint = true
		// The receiver must be our treasury: either as the owner of the token
		// account, or as the account itself. Filtering on mint *and* receiver
		// is what makes a look-alike token or a payment to someone else
		// unbuyable rather than accidentally accepted.
		if b.Owner == v.cfg.Treasury || tx.accountKeyAt(b.AccountIndex) == v.cfg.Treasury {
			credited = append(credited, b)
		}
	}
	if !hasMint {
		return Verdict{Status: StatusRejected, Reason: "wrong_mint"}
	}
	if len(credited) == 0 {
		return Verdict{Status: StatusRejected, Reason: "wrong_recipient"}
	}

	for _, b := range credited {
		// The exponent comes from the chain, on the very balances being
		// judged — never from SKR_DECIMALS, which is only ever warned about.
		expected := packRaw(v.cfg.Price, b.UI.Decimals)
		if delta := deltaFor(tx.Meta, b); delta != nil && delta.Cmp(expected) == 0 {
			return Verdict{
				Status:   StatusVerified,
				Reason:   "verified",
				Decimals: b.UI.Decimals,
				Expected: expected,
			}
		}
	}
	return Verdict{Status: StatusRejected, Reason: "wrong_amount"}
}

// solVerdict judges a settled transaction on the devnet-SOL test rail: one
// System transfer of exactly the quoted lamports to the treasury. Only the
// System program's own parsed transfers count — a token instruction that
// merely mentions the treasury as an account cannot buy, which is the same
// mint-and-receiver discipline skrVerdict applies, one program over.
func (v *Verifier) solVerdict(tx *txResult) Verdict {
	sawRecipient := false
	for _, ix := range tx.parsedInstructions() {
		if ix.Program != "system" || ix.Parsed == nil || ix.Parsed.Type != "transfer" {
			continue
		}
		if ix.Parsed.Info.Destination != v.cfg.Treasury {
			continue
		}
		sawRecipient = true
		if ix.Parsed.Info.Lamports == v.cfg.SolLamports {
			return Verdict{
				Status:   StatusVerified,
				Reason:   "verified",
				Decimals: SolDecimals,
				Expected: new(big.Int).SetUint64(v.cfg.SolLamports),
			}
		}
	}
	if !sawRecipient {
		return Verdict{Status: StatusRejected, Reason: "wrong_recipient"}
	}
	return Verdict{Status: StatusRejected, Reason: "wrong_amount"}
}

// packRaw converts a whole-token price into the base units the chain counts.
func packRaw(price, decimals int) *big.Int {
	n := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(decimals)), nil)
	return n.Mul(n, big.NewInt(int64(price)))
}

// deltaFor is how much of our mint landed in this account. A missing pre-state
// entry means the account did not exist before, so the delta is the whole
// balance — which can only under-credit, never over-credit: if the account
// already held funds the pre entry is present and the arithmetic is exact.
func deltaFor(meta *txMeta, post tokenBalance) *big.Int {
	pre := new(big.Int)
	for _, b := range meta.PreTokenBalances {
		if b.AccountIndex == post.AccountIndex && b.Mint == post.Mint && b.Owner == post.Owner {
			if n, ok := new(big.Int).SetString(b.UI.Amount, 10); ok {
				pre = n
			}
			break
		}
	}
	postAmount, ok := new(big.Int).SetString(post.UI.Amount, 10)
	if !ok {
		return nil
	}
	return new(big.Int).Sub(postAmount, pre)
}

// ProbeMint reads decimals and the owning program off the configured mint, so
// a mistyped SKR_MINT says so at startup instead of becoming a silent black
// hole where nobody can ever buy. It is an operator warning: verification does
// not depend on it, because verification takes its exponent from the chain
// anyway.
func (v *Verifier) ProbeMint(ctx context.Context) (decimals int, owner string, err error) {
	if v.cfg.RPCURL == "" || v.cfg.Mint == "" {
		return 0, "", errors.New("SKR_RPC_URL and SKR_MINT are not both configured")
	}
	params := []any{v.cfg.Mint, map[string]any{"encoding": "base64", "commitment": "confirmed"}}
	var out struct {
		Value *struct {
			Owner string   `json:"owner"`
			Data  []string `json:"data"`
		} `json:"value"`
	}
	if err := v.call(ctx, "getAccountInfo", params, &out); err != nil {
		return 0, "", err
	}
	if out.Value == nil {
		return 0, "", fmt.Errorf("mint account %s does not exist", v.cfg.Mint)
	}
	if len(out.Value.Data) == 0 {
		return 0, out.Value.Owner, errors.New("mint account returned no data")
	}
	raw, err := base64.StdEncoding.DecodeString(out.Value.Data[0])
	if err != nil {
		return 0, out.Value.Owner, err
	}
	// The SPL Mint layout is COption<Pubkey> mint authority (4 + 32) then u64
	// supply (8), which puts the decimals byte at offset 44. Anything shorter
	// than that is not a mint.
	if len(raw) < 45 {
		return 0, out.Value.Owner, errors.New("account is too short to be an SPL mint")
	}
	return int(raw[44]), out.Value.Owner, nil
}

// ---------------------------------------------------------------------------
// JSON-RPC plumbing
// ---------------------------------------------------------------------------

type tokenAmount struct {
	Amount   string `json:"amount"`
	Decimals int    `json:"decimals"`
}

type tokenBalance struct {
	AccountIndex int         `json:"accountIndex"`
	Mint         string      `json:"mint"`
	Owner        string      `json:"owner"`
	UI           tokenAmount `json:"uiTokenAmount"`
}

type txMeta struct {
	Err               any            `json:"err"`
	PreTokenBalances  []tokenBalance `json:"preTokenBalances"`
	PostTokenBalances []tokenBalance `json:"postTokenBalances"`
}

// parsedInstruction is one instruction as jsonParsed renders it: the System
// and SPL programs both arrive with their `parsed` body decoded, and the
// devnet-SOL rail reads System transfers straight off it. `program` is the
// program's own lowercase name ("system"), which is what keeps a token
// instruction that merely mentions the treasury from counting as a payment.
type parsedInstruction struct {
	Program string `json:"program"`
	Parsed  *struct {
		Type string `json:"type"`
		Info struct {
			Source      string `json:"source"`
			Destination string `json:"destination"`
			Lamports    uint64 `json:"lamports"`
		} `json:"info"`
	} `json:"parsed"`
}

type txResult struct {
	Meta        *txMeta `json:"meta"`
	Transaction *struct {
		Message struct {
			AccountKeys []struct {
				Pubkey string `json:"pubkey"`
			} `json:"accountKeys"`
			Instructions []parsedInstruction `json:"instructions"`
		} `json:"message"`
	} `json:"transaction"`
}

// parsedInstructions is the nil-safe view of the message's instructions.
func (t *txResult) parsedInstructions() []parsedInstruction {
	if t == nil || t.Transaction == nil {
		return nil
	}
	return t.Transaction.Message.Instructions
}

func (t *txResult) accountKeyAt(i int) string {
	if t == nil || t.Transaction == nil {
		return ""
	}
	keys := t.Transaction.Message.AccountKeys
	if i < 0 || i >= len(keys) {
		return ""
	}
	return keys[i].Pubkey
}

func (v *Verifier) getTransaction(ctx context.Context, signature string) (*txResult, error) {
	params := []any{signature, map[string]any{
		"encoding":                       "jsonParsed",
		"commitment":                     "confirmed",
		"maxSupportedTransactionVersion": 0,
	}}
	var out *txResult
	if err := v.call(ctx, "getTransaction", params, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// call issues one bounded JSON-RPC request. The timeout is enforced twice —
// on the context, so a caller's cancellation also cuts it short, and on the
// client — because a hung oracle must fail closed rather than hang the
// request that is waiting to be paid.
func (v *Verifier) call(ctx context.Context, method string, params, result any) error {
	ctx, cancel := context.WithTimeout(ctx, v.cfg.Timeout)
	defer cancel()

	body, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  method,
		"params":  params,
	})
	if err != nil {
		return fmt.Errorf("solana rpc: encode: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, v.cfg.RPCURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("solana rpc: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := v.client.Do(req)
	if err != nil {
		return fmt.Errorf("solana rpc: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return fmt.Errorf("solana rpc: read: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("solana rpc: status %d", resp.StatusCode)
	}
	var envelope struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return fmt.Errorf("solana rpc: bad response: %w", err)
	}
	if envelope.Error != nil {
		return fmt.Errorf("solana rpc error %d: %s", envelope.Error.Code, envelope.Error.Message)
	}
	if len(envelope.Result) == 0 {
		return errors.New("solana rpc: response carried no result")
	}
	return json.Unmarshal(envelope.Result, result)
}
