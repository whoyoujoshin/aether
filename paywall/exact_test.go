package paywall

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"cosmossdk.io/math"
	"github.com/stretchr/testify/require"

	"github.com/whoyoujoshin/aether/wallet"
	"github.com/whoyoujoshin/aether/x402"
)

// fakeSettler answers Settle with what the test sets.
type fakeSettler struct {
	mu    sync.Mutex
	resp  x402.SettleResponse
	calls int
	got   x402.PaymentRequirements
}

func (s *fakeSettler) Settle(_ context.Context, _ x402.PaymentPayload, req x402.PaymentRequirements) x402.SettleResponse {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	s.got = req
	return s.resp
}

func (s *fakeSettler) set(r x402.SettleResponse) {
	s.mu.Lock()
	s.resp = r
	s.mu.Unlock()
}

type exactHarness struct {
	ledger  *fakeLedger
	settler *fakeSettler
	srv     *httptest.Server
	now     time.Time
	served  int
	fail    bool
	payers  []string
}

func newExactHarness(t *testing.T, exact bool) *exactHarness {
	h := &exactHarness{ledger: &fakeLedger{txs: map[string]*wallet.TransactionDetail{}}, settler: &fakeSettler{}, now: time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)}
	cfg := Config{
		PayTo: seller(), Price: math.NewInt(10_000), Network: "aether-testnet-1",
		Description: "weather", Lookup: h.ledger.lookup, Now: func() time.Time { return h.now },
	}
	if exact {
		cfg.Exact = &ExactConfig{Settler: h.settler}
	}
	p, err := New(cfg)
	require.NoError(t, err)
	h.srv = httptest.NewServer(p.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if h.fail {
			http.Error(w, "upstream broke", http.StatusBadGateway)
			return
		}
		h.served++
		h.payers = append(h.payers, Payer(w))
		_, _ = w.Write([]byte(`{"weather":"sunny"}`))
	})))
	t.Cleanup(h.srv.Close)
	return h
}

// exactPayment is a PAYMENT-SIGNATURE header carrying txBytes, and the
// hash the chain would give them.
func exactPayment(txBytes string) (header, hash string) {
	pl, _ := json.Marshal(x402.ExactPayload{Transaction: base64.StdEncoding.EncodeToString([]byte(txBytes))})
	header, _ = EncodeHeader(x402.PaymentPayload{X402Version: 2, Payload: pl})
	return header, x402.TxHash([]byte(txBytes))
}

