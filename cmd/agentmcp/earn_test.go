package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	"github.com/whoyoujoshin/aether/paywall"
	"github.com/whoyoujoshin/aether/wallet"
)

func sellerAddr() string { return sdk.AccAddress("seller______________").String() }

// sellerServer is a paid API built on package paywall, verifying
// payments against the same fake chain the agent pays on.
func sellerServer(t *testing.T, f *fakeChain, priceUaeth int64) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	pw, err := paywall.New(paywall.Config{
		PayTo: sellerAddr(), Price: math.NewInt(priceUaeth), Network: chainID, Lookup: f.lookup,
	})
	require.NoError(t, err)
	var served atomic.Int32
	mux := http.NewServeMux()
	mux.Handle("/forecast", pw.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		served.Add(1)
		fmt.Fprint(w, `{"forecast":"sunny"}`)
	})))
	mux.HandleFunc("/free", func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "free stuff") })
	mux.HandleFunc("/weird402", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusPaymentRequired)
		fmt.Fprint(w, "pay me somehow")
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, &served
}

func fetch(t *testing.T, url, maxAmount, key string, timeout int) (fetchPaidOutput, error) {
	t.Helper()
	_, out, err := toolFetchPaid(context.Background(), nil, fetchPaidInput{URL: url, MaxAmount: maxAmount, IdempotencyKey: key, TimeoutSeconds: timeout})
	return out, err
}

func TestFetchPaid_PaysOnceAndGetsTheResponse(t *testing.T) {
	f := setupAgent(t)
	f.autoInclude = true
	srv, served := sellerServer(t, f, 20_000)

	out, err := fetch(t, srv.URL+"/forecast", "0.05 AETH", "buy-1", 5)
	require.NoError(t, err)
	require.Equal(t, "paid", out.Status)
	require.Equal(t, http.StatusOK, out.HTTPStatus)
	require.Equal(t, `{"forecast":"sunny"}`, out.Body)
	require.Equal(t, amountDTO{Uaeth: "20000", Aeth: "0.02"}, out.Payment.Amount)
	require.Equal(t, sellerAddr(), out.Payment.PayTo)
	require.Len(t, f.broadcasts, 1)
	memo, _ := decode(t, f.broadcasts[0])
	require.Equal(t, out.Payment.Invoice, memo, "the payment carries the seller's invoice as its memo")
	require.Equal(t, int64(20_000), spent(t))

	// A retry (say the reply got lost) never pays again. The seller
	// has already served this payment, and says so.
	_, err = fetch(t, srv.URL+"/forecast", "0.05 AETH", "buy-1", 5)
	requireCode(t, err, codePaymentAlreadyRedeemed)
	require.Len(t, f.broadcasts, 1)
	require.Equal(t, int64(20_000), spent(t))
	require.Equal(t, int32(1), served.Load())

	// A new key is a new purchase.
	out, err = fetch(t, srv.URL+"/forecast", "0.05 AETH", "buy-2", 5)
	require.NoError(t, err)
	require.Equal(t, "paid", out.Status)
	require.Len(t, f.broadcasts, 2)
	require.Equal(t, int32(2), served.Load())
}

func TestFetchPaid_ResumesAPendingPaymentWithoutPayingAgain(t *testing.T) {
	f := setupAgent(t)
	srv, served := sellerServer(t, f, 20_000)

	out, err := fetch(t, srv.URL+"/forecast", "1 AETH", "slow", 1)
	require.NoError(t, err)
	require.Equal(t, "payment_pending", out.Status)
	require.Len(t, f.broadcasts, 1)
	require.Zero(t, served.Load())

	f.include(out.Payment.TxHash, 0) // the next block lands

	out2, err := fetch(t, srv.URL+"/forecast", "1 AETH", "slow", 5)
	require.NoError(t, err)
	require.Equal(t, "paid", out2.Status)
	require.Equal(t, out.Payment.TxHash, out2.Payment.TxHash, "the same payment, not a new one")
	require.True(t, out2.Payment.Replayed)
	require.Len(t, f.broadcasts, 1, "a confirmed payment isn't even re-broadcast")
	require.Equal(t, int32(1), served.Load())
}

func TestFetchPaid_Refusals(t *testing.T) {
	f := setupAgent(t)
	f.autoInclude = true
	srv, _ := sellerServer(t, f, 20_000)

	_, err := fetch(t, srv.URL+"/forecast", "0.01 AETH", "too-dear", 5)
	ae := requireCode(t, err, codePriceExceedsMax)
	require.Contains(t, ae.Message, "0.02 AETH")

	_, err = fetch(t, srv.URL+"/weird402", "1 AETH", "weird", 5)
	requireCode(t, err, codePaymentUnsupported)

	_, err = fetch(t, srv.URL+"/forecast", "20000", "no-unit", 5)
	requireCode(t, err, codeInvalidAmount)

	_, err = fetch(t, "file:///etc/passwd", "1 AETH", "scheme", 5)
	requireCode(t, err, codeInvalidArgument)

	_, err = fetch(t, "http://127.0.0.1:1/", "1 AETH", "down", 5)
	ae = requireCode(t, err, codeHTTPError)
	require.True(t, ae.Retryable)

	// Server caps apply to purchases too.
	perTxLimit = 10_000
	_, err = fetch(t, srv.URL+"/forecast", "1 AETH", "over-cap", 5)
	requireCode(t, err, codePerTxLimit)
	require.Empty(t, f.broadcasts, "nothing refused was paid")

	// Free resources pass straight through, no payment.
	out, err := fetch(t, srv.URL+"/free", "1 AETH", "free", 5)
	require.NoError(t, err)
	require.Equal(t, "ok", out.Status)
	require.Equal(t, "free stuff", out.Body)
	require.Nil(t, out.Payment)

	perTxLimit = 1_000_000
	_, err = fetch(t, srv.URL+"/forecast", "1 AETH", "k", 5)
	require.NoError(t, err)
	_, err = fetch(t, srv.URL+"/free", "1 AETH", "k", 5)
	requireCode(t, err, codeIdempotencyConflict)
}

