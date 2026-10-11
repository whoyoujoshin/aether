// Package x402 is the standard x402 (v2) "exact" payment scheme on
// Aether, and the facilitator logic that verifies and settles it.
//
// x402 v2 (https://github.com/x402-foundation/x402, specs/) is HTTP 402
// payments: a resource server answers 402 with PaymentRequirements, the
// client retries with a signed PaymentPayload, and a facilitator verifies
// the payment and settles it on chain. The reference implementations
// cover EVM and Solana; this package adds Aether as the CAIP-2 network
// "cosmos:<chain-id>" (cosmos:aether-testnet-1 for the testnet).
//
// The exact scheme on Aether follows the Sui model: the payer signs a
// complete transaction, one bank MsgSend of exactly the required amount
// of the required denom to payTo, and sends its bytes. The facilitator
// holds no key and can't change where the money goes; it checks the
// transaction and broadcasts it. The payer pays the (tiny) fee in uaeth.
package x402

import "encoding/json"

// Version is the x402 protocol version this package speaks.
const Version = 2

// SchemeExact is the scheme identifier.
const SchemeExact = "exact"

// Network is the CAIP-2 identifier of an Aether chain.
func Network(chainID string) string { return "cosmos:" + chainID }

// ResourceInfo describes the paid resource.
type ResourceInfo struct {
	URL         string `json:"url"`
	Description string `json:"description,omitempty"`
	MimeType    string `json:"mimeType,omitempty"`
}

// PaymentRequirements is one way a resource accepts payment. On Aether,
// Asset is the denom ("uaeth", or "ibc/..." for USDC) and Amount is in
// its base units.
type PaymentRequirements struct {
	Scheme            string         `json:"scheme"`
	Network           string         `json:"network"`
	Amount            string         `json:"amount"`
	Asset             string         `json:"asset"`
	PayTo             string         `json:"payTo"`
	MaxTimeoutSeconds int            `json:"maxTimeoutSeconds"`
	Extra             map[string]any `json:"extra,omitempty"`
}

// PaymentRequired is the 402 response body (and the PAYMENT-REQUIRED header).
type PaymentRequired struct {
	X402Version int                   `json:"x402Version"`
	Error       string                `json:"error,omitempty"`
	Resource    *ResourceInfo         `json:"resource,omitempty"`
	Accepts     []PaymentRequirements `json:"accepts"`
	Extensions  map[string]any        `json:"extensions,omitempty"`
}

// PaymentPayload is what the client sends (the PAYMENT-SIGNATURE header).
// For the exact scheme on Aether, Payload is ExactPayload.
type PaymentPayload struct {
	X402Version int                 `json:"x402Version"`
	Resource    *ResourceInfo       `json:"resource,omitempty"`
	Accepted    PaymentRequirements `json:"accepted"`
	Payload     json.RawMessage     `json:"payload"`
	Extensions  map[string]any      `json:"extensions,omitempty"`
}

// ExactPayload is the exact scheme's payload on Aether: the signed
// transaction's bytes (a protobuf TxRaw), base64.
type ExactPayload struct {
	Transaction string `json:"transaction"`
}

// VerifyRequest is the body of POST /verify and POST /settle.
type VerifyRequest struct {
	X402Version         int                 `json:"x402Version"`
	PaymentPayload      PaymentPayload      `json:"paymentPayload"`
	PaymentRequirements PaymentRequirements `json:"paymentRequirements"`
}

// VerifyResponse answers POST /verify.
type VerifyResponse struct {
	IsValid       bool   `json:"isValid"`
	InvalidReason string `json:"invalidReason,omitempty"`
	Payer         string `json:"payer,omitempty"`
}

// SettleResponse answers POST /settle (and is the PAYMENT-RESPONSE header).
type SettleResponse struct {
	Success     bool   `json:"success"`
	ErrorReason string `json:"errorReason,omitempty"`
	Payer       string `json:"payer,omitempty"`
	Transaction string `json:"transaction"`
	Network     string `json:"network"`
	Amount      string `json:"amount,omitempty"`
}

// SupportedKind is one scheme and network a facilitator handles.
type SupportedKind struct {
	X402Version int            `json:"x402Version"`
	Scheme      string         `json:"scheme"`
	Network     string         `json:"network"`
	Extra       map[string]any `json:"extra,omitempty"`
}

// SupportedResponse answers GET /supported.
type SupportedResponse struct {
	Kinds      []SupportedKind     `json:"kinds"`
	Extensions []string            `json:"extensions"`
	Signers    map[string][]string `json:"signers"`
}

// Reasons a payment is refused. The generic ones are the spec's; the
// exact-scheme ones follow its invalid_exact_<network>_* naming, for the
// cosmos network family (docs/x402/scheme_exact_cosmos.md).
const (
	ReasonVersion            = "invalid_x402_version"
	ReasonScheme             = "unsupported_scheme"
	ReasonNetwork            = "invalid_network"
	ReasonPayload            = "invalid_payload"
	ReasonRequirements       = "invalid_payment_requirements"
	ReasonInsufficientFunds  = "insufficient_funds"
	ReasonTransactionState   = "invalid_transaction_state"
	ReasonUnexpectedVerify   = "unexpected_verify_error"
	ReasonUnexpectedSettle   = "unexpected_settle_error"
	ReasonAcceptedMismatch   = "invalid_exact_cosmos_payload_accepted_mismatch"
	ReasonTransaction        = "invalid_exact_cosmos_payload_transaction"
	ReasonMessage            = "invalid_exact_cosmos_payload_message"
	ReasonRecipientMismatch  = "invalid_exact_cosmos_payload_recipient_mismatch"
	ReasonAmountMismatch     = "invalid_exact_cosmos_payload_amount_mismatch"
	ReasonSigner             = "invalid_exact_cosmos_payload_signer"
	ReasonSignature          = "invalid_exact_cosmos_payload_signature"
	ReasonSequence           = "invalid_exact_cosmos_payload_sequence"
	ReasonExpired            = "invalid_exact_cosmos_payload_expired"
	ReasonFee                = "invalid_exact_cosmos_payload_fee"
	ReasonGas                = "invalid_exact_cosmos_payload_gas"
	ReasonAlreadySettled     = "invalid_exact_cosmos_payload_already_settled"
	ReasonSettlementRejected = "invalid_exact_cosmos_settlement_rejected"
)
