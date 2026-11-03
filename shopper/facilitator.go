package shopper

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/whoyoujoshin/aether/x402"
)

// Facilitator is the public verify and settle HTTP API. It holds no key.
type Facilitator struct {
	BaseURL string
	Client  *http.Client
}

func (f Facilitator) client() *http.Client {
	if f.Client != nil {
		return f.Client
	}
	return &http.Client{Timeout: 150 * time.Second}
}

func (f Facilitator) Verify(ctx context.Context, p x402.PaymentPayload, req x402.PaymentRequirements) (x402.VerifyResponse, error) {
	var out x402.VerifyResponse
	err := f.post(ctx, "/verify", p, req, &out)
	return out, err
}

func (f Facilitator) Settle(ctx context.Context, p x402.PaymentPayload, req x402.PaymentRequirements) (x402.SettleResponse, error) {
	var out x402.SettleResponse
	err := f.post(ctx, "/settle", p, req, &out)
	return out, err
}

func (f Facilitator) post(ctx context.Context, path string, p x402.PaymentPayload, req x402.PaymentRequirements, dest any) error {
	body, err := json.Marshal(x402.VerifyRequest{
		X402Version: x402.Version, PaymentPayload: p, PaymentRequirements: req,
	})
	if err != nil {
		return err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(f.BaseURL, "/")+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := f.client().Do(httpReq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode == http.StatusServiceUnavailable {
		return fmt.Errorf("facilitator busy")
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("facilitator %s: HTTP %d", path, resp.StatusCode)
	}
	if err := json.Unmarshal(b, dest); err != nil {
		return fmt.Errorf("facilitator %s: %w", path, err)
	}
	return nil
}

// preBroadcast is a facilitator reason that means the transaction was
// not broadcast. invalid_transaction_state is not in this set: the
// payment may still land.
func preBroadcast(reason string) bool {
	switch reason {
	case x402.ReasonVersion, x402.ReasonScheme, x402.ReasonNetwork, x402.ReasonPayload,
		x402.ReasonRequirements, x402.ReasonInsufficientFunds, x402.ReasonAcceptedMismatch,
		x402.ReasonTransaction, x402.ReasonMessage, x402.ReasonRecipientMismatch,
		x402.ReasonAmountMismatch, x402.ReasonSigner, x402.ReasonSignature,
		x402.ReasonSequence, x402.ReasonExpired, x402.ReasonFee, x402.ReasonGas,
		x402.ReasonSettlementRejected:
		return true
	default:
		return false
	}
}
