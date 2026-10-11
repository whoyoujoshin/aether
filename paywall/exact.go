package paywall

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/whoyoujoshin/aether/wallet"
	"github.com/whoyoujoshin/aether/x402"
)

// The standard x402 v2 headers, for the exact scheme. The aether-*
// schemes keep x402 v1's X-PAYMENT and X-PAYMENT-RESPONSE.
const (
	HeaderPaymentRequired   = "PAYMENT-REQUIRED"
	HeaderPaymentSignature  = "PAYMENT-SIGNATURE"
	HeaderPaymentResponseV2 = "PAYMENT-RESPONSE"
)

// Settler verifies and settles exact payments: an *x402.Facilitator on
// the paywall's node.
type Settler interface {
	Settle(ctx context.Context, p x402.PaymentPayload, req x402.PaymentRequirements) x402.SettleResponse
}

// ExactConfig offers the standard x402 v2 exact scheme (package x402): a
// buyer with any x402 v2 client, such as Coinbase's @x402/fetch with
// aether-chain-client's ExactAetherScheme, pays each request with a
// signed send of the price, which is settled before the request is
// served. Every 402 then also carries a PAYMENT-REQUIRED header.
type ExactConfig struct {
	Settler Settler
	// MaxTimeoutSeconds is what the requirement states (default 60).
	MaxTimeoutSeconds int
}

// exactState is the exact payments this paywall broadcast: only those may
// be served on a retry, once (one whose response failed, or whose block
// came after the request gave up). A transfer to payTo it didn't
// broadcast -- another scheme's payment, say -- is never accepted here.
type exactState struct {
	mu   sync.Mutex
	sent map[string]time.Time // tx hash → until when it may be redeemed
}

func (p *Paywall) exactNetwork() string { return x402.Network(p.cfg.Network) }

func (p *Paywall) exactRequirement() x402.PaymentRequirements {
	timeout := p.cfg.Exact.MaxTimeoutSeconds
	if timeout <= 0 {
		timeout = 60
	}
	return x402.PaymentRequirements{
		Scheme: x402.SchemeExact, Network: p.exactNetwork(),
		Amount: p.cfg.Price.String(), Asset: p.cfg.Asset.Denom, PayTo: p.cfg.PayTo,
		MaxTimeoutSeconds: timeout,
		Extra:             map[string]any{"symbol": p.cfg.Asset.Symbol, "decimals": p.cfg.Asset.Decimals},
	}
}

// exactPaymentRequired is the PAYMENT-REQUIRED header for a 402.
func (p *Paywall) exactPaymentRequired(r *http.Request, code string) string {
	h, _ := EncodeHeader(x402.PaymentRequired{
		X402Version: x402.Version,
		Error:       code,
		Resource:    &x402.ResourceInfo{URL: resourceURL(r), Description: p.cfg.Description, MimeType: p.cfg.MimeType},
		Accepts:     []x402.PaymentRequirements{p.exactRequirement()},
	})
	return h
}

