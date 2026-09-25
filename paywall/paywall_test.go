package paywall

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	"github.com/whoyoujoshin/aether/app"
	"github.com/whoyoujoshin/aether/wallet"
)

func TestMain(m *testing.M) {
	app.SetAddressPrefixes()
	os.Exit(m.Run())
}

func seller() string { return sdk.AccAddress("seller______________").String() }
func buyer() string  { return sdk.AccAddress("buyer_______________").String() }

type fakeLedger struct {
	mu  sync.Mutex
	txs map[string]*wallet.TransactionDetail
	err error
}

func (l *fakeLedger) lookup(hash string) (*wallet.TransactionDetail, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.err != nil {
		return nil, l.err
	}
	if d := l.txs[hash]; d != nil {
		return d, nil
	}
	return nil, fmt.Errorf("%w: %s", wallet.ErrTransactionNotFound, hash)
}

func (l *fakeLedger) pay(hash, memo, to string, uaeth int64, at time.Time, code uint32) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.txs[hash] = &wallet.TransactionDetail{
		Hash: hash, Code: code, Memo: memo, Timestamp: at.UTC().Format(time.RFC3339),
		Transfers: []wallet.Transfer{{From: buyer(), To: to, Amount: fmt.Sprintf("%duaeth", uaeth)}},
	}
}

type harness struct {
	t      *testing.T
	ledger *fakeLedger
	srv    *httptest.Server
	now    time.Time
	served int
	fail   bool
}

func newHarness(t *testing.T) *harness {
	h := &harness{t: t, ledger: &fakeLedger{txs: map[string]*wallet.TransactionDetail{}}, now: time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)}
	p, err := New(Config{
		PayTo: seller(), Price: math.NewInt(10_000), Network: "aether-testnet-1",
		Description: "weather", Lookup: h.ledger.lookup, Now: func() time.Time { return h.now },
	})
	require.NoError(t, err)
	mux := http.NewServeMux()
	mux.Handle("/weather", p.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if h.fail {
			http.Error(w, "upstream broke", http.StatusBadGateway)
			return
		}
		h.served++
		fmt.Fprintf(w, "sunny, paid by %s", Payer(w))
	})))
	mux.Handle("/other", p.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})))
	h.srv = httptest.NewServer(mux)
	t.Cleanup(h.srv.Close)
	return h
}

