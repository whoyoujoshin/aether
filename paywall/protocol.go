// Package paywall charges AETH per HTTP request, using the x402 wire
// format (HTTP 402 + X-PAYMENT / X-PAYMENT-RESPONSE headers) with an
// Aether-specific payment scheme, "aether-memo":
//
//  1. A request without payment gets 402 and a PaymentRequired body
//     naming the price, the address to pay and a one-time invoice ID.
//  2. The client sends that amount to that address with the invoice ID
//     as the transaction memo -- from any wallet, so humans can pay
//     too -- and waits for it to be in a block.
//  3. It repeats the request with an X-PAYMENT header carrying the
//     invoice ID and transaction hash. The server looks the
//     transaction up on chain and, if it paid this invoice, serves
//     the request exactly once.
//
// Unlike x402's "exact" scheme on EVM chains, the client broadcasts the
// payment itself and nothing is signed for the server to settle, so no
// facilitator is involved. The cost is latency: with ~60s blocks, a
// paid request waits about a block for confirmation.
//
// For agents making many small requests, a server can also offer the
// "aether-prepaid" scheme (see prepaid.go): deposit once on chain, then
// pay per request instantly by signing each request with the account's
// ML-DSA key.
//
// Operational notes: issuing invoices stores nothing (they are
// HMAC-signed), but each X-PAYMENT carrying a genuine invoice costs one
// transaction lookup on the node, so put a rate limit in front of a
// public deployment. A payment confirmed after its invoice expired is
// kept by the payee and not served; there are no automatic refunds.
package paywall

import (
	"encoding/base64"
	"encoding/json"
)

const (
	X402Version = 1
	// Scheme is the per-request payment scheme.
	Scheme = "aether-memo"
	// SchemePrepaid draws requests from a deposited balance.
	SchemePrepaid = "aether-prepaid"
	// DepositMemoPrefix + the account to credit is a deposit's memo.
	DepositMemoPrefix = "prepaid:"
	// Asset is the denom prices are stated in.
	Asset = "uaeth"

	HeaderPayment         = "X-PAYMENT"
	HeaderPaymentResponse = "X-PAYMENT-RESPONSE"
)

// Error values in a 402 body's "error" field.
const (
	ErrPaymentRequired   = "payment_required"
	ErrInvalidPayment    = "invalid_payment"    // X-PAYMENT unreadable
	ErrUnsupportedScheme = "unsupported_scheme" // wrong scheme or network
	ErrInvalidInvoice    = "invalid_invoice"    // not issued by this server
	ErrResourceMismatch  = "invoice_for_other_resource"
	ErrInvoiceExpired    = "invoice_expired"
	ErrNotConfirmed      = "payment_not_confirmed" // not in a block (yet): retry
	ErrPaymentFailed     = "payment_failed"        // in a block, but failed
	ErrMemoMismatch      = "memo_mismatch"
	ErrInsufficient      = "insufficient_payment"
	ErrAlreadyRedeemed   = "invoice_already_redeemed"

	// aether-prepaid
	ErrInvalidSignature    = "invalid_signature"
	ErrStaleRequest        = "stale_request" // timestamp outside the allowed window
	ErrPriceAboveMax       = "price_above_signed_max"
	ErrInsufficientBalance = "insufficient_balance"
	ErrInvalidDeposit      = "invalid_deposit"
	ErrRequestTooLarge     = "request_too_large"
)

// PaymentRequired is the body of a 402 response.
type PaymentRequired struct {
	X402Version int                   `json:"x402Version"`
	Error       string                `json:"error"`
	Message     string                `json:"message,omitempty"`
	Accepts     []PaymentRequirements `json:"accepts"`
}

// PaymentRequirements is one way to pay; this package offers one.
type PaymentRequirements struct {
	Scheme            string `json:"scheme"`
	Network           string `json:"network"` // chain ID
	MaxAmountRequired string `json:"maxAmountRequired"`
	Asset             string `json:"asset"`
	PayTo             string `json:"payTo"`
	Resource          string `json:"resource"`
	Description       string `json:"description"`
	MimeType          string `json:"mimeType"`
	MaxTimeoutSeconds int64  `json:"maxTimeoutSeconds"`
	Extra             Extra  `json:"extra"`
}

// Extra carries the schemes' own fields.
type Extra struct {
	// aether-memo: Invoice is the memo the payment must carry, and what
	// X-PAYMENT must name.
	Invoice    string `json:"invoice,omitempty"`
	AmountAeth string `json:"amountAeth"`
	ExpiresAt  string `json:"expiresAt,omitempty"` // present the payment by then

	// aether-prepaid
	DepositMemo string `json:"depositMemo,omitempty"` // with the account to credit in place of <address>
	MinDeposit  string `json:"minDeposit,omitempty"`  // uaeth
	Balance     string `json:"balance,omitempty"`     // uaeth, when the request named an account
	// WithdrawPath, if set, is where unspent balance can be withdrawn.
	WithdrawPath string `json:"withdrawPath,omitempty"`

	Instructions string `json:"instructions"`
}

// PaymentPayload is the decoded X-PAYMENT header. Payload's shape
// depends on Scheme: MemoPayment or PrepaidPayment.
type PaymentPayload struct {
	X402Version int             `json:"x402Version"`
	Scheme      string          `json:"scheme"`
	Network     string          `json:"network"`
	Payload     json.RawMessage `json:"payload"`
}

// MemoPayment proves payment of an invoice.
type MemoPayment struct {
	Invoice string `json:"invoice"`
	TxHash  string `json:"txHash"`
}

// SettlementResponse is the decoded X-PAYMENT-RESPONSE header.
type SettlementResponse struct {
	Success     bool   `json:"success"`
	Transaction string `json:"transaction,omitempty"` // aether-memo
	Network     string `json:"network"`
	Payer       string `json:"payer"`
	Balance     string `json:"balance,omitempty"` // aether-prepaid: uaeth left
}

// EncodeMemoPayment builds an aether-memo X-PAYMENT header value.
func EncodeMemoPayment(network, invoice, txHash string) (string, error) {
	return encodePayment(Scheme, network, MemoPayment{Invoice: invoice, TxHash: txHash})
}

func encodePayment(scheme, network string, payload any) (string, error) {
	bz, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	return EncodeHeader(PaymentPayload{X402Version: X402Version, Scheme: scheme, Network: network, Payload: bz})
}

// EncodeHeader encodes an X-PAYMENT or X-PAYMENT-RESPONSE value.
func EncodeHeader(v any) (string, error) {
	bz, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(bz), nil
}

// DecodeHeader decodes an X-PAYMENT or X-PAYMENT-RESPONSE value.
func DecodeHeader(s string, v any) error {
	bz, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return err
	}
	return json.Unmarshal(bz, v)
}

const instructions = "Send maxAmountRequired uaeth to payTo with memo set to exactly this invoice, wait until the transaction is in a block, " +
	"then repeat this request with header X-PAYMENT: base64 of the JSON " +
	`{"x402Version":1,"scheme":"aether-memo","network":"<network>","payload":{"invoice":"<invoice>","txHash":"<hash>"}}. ` +
	"Each invoice pays for one response, and must be presented by expiresAt."
