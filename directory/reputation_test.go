package directory

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/whoyoujoshin/aether/paywall"
	"github.com/whoyoujoshin/aether/wallet"
)

func TestRatingMemo(t *testing.T) {
	m, err := RatingMemo("https://Weather.Example/", 4)
	require.NoError(t, err)
	require.Equal(t, "x402-rate:4:https://weather.example", m)
	for _, s := range []int{0, 6, -1} {
		_, err := RatingMemo("https://w.example", s)
		require.Error(t, err)
	}
}

func TestRatings_LatestPerRaterAndWellFormed(t *testing.T) {
	alice, bob := addr("alice"), addr("bob")
	got := Ratings([]wallet.IncomingPayment{
		pay(5, alice, "x402-rate:2:https://w.example", 1, 0),
		pay(6, alice, "x402-rate:5:https://W.example/", 1, 0), // replaces alice's 2
		pay(7, bob, "x402-rate:1:https://w.example", 1, 5),     // failed tx
		pay(8, bob, "x402-rate:9:https://w.example", 1, 0),     // bad score
		pay(9, bob, "x402-rate:3:https://w.example", 0, 0),     // unpaid
		pay(10, bob, "x402-rate:3:not a url", 1, 0),
		pay(11, bob, "x402-rate:4:https://w.example", 1, 0),
		pay(2, bob, "x402-rate:4:https://old.example", 1, 0), // before the window
	}, 3)
	require.Len(t, got, 2)
	require.Equal(t, Rating{Rater: bob, URL: "https://w.example", Score: 4, Height: 11, TxHash: "TX11"}, got[0])
	require.Equal(t, 5, got[1].Score)
}

func TestAssess_OnlyRatingsFromPayingBuyersCount(t *testing.T) {
	payee, alice, bob, carol := addr("payee"), addr("alice"), addr("bob"), addr("carol")
	paid := []wallet.IncomingPayment{
		pay(10, alice, "inv-1", 20_000, 0),
		pay(11, alice, "inv-2", 20_000, 0),
		pay(12, bob, "inv-3", 20_000, 5), // failed: not a payment
		pay(13, payee, "self", 99_000, 0), // paying yourself isn't a customer
		pay(20, carol, "inv-4", 20_000, 0),
	}
	ratings := []Rating{
		{Rater: alice, URL: "https://w.example", Score: 5, Height: 15},
		{Rater: bob, URL: "https://w.example", Score: 1, Height: 15},   // never paid
		{Rater: carol, URL: "https://w.example", Score: 1, Height: 18}, // paid only after rating
		{Rater: payee, URL: "https://w.example", Score: 5, Height: 18}, // rating yourself
		{Rater: alice, URL: "https://other.example", Score: 1, Height: 15},
	}
	rep := Assess("https://w.example", payee, paid, ratings, 5)
	require.Equal(t, 3, rep.Stats.Payments)
	require.Equal(t, 2, rep.Stats.Payers)
	require.Equal(t, "60000", rep.Stats.Volume.String())
	require.Len(t, rep.Ratings, 1)
	require.Equal(t, alice, rep.Ratings[0].Rater)

	require.Equal(t, RatingSummary{Count: 1, Average: 5}, Summarize(rep.Ratings, nil))
	require.Equal(t, RatingSummary{}, Summarize(rep.Ratings, func(r string) bool { return r == bob }))
}

func TestDirectory_ListingsCarryReputation(t *testing.T) {
	payee, alice := addr("payee"), addr("alice")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(paywall.Manifest{X402Version: 1, Name: "W", Network: "n", PayTo: payee, Price: "10"})
	}))
	defer srv.Close()
	u, _ := NormalizeURL(srv.URL)
	var scannedSince int64
	d := &Directory{
		Network: "n",
		Fetch: func(ctx context.Context, base string) (*paywall.Manifest, error) {
			return SafeFetcher(true)(ctx, base)
		},
		Scan: func() ([]wallet.IncomingPayment, error) {
			return []wallet.IncomingPayment{
				pay(3, payee, AnnouncePrefix+u, 1, 0),
				pay(95, alice, "x402-rate:4:"+u, 1, 0),
			}, nil
		},
		ScanPayee: func(address string, since int64) ([]wallet.IncomingPayment, error) {
			require.Equal(t, payee, address)
			scannedSince = since
			return []wallet.IncomingPayment{pay(90, alice, "inv", 10, 0)}, nil
		},
		LatestHeight: func() (int64, error) { return 100, nil },
		Window:       50,
	}
	ls, err := d.Listings(context.Background())
	require.NoError(t, err)
	require.Len(t, ls, 1)
	require.Equal(t, int64(50), scannedSince)
	require.NotNil(t, ls[0].Reputation)
	require.Equal(t, 1, ls[0].Reputation.Stats.Payers)
	require.Len(t, ls[0].Reputation.Ratings, 1)
	require.Equal(t, 4, ls[0].Reputation.Ratings[0].Score)
}
