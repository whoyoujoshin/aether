package shopper

import "fmt"

// Decision codes are stable. Agents branch on Code and NextAction.
// Message is the sentence a person sees. Spent is false unless a
// transaction has been broadcast.
const (
	CodeNeedsConfirm      = "NEEDS_CONFIRM"
	CodeNeedsApproval     = "NEEDS_APPROVAL"
	CodeUSDCNeedsApproval = "USDC_NEEDS_APPROVAL"
	CodeOverCap           = "OVER_CAP"
	CodeDailyCap          = "DAILY_CAP"
	CodeNotBuyable        = "NOT_BUYABLE"
	CodeNothingFound      = "NOTHING_FOUND"
	CodeQuoteStale        = "QUOTE_STALE"
	CodePayeeMismatch     = "PAYEE_MISMATCH"
	CodeFacilitatorDown   = "FACILITATOR_DOWN"
	CodeInsufficientFunds = "INSUFFICIENT_FUNDS"
	CodeIdempotency       = "IDEMPOTENCY_CONFLICT"
	CodeSettlementUnknown = "SETTLEMENT_UNKNOWN"
	CodeAlreadyPaid       = "ALREADY_PAID"
	CodePaidNotDelivered  = "PAID_NOT_DELIVERED"
	CodeDryRun            = "DRY_RUN"
	CodeInPolicy          = "IN_POLICY"
	CodeRefused           = "REFUSED"
)

const (
	NextConfirm      = "confirm_or_open_url"
	NextOpenApproval = "open_approval_url"
	NextPickCheaper  = "pick_cheaper_or_edit_config"
	NextWaitOrEdit   = "wait_or_edit_config"
	NextPickAnother  = "pick_another"
	NextBroaden      = "broaden_query"
	NextQuoteAgain   = "shop_quote_again"
	NextRetryLater   = "retry_later"
	NextGetCoins     = "get_coins"
	NextNewKey       = "new_idempotency_key"
	NextPoll         = "poll_status"
	NextReadReceipt  = "read_receipt"
	NextRefetch      = "refetch_same_proof"
	NextSubmit       = "submit_without_dry_run"
	NextBuy          = "buy"
	NextStop         = "stop"
)

// Decision is the outcome of a quote or a buy. A quote never sets Spent.
type Decision struct {
	Code       string `json:"code"`
	NextAction string `json:"next_action"`
	Message    string `json:"message"`
	Spent      bool   `json:"spent"`
	QuoteID    string `json:"quote_id,omitempty"`
}

// Error is a refused buy or a broken request. It is safe to print:
// it carries no key, passphrase, or signature.
type Error struct {
	Decision
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

func decide(code, next, msg, quoteID string) Decision {
	return Decision{Code: code, NextAction: next, Message: msg, QuoteID: quoteID}
}

func fail(code, next, msg string) *Error {
	return &Error{Decision: decide(code, next, msg, "")}
}

func failf(code, next, format string, args ...any) *Error {
	return fail(code, next, fmt.Sprintf(format, args...))
}