func (h *exactHarness) get(t *testing.T, sig string) *http.Response {
	req, _ := http.NewRequest("GET", h.srv.URL+"/weather", nil)
	if sig != "" {
		req.Header.Set(HeaderPaymentSignature, sig)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	resp.Body.Close()
	return resp
}

func paymentRequiredV2(t *testing.T, resp *http.Response) x402.PaymentRequired {
	var pr x402.PaymentRequired
	require.NoError(t, DecodeHeader(resp.Header.Get(HeaderPaymentRequired), &pr))
	return pr
}

func TestExactIsOfferedInAV2Header(t *testing.T) {
	h := newExactHarness(t, true)
	resp := h.get(t, "")
	require.Equal(t, 402, resp.StatusCode)
	pr := paymentRequiredV2(t, resp)
	require.Equal(t, 2, pr.X402Version)
	require.Equal(t, h.srv.URL+"/weather", pr.Resource.URL)
	require.Len(t, pr.Accepts, 1)
	want := x402.PaymentRequirements{Scheme: "exact", Network: "cosmos:aether-testnet-1", Amount: "10000", Asset: "uaeth", PayTo: seller(), MaxTimeoutSeconds: 60}
	got := pr.Accepts[0]
	got.Extra = nil
	require.Equal(t, want, got)

	// Without Exact, no header, and a PAYMENT-SIGNATURE is no payment.
	plain := newExactHarness(t, false)
	resp = plain.get(t, "")
	require.Empty(t, resp.Header.Get(HeaderPaymentRequired))
	sig, _ := exactPayment("tx")
	require.Equal(t, 402, plain.get(t, sig).StatusCode)
	require.Zero(t, plain.settler.calls)
}

func TestExactSettlesThenServes(t *testing.T) {
	h := newExactHarness(t, true)
	sig, hash := exactPayment("tx-1")
	h.settler.set(x402.SettleResponse{Success: true, Payer: buyer(), Transaction: hash, Network: "cosmos:aether-testnet-1", Amount: "10000"})
	resp := h.get(t, sig)
	require.Equal(t, 200, resp.StatusCode)
	require.Equal(t, 1, h.served)
	require.Equal(t, []string{buyer()}, h.payers, "the upstream learns who paid")
	require.Equal(t, "10000", h.settler.got.Amount, "settled against this resource's price")
	var s x402.SettleResponse
	require.NoError(t, DecodeHeader(resp.Header.Get(HeaderPaymentResponseV2), &s))
	require.True(t, s.Success)
	require.Equal(t, hash, s.Transaction)
	require.Equal(t, buyer(), s.Payer)

	// The same payment again: the facilitator refuses it (its sequence is
	// used) and the chain shows it, but it's been used for a response.
	h.settler.set(x402.SettleResponse{ErrorReason: x402.ReasonSequence})
	h.ledger.pay(hash, "", seller(), 10_000, h.now, 0)
	resp = h.get(t, sig)
	require.Equal(t, 402, resp.StatusCode)
	require.Equal(t, ErrAlreadyRedeemed, paymentRequiredV2(t, resp).Error)
	require.Equal(t, 1, h.served)
}

func TestExactRefusalIsA402WithTheReason(t *testing.T) {
	h := newExactHarness(t, true)
	sig, _ := exactPayment("tx-poor")
	h.settler.set(x402.SettleResponse{ErrorReason: x402.ReasonInsufficientFunds, Payer: buyer()})
	resp := h.get(t, sig)
	require.Equal(t, 402, resp.StatusCode)
	require.Equal(t, x402.ReasonInsufficientFunds, paymentRequiredV2(t, resp).Error)
	require.Zero(t, h.served)
}

func TestExactRetryServesAPaymentBroadcastHereOnce(t *testing.T) {
	h := newExactHarness(t, true)

	// Settled, but the upstream failed: the payment may be used again.
	sig, hash := exactPayment("tx-2")
	h.settler.set(x402.SettleResponse{Success: true, Payer: buyer(), Transaction: hash})
	h.fail = true
	require.Equal(t, http.StatusBadGateway, h.get(t, sig).StatusCode)
	h.fail = false
	h.settler.set(x402.SettleResponse{ErrorReason: x402.ReasonSequence, Payer: buyer()})
	h.ledger.pay(hash, "", seller(), 10_000, h.now, 0)
	require.Equal(t, 200, h.get(t, sig).StatusCode)
	require.Equal(t, 402, h.get(t, sig).StatusCode, "once")
	require.Equal(t, 1, h.served)

	// Broadcast, but no block before the settle gave up: retry until it lands.
	sig3, hash3 := exactPayment("tx-3")
	h.settler.set(x402.SettleResponse{ErrorReason: x402.ReasonTransactionState, Transaction: hash3})
	require.Equal(t, 402, h.get(t, sig3).StatusCode)
	h.settler.set(x402.SettleResponse{ErrorReason: x402.ReasonSequence})
	resp := h.get(t, sig3)
	require.Equal(t, 402, resp.StatusCode)
	require.Equal(t, ErrNotConfirmed, paymentRequiredV2(t, resp).Error)
	require.Equal(t, "10", resp.Header.Get("Retry-After"))
	h.ledger.pay(hash3, "", seller(), 10_000, h.now, 0)
	require.Equal(t, 200, h.get(t, sig3).StatusCode)
	require.Equal(t, 2, h.served)
}

func TestExactNeverAcceptsATransferItDidntBroadcast(t *testing.T) {
	h := newExactHarness(t, true)
	// Someone else's payment to the seller (another scheme's, say), on
	// chain and public: presenting its bytes buys nothing.
	sig, hash := exactPayment("someone-elses-tx")
	h.ledger.pay(hash, "inv-1", seller(), 1_000_000, h.now, 0)
	h.settler.set(x402.SettleResponse{ErrorReason: x402.ReasonSequence})
	resp := h.get(t, sig)
	require.Equal(t, 402, resp.StatusCode)
	require.Equal(t, x402.ReasonSequence, paymentRequiredV2(t, resp).Error)
	require.Zero(t, h.served)
}