func (h *harness) get(path, payment string) (*http.Response, string) {
	req, _ := http.NewRequest(http.MethodGet, h.srv.URL+path, nil)
	if payment != "" {
		req.Header.Set(HeaderPayment, payment)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(h.t, err)
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp, string(body)
}

func (h *harness) invoice(path string) PaymentRequirements {
	resp, body := h.get(path, "")
	require.Equal(h.t, http.StatusPaymentRequired, resp.StatusCode)
	var pr PaymentRequired
	require.NoError(h.t, json.Unmarshal([]byte(body), &pr))
	require.Equal(h.t, ErrPaymentRequired, pr.Error)
	require.Len(h.t, pr.Accepts, 1)
	return pr.Accepts[0]
}

func proof(invoice, hash string) string {
	s, _ := EncodeMemoPayment("aether-testnet-1", invoice, hash)
	return s
}

func (h *harness) expectRefusal(payment, want string) {
	h.t.Helper()
	resp, body := h.get("/weather", payment)
	require.Equal(h.t, http.StatusPaymentRequired, resp.StatusCode, body)
	var pr PaymentRequired
	require.NoError(h.t, json.Unmarshal([]byte(body), &pr))
	require.Equal(h.t, want, pr.Error, pr.Message)
}

func TestPaywall_PayThenServeExactlyOnce(t *testing.T) {
	h := newHarness(t)
	req := h.invoice("/weather")
	require.Equal(t, Scheme, req.Scheme)
	require.Equal(t, "10000", req.MaxAmountRequired)
	require.Equal(t, "0.01", req.Extra.AmountAeth)
	require.Equal(t, seller(), req.PayTo)
	require.Less(t, len(req.Extra.Invoice), 256, "must fit in a memo")
	require.Equal(t, h.srv.URL+"/weather", req.Resource)

	// Not in a block yet: told to retry, with the same invoice.
	resp, body := h.get("/weather", proof(req.Extra.Invoice, "AB12"))
	require.Equal(t, http.StatusPaymentRequired, resp.StatusCode)
	require.Equal(t, "10", resp.Header.Get("Retry-After"))
	var pending PaymentRequired
	require.NoError(t, json.Unmarshal([]byte(body), &pending))
	require.Equal(t, ErrNotConfirmed, pending.Error)
	require.Equal(t, req.Extra.Invoice, pending.Accepts[0].Extra.Invoice)

	h.ledger.pay("AB12", req.Extra.Invoice, seller(), 10_000, h.now.Add(time.Minute), 0)
	resp, body = h.get("/weather", proof(req.Extra.Invoice, "ab12"))
	require.Equal(t, http.StatusOK, resp.StatusCode, body)
	require.Equal(t, "sunny, paid by "+buyer(), body)
	var settled SettlementResponse
	require.NoError(t, DecodeHeader(resp.Header.Get(HeaderPaymentResponse), &settled))
	require.Equal(t, SettlementResponse{Success: true, Transaction: "AB12", Network: "aether-testnet-1", Payer: buyer()}, settled)

	// The transaction is public on chain: replaying its proof must not
	// get a second response.
	h.expectRefusal(proof(req.Extra.Invoice, "AB12"), ErrAlreadyRedeemed)
	require.Equal(t, 1, h.served)
}

func TestPaywall_RefusesWhatDidntPay(t *testing.T) {
	h := newHarness(t)
	inv := h.invoice("/weather").Extra.Invoice
	at := h.now.Add(time.Minute)

	h.ledger.pay("SHORT", inv, seller(), 9_999, at, 0)
	h.expectRefusal(proof(inv, "SHORT"), ErrInsufficient)

	h.ledger.pay("ELSEWHERE", inv, buyer(), 10_000, at, 0)
	h.expectRefusal(proof(inv, "ELSEWHERE"), ErrInsufficient)

	h.ledger.pay("WRONGMEMO", "some other memo", seller(), 10_000, at, 0)
	h.expectRefusal(proof(inv, "WRONGMEMO"), ErrMemoMismatch)

	h.ledger.pay("FAILED", inv, seller(), 10_000, at, 5)
	h.expectRefusal(proof(inv, "FAILED"), ErrPaymentFailed)

	// Invoices are bound to this server, this resource and a deadline.
	tampered := inv[:len(inv)-3] + "AAA"
	h.ledger.pay("TAMPERED", tampered, seller(), 10_000, at, 0)
	h.expectRefusal(proof(tampered, "TAMPERED"), ErrInvalidInvoice)
	h.expectRefusal(proof("made-up", "X"), ErrInvalidInvoice)

	other := h.invoice("/other").Extra.Invoice
	h.ledger.pay("OTHER", other, seller(), 10_000, at, 0)
	h.expectRefusal(proof(other, "OTHER"), ErrResourceMismatch)

	h.expectRefusal("%%%not-base64", ErrInvalidPayment)
	wrongNet, _ := EncodeMemoPayment("other-chain", inv, "X")
	h.expectRefusal(wrongNet, ErrUnsupportedScheme)

	require.Zero(t, h.served)

	// ...and after all that the invoice is still good for a real payment.
	h.ledger.pay("GOOD", inv, seller(), 12_000, at, 0)
	resp, _ := h.get("/weather", proof(inv, "GOOD"))
	require.Equal(t, http.StatusOK, resp.StatusCode)

	// A payment that took hours to confirm is still served...
	inv2 := h.invoice("/weather").Extra.Invoice
	h.ledger.pay("SLOW", inv2, seller(), 10_000, h.now.Add(6*time.Hour), 0)
	h.now = h.now.Add(6 * time.Hour)
	resp, _ = h.get("/weather", proof(inv2, "SLOW"))
	require.Equal(t, http.StatusOK, resp.StatusCode, "slow confirmation must not forfeit a payment")

	// ...but an invoice presented after its 24h lifetime is dead.
	inv3 := h.invoice("/weather").Extra.Invoice
	h.ledger.pay("STALE", inv3, seller(), 10_000, h.now, 0)
	h.now = h.now.Add(24*time.Hour + time.Second)
	h.expectRefusal(proof(inv3, "STALE"), ErrInvoiceExpired)
}

func TestPaywall_ServerErrorLetsThePaymentBeReused(t *testing.T) {
	h := newHarness(t)
	inv := h.invoice("/weather").Extra.Invoice
	h.ledger.pay("P", inv, seller(), 10_000, h.now, 0)

	h.fail = true
	resp, _ := h.get("/weather", proof(inv, "P"))
	require.Equal(t, http.StatusBadGateway, resp.StatusCode)

	h.fail = false
	resp, body := h.get("/weather", proof(inv, "P"))
	require.Equal(t, http.StatusOK, resp.StatusCode, body)
}

func TestPaywall_NodeDownIsNotAPaymentVerdict(t *testing.T) {
	h := newHarness(t)
	inv := h.invoice("/weather").Extra.Invoice
	h.ledger.err = fmt.Errorf("connection refused")
	resp, _ := h.get("/weather", proof(inv, "P"))
	require.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)

	h.ledger.err = nil
	h.ledger.pay("P", inv, seller(), 10_000, h.now, 0)
	resp, _ = h.get("/weather", proof(inv, "P"))
	require.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestInvoice_DifferentSecretRejected(t *testing.T) {
	a, err := New(Config{PayTo: seller(), Price: math.NewInt(1), Network: "n", Lookup: (&fakeLedger{}).lookup})
	require.NoError(t, err)
	b, err := New(Config{PayTo: seller(), Price: math.NewInt(1), Network: "n", Lookup: (&fakeLedger{}).lookup})
	require.NoError(t, err)
	inv, err := a.newInvoice(invoice{expiry: time.Now(), price: 1, resource: resourceKey("GET", "/")})
	require.NoError(t, err)
	_, err = a.parseInvoice(inv)
	require.NoError(t, err)
	_, err = b.parseInvoice(inv)
	require.ErrorIs(t, err, errBadInvoice, "a restarted server (new random secret) must not honor old invoices")
	require.True(t, strings.HasPrefix(inv, invoicePrefix))
}
