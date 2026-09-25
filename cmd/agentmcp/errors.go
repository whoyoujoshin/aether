package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/whoyoujoshin/aether/wallet"
)

// Every tool failure is a JSON object with a stable code, so an agent
// can decide what to do next without parsing English:
//
//	{"error":{"code":"DAILY_LIMIT_EXCEEDED","retryable":false,"retryAfterSeconds":5400,"message":"..."}}
//
// retryable means "the identical call may succeed if repeated"; codes
// are part of this server's interface and are never renamed.
const (
	codeInvalidAmount       = "INVALID_AMOUNT"
	codeInvalidAddress      = "INVALID_ADDRESS"
	codeInvalidArgument     = "INVALID_ARGUMENT"
	codePerTxLimit          = "PER_TX_LIMIT_EXCEEDED"
	codeDailyLimit          = "DAILY_LIMIT_EXCEEDED"
	codeIdempotencyConflict = "IDEMPOTENCY_CONFLICT"
	codeAccountNotFound     = "ACCOUNT_NOT_FOUND"
	codeInsufficientFunds   = "INSUFFICIENT_FUNDS"
	codeInsufficientFee     = "INSUFFICIENT_FEE"
	codeGrantNotActive      = "GRANT_NOT_ACTIVE"
	codeGrantNotFound       = "GRANT_NOT_FOUND"
	codeGrantExpired        = "GRANT_EXPIRED"
	codeGrantLimit          = "GRANT_LIMIT_EXCEEDED"
	codeGrantRecipient      = "GRANT_RECIPIENT_NOT_ALLOWED"
	codeFeeGrantRejected    = "FEE_GRANT_REJECTED"
	codeNodeUnreachable     = "NODE_UNREACHABLE"
	codeBroadcastUncertain  = "BROADCAST_UNCERTAIN"
	codeTxRejected          = "TX_REJECTED" // refused before entering a block
	codeTxFailed            = "TX_FAILED"   // in a block, but failed
	codeScanLimit           = "SCAN_LIMIT"
	// fetch_paid
	codeHTTPError              = "HTTP_ERROR" // couldn't reach the server
	codePaymentUnsupported     = "PAYMENT_UNSUPPORTED"
	codePriceExceedsMax        = "PRICE_EXCEEDS_MAX"
	codePaymentPending         = "PAYMENT_PENDING"
	codePaymentRejected        = "PAYMENT_REJECTED"
	codePaymentAlreadyRedeemed = "PAYMENT_ALREADY_REDEEMED"
	// announce_service
	codeServiceUnverifiable = "SERVICE_UNVERIFIABLE"
	codeApprovalRejected    = "APPROVAL_REJECTED"
	codeInternal            = "INTERNAL"
)

var retryableCodes = map[string]bool{
	codeNodeUnreachable:    true,
	codeBroadcastUncertain: true,
	codeHTTPError:          true,
	codePaymentPending:     true,
}

type agentError struct {
	Code              string `json:"code"`
	Retryable         bool   `json:"retryable"`
	RetryAfterSeconds int64  `json:"retryAfterSeconds,omitempty"`
	TxHash            string `json:"txHash,omitempty"`
	Message           string `json:"message"`
}

func newError(code, message string) *agentError {
	return &agentError{Code: code, Retryable: retryableCodes[code], Message: message}
}

func (e *agentError) Error() string {
	bz, _ := json.Marshal(struct {
		Error *agentError `json:"error"`
	}{e})
	return string(bz)
}

// classify gives an error that doesn't already carry a code one.
func classify(err error) *agentError {
	var ae *agentError
	if errors.As(err, &ae) {
		return ae
	}
	switch {
	case errors.Is(err, wallet.ErrAuthzNotActive):
		return newError(codeGrantNotActive, err.Error())
	case errors.Is(err, context.DeadlineExceeded):
		return newError(codeNodeUnreachable, err.Error())
	}
	switch status.Code(err) {
	case codes.Unavailable, codes.DeadlineExceeded, codes.Canceled, codes.ResourceExhausted, codes.Aborted:
		return newError(codeNodeUnreachable, err.Error())
	}
	return newError(codeInternal, err.Error())
}

// coded makes every error a tool returns a structured one.
func coded[In, Out any](h mcp.ToolHandlerFor[In, Out]) mcp.ToolHandlerFor[In, Out] {
	return func(ctx context.Context, req *mcp.CallToolRequest, in In) (*mcp.CallToolResult, Out, error) {
		res, out, err := h(ctx, req, in)
		if err != nil {
			return res, out, classify(err)
		}
		return res, out, nil
	}
}

// chainErrorCode maps a transaction's (codespace, code) onto this
// server's codes; fallback is codeTxRejected or codeTxFailed.
func chainErrorCode(codespace string, code uint32, rawLog, fallback string) string {
	switch {
	case codespace == "sdk" && code == 5:
		// x/bank's SendAuthorization reports an over-limit spend as
		// insufficient funds too; its message tells them apart.
		if strings.Contains(rawLog, "spend limit") {
			return codeGrantLimit
		}
		return codeInsufficientFunds
	case codespace == "sdk" && code == 4 && strings.Contains(rawLog, "cannot send to"):
		return codeGrantRecipient
	case codespace == "sdk" && code == 13:
		return codeInsufficientFee
	case codespace == "authz" && code == 2:
		return codeGrantNotFound
	case codespace == "authz" && code == 6:
		return codeGrantExpired
	case codespace == "feegrant":
		return codeFeeGrantRejected
	}
	return fallback
}
