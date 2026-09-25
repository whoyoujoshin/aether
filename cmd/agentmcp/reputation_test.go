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

	"github.com/whoyoujoshin/aether/directory"
	"github.com/whoyoujoshin/aether/paywall"
	"github.com/whoyoujoshin/aether/wallet"
)

func TestFindServices_ReputationAndRating(t *testing.T) {
	f := setupAgent(t)
	f.autoInclude = true
	resetDirectory(t)
	w, _ := newWallet()
	agent, _ := getOrCreateAgentAccount(w)
	payTo := sdk.AccAddress("rated_seller________").String()
	pw, err := paywall.New(paywall.Config{PayTo: payTo, Price: math.NewInt(20_000), Network: chainID, Lookup: f.lookup})
	require.NoError(t, err)
	mux := http.NewServeMux()
	mux.Handle(paywall.ManifestPath, pw.ManifestHandler("Weather", "forecasts"))
	mux.Handle("/", pw.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "sunny") })))
	srv := httptest.NewServer(mux)
	defer srv.Close()
	popular := manifestAt(t, paywall.Manifest{X402Version: 1, Name: "Popular", Network: chainID, PayTo: sellerAddr(), Price: "20000"})

	// Rating before buying doesn't count, so the tool refuses.
	_, _, err = toolRateService(context.Background(), nil, rateServiceInput{URL: srv.URL, Score: 5})
	requireCode(t, err, codeInvalidArgument)
	_, _, err = toolFetchPaid(context.Background(), nil, fetchPaidInput{URL: srv.URL + "/today", MaxAmount: "0.05 AETH", IdempotencyKey: "b1"})
	require.NoError(t, err)
	_, rated, err := toolRateService(context.Background(), nil, rateServiceInput{URL: srv.URL + "/", Score: 5})
	require.NoError(t, err)
	require.Equal(t, statusPending, rated.Status)
	memo, _ := decode(t, f.broadcasts[len(f.broadcasts)-1])
	require.Equal(t, "x402-rate:5:"+srv.URL, memo)
	_, _, err = toolRateService(context.Background(), nil, rateServiceInput{URL: srv.URL, Score: 9})
	requireCode(t, err, codeInvalidArgument)

	stranger, friend, sock := sdk.AccAddress("stranger____________").String(), sdk.AccAddress("friend______________").String(), sdk.AccAddress("sock________________").String()
	trustedRaters = []string{friend}
	t.Cleanup(func() { trustedRaters = nil })
	one := sdk.NewCoins(sdk.NewInt64Coin("uaeth", 1))
	paid := func(h int64, from string) wallet.IncomingPayment {
		return wallet.IncomingPayment{Hash: fmt.Sprintf("P%d", h), Height: h, From: from, Amount: sdk.NewCoins(sdk.NewInt64Coin("uaeth", 20_000))}
	}
	rate := func(h int64, from, url string, score int) wallet.IncomingPayment {
		m, err := directory.RatingMemo(url, score)
		require.NoError(t, err)
		return wallet.IncomingPayment{Hash: fmt.Sprintf("R%d", h), Height: h, From: from, Memo: m, Amount: one}
	}
	f.mu.Lock()
	f.height = 100
	f.payments = []wallet.IncomingPayment{
		{Hash: "A1", Height: 10, From: payTo, Memo: directory.AnnouncePrefix + srv.URL, Amount: one},
		{Hash: "A2", Height: 11, From: sellerAddr(), Memo: directory.AnnouncePrefix + popular, Amount: one},
		rate(60, agent.Address, srv.URL, 5),
		rate(61, friend, srv.URL, 4),
		rate(62, stranger, srv.URL, 1),
		rate(63, sock, srv.URL, 1), // never paid it: ignored
		rate(64, sock, popular, 5),
	}
	f.paymentsTo = map[string][]wallet.IncomingPayment{
		payTo:        {paid(50, agent.Address), paid(51, friend), paid(52, stranger)},
		sellerAddr(): {paid(50, sock), paid(51, sock), paid(52, sock), paid(53, stranger), paid(54, friend)},
	}
	f.mu.Unlock()

	_, out, err := toolFindServices(context.Background(), nil, findServicesInput{OrderBy: "trusted"})
	require.NoError(t, err)
	require.Len(t, out.Services, 2)
	s := out.Services[0]
	require.Equal(t, "Weather", s.Name, "own experience and trusted ratings beat faked popularity")
	require.Equal(t, 3, s.Reputation.Payers)
	require.Equal(t, 3, s.Reputation.Ratings.Count, "the non-paying sock's rating doesn't count")
	require.InDelta(t, 10.0/3, s.Reputation.Ratings.Average, 0.001)
	require.Equal(t, ratingSummaryDTO{Count: 2, Average: 4.5}, s.Reputation.TrustedRatings, "this agent's and --trust's")
	require.Equal(t, &yourHistoryDTO{Purchases: 1, YourRating: 5}, s.YourHistory)

	p := out.Services[1]
	require.Equal(t, "Popular", p.Name)
	require.Equal(t, 5, p.Reputation.Payments)
	require.Equal(t, 1, p.Reputation.Ratings.Count)
	require.Equal(t, 0, p.Reputation.TrustedRatings.Count)
	require.Nil(t, p.YourHistory)
}
