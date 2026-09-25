package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"testing"
	"time"

	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	"github.com/whoyoujoshin/aether/app"
	"github.com/whoyoujoshin/aether/paywall"
	"github.com/whoyoujoshin/aether/wallet"
)

func TestMain(m *testing.M) {
	app.SetAddressPrefixes()
	os.Exit(m.Run())
}

// The proxy as main wires it, against a recording upstream.
func TestProxy_UpstreamSeesPayerOnlyWhenPaid(t *testing.T) {
	var seen http.Header
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.Header.Clone()
		fmt.Fprint(w, "data")
	}))
	defer upstream.Close()
	target, _ := url.Parse(upstream.URL)

	payTo := sdk.AccAddress("seller______________").String()
	payer := sdk.AccAddress("buyer_______________").String()
	var memo string
	pw, err := paywall.New(paywall.Config{
		PayTo: payTo, Price: math.NewInt(5), Network: "n",
		Lookup: func(hash string) (*wallet.TransactionDetail, error) {
			return &wallet.TransactionDetail{Hash: hash, Memo: memo, Timestamp: time.Now().UTC().Format(time.RFC3339),
				Transfers: []wallet.Transfer{{From: payer, To: payTo, Amount: "5uaeth"}}}, nil
		},
	})
	require.NoError(t, err)
	proxy := newProxy(target)
	srv := httptest.NewServer(newHandler(proxy, pw.Middleware(withPayerHeaders(proxy)), []string{"/health"}, pw.ManifestHandler("Test", "d"), pw.WithdrawHandler()))
	defer srv.Close()

	do := func(path string, h map[string]string) *http.Response {
		req, _ := http.NewRequest(http.MethodGet, srv.URL+path, nil)
		for k, v := range h {
			req.Header.Set(k, v)
		}
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		return resp
	}

	// The manifest is free and describes the service.
	seen = nil
	resp := do(paywall.ManifestPath, nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Nil(t, seen, "the manifest is served by the proxy, not upstream")

	// A client can't claim to have paid by setting the headers itself.
	seen = nil
	resp = do("/health", map[string]string{headerPayer: "liar"})
	require.Equal(t, http.StatusOK, resp.StatusCode, "free paths skip payment")
	require.Empty(t, seen.Get(headerPayer))

	seen = nil
	resp = do("/api", map[string]string{headerPayer: "liar"})
	require.Equal(t, http.StatusPaymentRequired, resp.StatusCode)
	require.Nil(t, seen, "unpaid requests never reach upstream")

	// Get an invoice, "pay" it, and come back with proof.
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api", nil)
	r402, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	var pr paywall.PaymentRequired
	require.NoError(t, jsonDecode(r402.Body, &pr))
	memo = pr.Accepts[0].Extra.Invoice
	proof, _ := paywall.EncodeMemoPayment("n", memo, "ABCD")

	resp = do("/api", map[string]string{paywall.HeaderPayment: proof, headerPayer: "liar"})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, payer, seen.Get(headerPayer))
	require.Equal(t, "ABCD", seen.Get(headerPaymentTx))
	require.Empty(t, seen.Get(paywall.HeaderPayment), "the proof isn't forwarded")
}

func jsonDecode(r io.ReadCloser, v any) error {
	defer r.Close()
	return json.NewDecoder(r).Decode(v)
}
