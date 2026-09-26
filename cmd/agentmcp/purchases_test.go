package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	"github.com/whoyoujoshin/aether/crypto/mldsa"
	"github.com/whoyoujoshin/aether/paywall"
)

// receiptSeller signs receipts with its payee key; tamper, if set,
// rewrites the response after the paywall signed it.
func receiptSeller(t *testing.T, f *fakeChain, tamper bool) (*httptest.Server, string) {
	t.Helper()
	sk, err := mldsa.GenPrivKey()
	require.NoError(t, err)
	payTo := sdk.AccAddress(sk.PubKey().Address()).String()
	ledger, err := paywall.NewFileLedger("")
	require.NoError(t, err)
	pw, err := paywall.New(paywall.Config{
		PayTo: payTo, Price: math.NewInt(20_000), Network: chainID, Lookup: f.lookup,
		Prepaid: &paywall.PrepaidConfig{Ledger: ledger, MinDeposit: math.NewInt(50_000)},
		Receipts: &paywall.ReceiptConfig{Sign: func(msg []byte) ([]byte, []byte, error) {
			sig, err := sk.Sign(msg)
			return sig, sk.PubKey().Bytes(), err
		}},
	})
	require.NoError(t, err)
	var h http.Handler = pw.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "the real answer") }))
	if tamper {
		inner := h
		h = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rec := httptest.NewRecorder()
			inner.ServeHTTP(rec, r)
			for k, v := range rec.Header() {
				w.Header()[k] = v
			}
			w.WriteHeader(rec.Code)
			if rec.Code != http.StatusOK {
				_, _ = w.Write(rec.Body.Bytes())
				return
			}
			fmt.Fprint(w, "something else")
		})
	}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv, payTo
}

func TestFetchPaid_ReceiptsAreCheckedAndKept(t *testing.T) {
	f := setupAgent(t)
	f.autoInclude = true
	srv, payTo := receiptSeller(t, f, false)

	_, out, err := toolFetchPaid(context.Background(), nil, fetchPaidInput{URL: srv.URL + "/q?city=oslo", Method: "POST", Body: `{"d":1}`, MaxAmount: "0.05 AETH", IdempotencyKey: "m1"})
	require.NoError(t, err)
	require.Equal(t, "paid", out.Status)
	require.NotNil(t, out.Receipt)
	require.True(t, out.Receipt.Verified, out.Receipt.Problem)
	require.Equal(t, payTo, out.Receipt.Receipt.PayTo)
	require.Equal(t, out.Payment.TxHash, out.Receipt.Receipt.Payment)

	pre, err := fetchPrepay(t, srv.URL+"/q", "p1", "0.1 AETH", 5)
	require.NoError(t, err)
	require.True(t, pre.Receipt.Verified, pre.Receipt.Problem)
	require.Equal(t, paywall.SchemePrepaid, pre.Receipt.Receipt.Scheme)

	_, list, err := toolListPurchases(context.Background(), nil, listPurchasesInput{Service: srv.URL})
	require.NoError(t, err)
	require.Len(t, list.Purchases, 2)
	require.Equal(t, paywall.SchemePrepaid, list.Purchases[0].Scheme, "newest first")
	for _, p := range list.Purchases {
		require.True(t, p.Verified)
		r, err := paywall.DecodeReceipt(p.Receipt)
		require.NoError(t, err)
		require.NoError(t, r.Verify(), "the kept receipt still proves the purchase")
	}
}

func TestFetchPaid_ReceiptForAnotherResponseIsFlagged(t *testing.T) {
	f := setupAgent(t)
	f.autoInclude = true
	srv, _ := receiptSeller(t, f, true)
	_, out, err := toolFetchPaid(context.Background(), nil, fetchPaidInput{URL: srv.URL + "/q", MaxAmount: "0.05 AETH", IdempotencyKey: "m1"})
	require.NoError(t, err)
	require.Equal(t, "something else", out.Body)
	require.False(t, out.Receipt.Verified)
	require.Contains(t, out.Receipt.Problem, "responseHash")
	_, list, _ := toolListPurchases(context.Background(), nil, listPurchasesInput{})
	require.False(t, list.Purchases[0].Verified)
}

func TestFetchPaid_NoReceiptsIsFine(t *testing.T) {
	f := setupAgent(t)
	f.autoInclude = true
	srv, _, _ := prepaidSeller(t, f)
	_, out, err := toolFetchPaid(context.Background(), nil, fetchPaidInput{URL: srv.URL + "/q", MaxAmount: "0.05 AETH", IdempotencyKey: "m1"})
	require.NoError(t, err)
	require.Nil(t, out.Receipt)
	_, list, _ := toolListPurchases(context.Background(), nil, listPurchasesInput{})
	require.Len(t, list.Purchases, 1, "still logged")
}
