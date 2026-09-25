package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"cosmossdk.io/math"
	"github.com/stretchr/testify/require"

	"github.com/whoyoujoshin/aether/paywall"
)

// recordingPayout "pays" by remembering what it signed.
type recordingPayout struct {
	mu     sync.Mutex
	signed []string
	state  string
}

func (p *recordingPayout) Sign(to string, amount math.Int, memo string) ([]byte, uint64, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	s := fmt.Sprintf("%s %s %s", to, amount, memo)
	p.signed = append(p.signed, s)
	return []byte(s), uint64(len(p.signed)), nil
}

func (p *recordingPayout) Submit([]byte, uint64) (paywall.PayoutState, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.state == "" {
		return paywall.PayoutState{Status: paywall.PayoutPending}, nil
	}
	return paywall.PayoutState{Status: p.state}, nil
}

func withdrawingSeller(t *testing.T, f *fakeChain, payout paywall.Payout) *httptest.Server {
	t.Helper()
	ledger, err := paywall.NewFileLedger("")
	require.NoError(t, err)
	pw, err := paywall.New(paywall.Config{
		PayTo: sellerAddr(), Price: math.NewInt(20_000), Network: chainID, Lookup: f.lookup,
		Prepaid: &paywall.PrepaidConfig{Ledger: ledger, MinDeposit: math.NewInt(50_000), Payout: payout},
	})
	require.NoError(t, err)
	mux := http.NewServeMux()
	mux.Handle(paywall.ManifestPath, pw.ManifestHandler("svc", ""))
	mux.Handle(paywall.WithdrawPath, pw.WithdrawHandler())
	mux.Handle("/", pw.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "answer") })))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func withdraw(t *testing.T, service, amount, key string) (withdrawPrepaidOutput, error) {
	t.Helper()
	_, out, err := toolWithdrawPrepaid(context.Background(), nil, withdrawPrepaidInput{Service: service, Amount: amount, IdempotencyKey: key})
	return out, err
}

func balances(t *testing.T) []prepaidBalanceDTO {
	t.Helper()
	_, out, err := toolListPrepaidBalances(context.Background(), nil, listPrepaidBalancesInput{})
	require.NoError(t, err)
	return out.Balances
}

func TestWithdrawPrepaid_TakesBackWhatsLeftOnce(t *testing.T) {
	f := setupAgent(t)
	f.autoInclude = true
	payout := &recordingPayout{}
	srv := withdrawingSeller(t, f, payout)

	_, err := fetchPrepay(t, srv.URL+"/q", "k1", "0.1 AETH", 5)
	require.NoError(t, err)
	got := balances(t)
	require.Len(t, got, 1)
	require.Equal(t, srv.URL, got[0].Service)
	require.Equal(t, sellerAddr(), got[0].PayTo)
	require.Equal(t, "0.08", got[0].Balance.Aeth)

	// Too little (and not everything) is refused; nothing is paid.
	_, err = withdraw(t, srv.URL, "0.01 AETH", "w0")
	requireCode(t, err, codeInsufficientPrepaid)
	_, err = withdraw(t, srv.URL, "10000", "w0")
	requireCode(t, err, codeInvalidAmount)

	out, err := withdraw(t, srv.URL+"/any/path", "all", "w1")
	require.NoError(t, err)
	require.Equal(t, "pending", out.Status)
	require.Equal(t, "0.08", out.Amount.Aeth)
	require.Equal(t, "0", out.Balance.Uaeth)
	require.NotEmpty(t, out.TxHash)
	w, _ := newWallet()
	agent, _ := getOrCreateAgentAccount(w)
	require.Len(t, payout.signed, 1)
	require.Contains(t, payout.signed[0], agent.Address+" 80000 prepaid-withdrawal:")
	require.Empty(t, balances(t), "nothing left anywhere")

	// Retrying the same key reports the same payout.
	payout.state = paywall.PayoutConfirmed
	again, err := withdraw(t, srv.URL, "", "w1")
	require.NoError(t, err)
	require.Equal(t, "confirmed", again.Status)
	require.Equal(t, out.TxHash, again.TxHash)
	require.Len(t, payout.signed, 1)

	_, err = withdraw(t, srv.URL, "", "w2")
	requireCode(t, err, codeInsufficientPrepaid)

	// Spending again after a withdrawal just deposits again.
	res, err := fetchPrepay(t, srv.URL+"/q", "k2", "0.1 AETH", 5)
	require.NoError(t, err)
	require.Equal(t, "answer", res.Body)
	require.NotEmpty(t, res.Payment.DepositTxHash)
}

func TestWithdrawPrepaid_ServiceWithoutWithdrawals(t *testing.T) {
	f := setupAgent(t)
	srv := withdrawingSeller(t, f, nil)
	_, err := withdraw(t, srv.URL, "", "w1")
	requireCode(t, err, codeWithdrawalsUnavailable)

	plain := httptest.NewServer(http.NotFoundHandler())
	defer plain.Close()
	_, err = withdraw(t, plain.URL, "", "w1")
	requireCode(t, err, codePaymentUnsupported)

	_, err = withdraw(t, "ftp://x", "", "w1")
	requireCode(t, err, codeInvalidArgument)
	_, err = withdraw(t, srv.URL, "", "")
	requireCode(t, err, codeInvalidArgument)
}

func TestWithdrawPrepaid_ManifestCantRedirectTheRequest(t *testing.T) {
	setupAgent(t)
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == paywall.ManifestPath {
			fmt.Fprintf(w, `{"x402Version":1,"network":%q,"payTo":%q,"withdrawPath":"@evil.example/w"}`, chainID, sellerAddr())
			return
		}
		hits++
	}))
	defer srv.Close()
	_, err := withdraw(t, srv.URL, "", "w1")
	requireCode(t, err, codePaymentUnsupported)
	require.Zero(t, hits, "nothing was sent anywhere")
}