func (p *Paywall) serveExact(w http.ResponseWriter, r *http.Request, header string, next http.Handler) {
	var pay x402.PaymentPayload
	if err := DecodeHeader(header, &pay); err != nil {
		p.paymentRequired(w, r, ErrInvalidPayment, "PAYMENT-SIGNATURE must be base64-encoded JSON", "")
		return
	}
	s := p.cfg.Exact.Settler.Settle(r.Context(), pay, p.exactRequirement())
	if s.Transaction != "" {
		p.noteExact(s.Transaction) // broadcast: from now on it may be redeemed here
	}
	if s.Success {
		p.serveExactPaid(w, r, next, s.Transaction, s.Payer)
		return
	}

	// Already on chain? Then it's a retry of a payment broadcast here.
	hash := exactTxHash(pay)
	if hash == "" || !p.sentExact(hash) {
		p.paymentRequired(w, r, s.ErrorReason, "payment not accepted: "+s.ErrorReason, "")
		return
	}
	detail, err := p.cfg.Lookup(hash)
	if errors.Is(err, wallet.ErrTransactionNotFound) {
		w.Header().Set("Retry-After", "10")
		p.paymentRequired(w, r, ErrNotConfirmed, "transaction "+hash+" is not in a block yet; retry shortly with the same PAYMENT-SIGNATURE", "")
		return
	}
	if err != nil {
		log.Printf("paywall: looking up %s: %v", hash, err)
		w.Header().Set("Retry-After", "10")
		http.Error(w, "could not reach the chain to verify payment; retry with the same PAYMENT-SIGNATURE", http.StatusServiceUnavailable)
		return
	}
	if detail.Code != 0 {
		p.paymentRequired(w, r, ErrPaymentFailed, "transaction "+hash+" failed on chain", "")
		return
	}
	paid, payer := p.received(detail)
	if paid.LT(p.cfg.Price) {
		p.paymentRequired(w, r, ErrInsufficient, fmt.Sprintf("paid %s to %s; the price is %s", p.both(paid), p.cfg.PayTo, p.both(p.cfg.Price)), "")
		return
	}
	p.serveExactPaid(w, r, next, hash, payer)
}

func (p *Paywall) serveExactPaid(w http.ResponseWriter, r *http.Request, next http.Handler, hash, payer string) {
	key := "exact:" + hash
	if !p.store.Redeem(key, p.cfg.Now().Add(p.cfg.InvoiceTTL)) {
		p.paymentRequired(w, r, ErrAlreadyRedeemed, "this payment has already been used for a response", "")
		return
	}
	settlement, _ := EncodeHeader(x402.SettleResponse{Success: true, Payer: payer, Transaction: hash, Network: p.exactNetwork(), Amount: p.cfg.Price.String()})
	w.Header().Set(HeaderPaymentResponseV2, settlement)
	bought := paidRequest{scheme: x402.SchemeExact, payer: payer, payment: hash}
	if p.cfg.Receipts != nil {
		bought.body = readForReceipt(r)
	}
	if status := p.serveReceipted(w, r, next, bought); status >= 500 {
		p.store.Release(key) // the server failed: the same payment may be used again
	}
}

func (p *Paywall) noteExact(hash string) {
	now := p.cfg.Now()
	p.exact.mu.Lock()
	defer p.exact.mu.Unlock()
	if p.exact.sent == nil {
		p.exact.sent = map[string]time.Time{}
	}
	for h, until := range p.exact.sent {
		if now.After(until) {
			delete(p.exact.sent, h)
		}
	}
	p.exact.sent[hash] = now.Add(p.cfg.InvoiceTTL)
}

func (p *Paywall) sentExact(hash string) bool {
	p.exact.mu.Lock()
	defer p.exact.mu.Unlock()
	until, ok := p.exact.sent[hash]
	return ok && !p.cfg.Now().After(until)
}

// exactTxHash is the hash of the transaction an exact payload carries, or
// "" if it carries none.
func exactTxHash(pay x402.PaymentPayload) string {
	var ep x402.ExactPayload
	if json.Unmarshal(pay.Payload, &ep) != nil {
		return ""
	}
	bz, err := base64.StdEncoding.DecodeString(ep.Transaction)
	if err != nil || len(bz) == 0 {
		return ""
	}
	return x402.TxHash(bz)
}

// Settlement is who paid for a request and with which transaction, from
// the settlement header the middleware set on w (any scheme).
func Settlement(h http.Header) (payer, tx string) {
	var s SettlementResponse
	if DecodeHeader(h.Get(HeaderPaymentResponse), &s) == nil {
		return s.Payer, s.Transaction
	}
	var v2 x402.SettleResponse
	if DecodeHeader(h.Get(HeaderPaymentResponseV2), &v2) == nil {
		return v2.Payer, v2.Transaction
	}
	return "", ""
}
