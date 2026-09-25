package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"cosmossdk.io/math"
	"github.com/stretchr/testify/require"

	"github.com/whoyoujoshin/aether/paywall"
)

// prepaidSeller offers both schemes at 0.02 AETH per request.
func prepaidSeller(t *testing.T, f *fakeChain) (*httptest.Server, *atomic.Int32, *paywall.FileLedger) {
	t.Helper()
	ledger, err := paywall.NewFileLedger("")
	require.NoError(t, err)
	pw, err := paywall.New(paywall.Config{
		PayTo: sellerAddr(), Price: math.NewInt(20_000), Network: chainID, Lookup: f.lookup,
		Prepaid: &paywall.PrepaidConfig{Ledger: ledger, MinDeposit: math.NewInt(50_000)},
	})
	require.NoError(t, err)
	var served atomic.Int32
	srv := httptest.NewServer(pw.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		served.Add(1)
		fmt.Fprintf(w, "answer %d", served.Load())
	})))
	t.Cleanup(srv.Close)
	return srv, &served, ledger
}

func fetchPrepay(t *testing.T, url, key, prepay string, timeout int) (fetchPaidOutput, error) {
	t.Helper()
	_, out, err := toolFetchPaid(context.Background(), nil, fetchPaidInput{URL: url, MaxAmount: "0.05 AETH", IdempotencyKey: key, Prepay: prepay, TimeoutSeconds: timeout})
	return out, err
}

func TestFetchPaid_PrepaidDepositsOnceThenPaysInstantly(t *testing.T) {
	f := setupAgent(t)
	f.autoInclude = true
	srv, served, _ := prepaidSeller(t, f)

	out, err := fetchPrepay(t, srv.URL+"/q", "k1", "0.1 AETH", 5)
	require.NoError(t, err)
	require.Equal(t, "paid", out.Status)
	require.Equal(t, "answer 1", out.Body)
	require.Equal(t, paywall.SchemePrepaid, out.Payment.Scheme)
	require.NotEmpty(t, out.Payment.DepositTxHash)
	require.Equal(t, "0.08", out.Payment.Balance.Aeth)
	require.Len(t, f.broadcasts, 1)
	memo, _ := decode(t, f.broadcasts[0])
	w, _ := newWallet()
	agent, _ := getOrCreateAgentAccount(w)
	require.Equal(t, "prepaid:"+agent.Address, memo)
	require.Equal(t, int64(100_000), spent(t), "the deposit counts against the daily budget")

	// The next four draw on the balance: no transactions at all.
	for i := 2; i <= 5; i++ {
		out, err = fetchPrepay(t, srv.URL+"/q", fmt.Sprintf("k%d", i), "0.1 AETH", 5)
		require.NoError(t, err)
		require.Equal(t, fmt.Sprintf("answer %d", i), out.Body)
		require.Empty(t, out.Payment.DepositTxHash)
	}
	require.Equal(t, "0", out.Payment.Balance.Uaeth)
	require.Len(t, f.broadcasts, 1)

	// Out of balance: tops up by itself.
	out, err = fetchPrepay(t, srv.URL+"/q", "k6", "0.1 AETH", 5)
	require.NoError(t, err)
	require.Equal(t, "answer 6", out.Body)
	require.NotEmpty(t, out.Payment.DepositTxHash)
	require.Len(t, f.broadcasts, 2)
	require.Equal(t, int32(6), served.Load())
}

func TestFetchPaid_PrepaidRetryIsNeverChargedTwice(t *testing.T) {
	f := setupAgent(t)
	f.autoInclude = true
	srv, served, ledger := prepaidSeller(t, f)

	_, err := fetchPrepay(t, srv.URL+"/q", "once", "0.1 AETH", 5)
	require.NoError(t, err)

	_, err = fetchPrepay(t, srv.URL+"/q", "once", "0.1 AETH", 5)
	requireCode(t, err, codePaymentAlreadyRedeemed)
	w, _ := newWallet()
	agent, _ := getOrCreateAgentAccount(w)
	bal, _ := ledger.Balance(agent.Address)
	require.Equal(t, "80000", bal.String())
	require.Equal(t, int32(1), served.Load())
	require.Len(t, f.broadcasts, 1)
}

func TestFetchPaid_PendingDepositIsReusedNotRepeated(t *testing.T) {
	f := setupAgent(t)
	srv, served, _ := prepaidSeller(t, f)

	out, err := fetchPrepay(t, srv.URL+"/q", "a", "0.1 AETH", 1)
	require.NoError(t, err)
	require.Equal(t, "payment_pending", out.Status)
	require.Len(t, f.broadcasts, 1)

	// Another purchase while the deposit is unconfirmed: same deposit.
	out2, err := fetchPrepay(t, srv.URL+"/q", "b", "0.1 AETH", 1)
	require.NoError(t, err)
	require.Equal(t, "payment_pending", out2.Status)
	require.Equal(t, out.Payment.DepositTxHash, out2.Payment.DepositTxHash)
	require.Equal(t, f.broadcasts[0], f.broadcasts[len(f.broadcasts)-1], "only ever the one deposit transaction")

	f.include(out.Payment.DepositTxHash, 0)
	out, err = fetchPrepay(t, srv.URL+"/q", "a", "0.1 AETH", 5)
	require.NoError(t, err)
	require.Equal(t, "paid", out.Status)
	out, err = fetchPrepay(t, srv.URL+"/q", "b", "0.1 AETH", 5)
	require.NoError(t, err)
	require.Equal(t, "paid", out.Status)
	require.Equal(t, "0.06", out.Payment.Balance.Aeth)
	require.Equal(t, int32(2), served.Load())
	require.Equal(t, int64(100_000), spent(t), "one deposit")
}

func TestFetchPaid_PrepaidIsOptIn(t *testing.T) {
	f := setupAgent(t)
	f.autoInclude = true
	srv, _, _ := prepaidSeller(t, f)

	out, err := fetchPrepay(t, srv.URL+"/q", "plain", "", 5)
	require.NoError(t, err)
	require.Equal(t, paywall.Scheme, out.Payment.Scheme, "without prepay, pay per request")
	require.Equal(t, int64(20_000), spent(t))

	_, err = fetchPrepay(t, srv.URL+"/q", "tiny", "0.01 AETH", 5)
	requireCode(t, err, codePaymentUnsupported) // below the seller's minimum deposit
}