func TestWaitForPayment_WakesOnNewBlock(t *testing.T) {
	f := setupAgent(t)
	defer func(p time.Duration) { pollInterval = p }(pollInterval)
	pollInterval = time.Minute // only a pushed block can wake it in time

	go func() {
		time.Sleep(200 * time.Millisecond)
		f.mu.Lock()
		f.payments = append(f.payments, wallet.IncomingPayment{Hash: "P", Height: 101, Memo: "inv", Amount: sdk.NewCoins(sdk.NewInt64Coin("uaeth", 5))})
		f.mu.Unlock()
		blocks.signal()
	}()
	start := time.Now()
	_, out, err := toolWaitForPayment(context.Background(), nil, waitForPaymentInput{Memo: "inv", MinAmount: "5uaeth", SinceHeight: 100, TimeoutSeconds: 30})
	require.NoError(t, err)
	require.True(t, out.Paid)
	require.Less(t, time.Since(start), 5*time.Second)
}

func TestWaitForPayment_ResumeHeightKeepsAReindexMargin(t *testing.T) {
	f := setupAgent(t)
	f.height = 500
	_, out, err := toolWaitForPayment(context.Background(), nil, waitForPaymentInput{Memo: "nothing", MinAmount: "1uaeth", SinceHeight: 10, TimeoutSeconds: 1})
	require.NoError(t, err)
	require.False(t, out.Paid)
	require.Equal(t, int64(500-reindexMargin), out.ResumeFromHeight)
}

func TestCreateInvoice(t *testing.T) {
	f := setupAgent(t)
	f.height = 777
	_, a, err := toolCreateInvoice(context.Background(), nil, createInvoiceInput{Amount: "0.5 AETH"})
	require.NoError(t, err)
	_, b, err := toolCreateInvoice(context.Background(), nil, createInvoiceInput{Amount: "0.5 AETH"})
	require.NoError(t, err)
	require.NotEqual(t, a.Invoice, b.Invoice)
	require.Equal(t, int64(777), a.SinceHeight)
	require.Equal(t, amountDTO{Uaeth: "500000", Aeth: "0.5"}, a.Amount)
	require.Contains(t, a.Instructions, a.Invoice)

	_, _, err = toolCreateInvoice(context.Background(), nil, createInvoiceInput{Amount: "5"})
	requireCode(t, err, codeInvalidAmount)
}

func TestFetchPaid_RequotesAnExpiringInvoiceNothingWasPaidFor(t *testing.T) {
	f := setupAgent(t)
	f.autoInclude = true
	srv, served := sellerServer(t, f, 20_000)

	// Quoted, but refused before sending anything.
	perTxLimit = 10_000
	_, err := fetch(t, srv.URL+"/forecast", "1 AETH", "later", 5)
	requireCode(t, err, codePerTxLimit)
	st, err := loadState()
	require.NoError(t, err)
	old := st.Fetches["later"]
	require.NotNil(t, old)
	require.False(t, old.ExpiresAt.IsZero(), "the seller's expiry is recorded")

	// Much later, the invoice is about to expire. Paying it now would
	// land too late to be served, so a fresh one is quoted.
	old.ExpiresAt = time.Now().Add(time.Minute)
	require.NoError(t, st.save())
	perTxLimit = 1_000_000
	out, err := fetch(t, srv.URL+"/forecast", "1 AETH", "later", 5)
	require.NoError(t, err)
	require.Equal(t, "paid", out.Status)
	require.NotEqual(t, old.Invoice, out.Payment.Invoice)
	require.Len(t, f.broadcasts, 1)
	require.Equal(t, int32(1), served.Load())
}

func TestFetchPaid_NeverRequotesOnceAPaymentWasAccepted(t *testing.T) {
	f := setupAgent(t)
	srv, _ := sellerServer(t, f, 20_000)

	out, err := fetch(t, srv.URL+"/forecast", "1 AETH", "inflight", 1)
	require.NoError(t, err)
	require.Equal(t, "payment_pending", out.Status)

	st, err := loadState()
	require.NoError(t, err)
	st.Fetches["inflight"].ExpiresAt = time.Now().Add(time.Minute)
	require.NoError(t, st.save())

	again, err := fetch(t, srv.URL+"/forecast", "1 AETH", "inflight", 1)
	require.NoError(t, err)
	require.Equal(t, out.Payment.Invoice, again.Payment.Invoice, "money is in flight for this invoice: stick with it")
	require.Equal(t, out.Payment.TxHash, again.Payment.TxHash)
}
