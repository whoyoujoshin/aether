package shopper

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	"github.com/whoyoujoshin/aether/app"
	"github.com/whoyoujoshin/aether/directory"
	"github.com/whoyoujoshin/aether/paywall"
	"github.com/whoyoujoshin/aether/wallet"
	"github.com/whoyoujoshin/aether/x402"
)

func TestMain(m *testing.M) {
	app.SetAddressPrefixes()
	os.Exit(m.Run())
}

func bech(s string) string {
	b := []byte(s)
	if len(b) < 20 {
		b = append(b, bytes.Repeat([]byte("_"), 20-len(b))...)
	}
	return sdk.AccAddress(b[:20]).String()
}

func listing(url, name, desc, payee, price string, height int64) directory.Listing {
	return directory.Listing{
		Announcement: directory.Announcement{URL: url, Announcer: payee, Height: height},
		Manifest: paywall.Manifest{
			X402Version: paywall.X402Version, Name: name, Description: desc,
			Network: "aether-testnet-1", PayTo: payee, Price: price, Asset: wallet.BaseDenom,
			Schemes: []string{x402.SchemeExact},
		},
	}
}

func exactReq(payTo, amount, asset string, extra map[string]any) x402.PaymentRequirements {
	if asset == "" {
		asset = wallet.BaseDenom
	}
	return x402.PaymentRequirements{
		Scheme: x402.SchemeExact, Network: x402.Network("aether-testnet-1"),
		Amount: amount, Asset: asset, PayTo: payTo, MaxTimeoutSeconds: 60,
		Extra: extra,
	}
}

func serve402(reqs ...x402.PaymentRequirements) http.Handler {
	h, err := paywall.EncodeHeader(x402.PaymentRequired{X402Version: x402.Version, Accepts: reqs})
	if err != nil {
		panic(err)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(paywall.HeaderPaymentRequired, h)
		w.WriteHeader(http.StatusPaymentRequired)
	})
}

func testAssets(t *testing.T) *wallet.Assets {
	t.Helper()
	a, err := wallet.NewAssetsFor(wallet.TestnetUSDC)
	require.NoError(t, err)
	return a
}

func testPolicy() Policy {
	p := DefaultCaps("")
	usdc, err := wallet.NewAssetsFor(wallet.TestnetUSDC)
	if err != nil {
		panic(err)
	}
	if u, ok := usdc.BySymbol("USDC"); ok {
		p.USDCDenom = u.Denom
	}
	return p
}

func probeOf(h http.Handler) func(context.Context, string, string, []byte) (ProbeResult, error) {
	srv := httptest.NewServer(h)
	return func(ctx context.Context, method, raw string, body []byte) (ProbeResult, error) {
		// The test server has one price for every path. Call it directly
		// so the shopper's URL is what we record, not the test server's.
		_ = raw
		return ProbePayment(ctx, srv.Client(), method, srv.URL, body)
	}
}

func TestQuoteSelectsPulseForBlockDataFeed(t *testing.T) {
	syns, err := LoadSynonyms()
	require.NoError(t, err)
	payee := bech("pulsepayee")
	pulse := listing("https://example.test/svc/pulse", "Aether chain pulse",
		"height, block time, difficulty, block reward", payee, "500", 10)
	miners := listing("https://example.test/svc/miners", "Aether miner leaderboard",
		"this epoch's proof-of-work leaderboard", bech("minerpayee"), "500", 11)
	probes := 0
	res, err := Quote(context.Background(), QuoteInput{
		ChainID: "aether-testnet-1", Listings: []directory.Listing{miners, pulse},
		Synonyms: syns, Policy: testPolicy(), Assets: testAssets(t),
		Request: Request{Query: "block-data feed", MaxPrice: "0.1 AETH"},
		Probe: func(ctx context.Context, method, raw string, body []byte) (ProbeResult, error) {
			probes++
			pay := payee
			if strings.Contains(raw, "miners") {
				pay = miners.Announcer
			}
			srv := httptest.NewServer(serve402(exactReq(pay, "500", "", nil)))
			defer srv.Close()
			return ProbePayment(ctx, srv.Client(), method, srv.URL, body)
		},
	})
	require.NoError(t, err)
	require.NotNil(t, res.Selected)
	require.Equal(t, pulse.URL, res.Selected.URL)
	require.Equal(t, "500", res.Selected.Amount)
	require.Equal(t, CodeNeedsConfirm, res.Decision.Code)
	require.False(t, res.Decision.Spent)
	require.Equal(t, 1, probes, "miners does not match block-data feed")
}

func TestQuoteDoesNotProbeWithoutWords(t *testing.T) {
	payee := bech("hellopayee")
	probes := 0
	res, err := Quote(context.Background(), QuoteInput{
		ChainID: "aether-testnet-1",
		Listings: []directory.Listing{
			listing("https://example.test/svc/hello", "Aether hello", "practice purchase", payee, "1", 3),
		},
		Policy: testPolicy(), Assets: testAssets(t),
		Probe: func(context.Context, string, string, []byte) (ProbeResult, error) {
			probes++
			return ProbeResult{}, nil
		},
	})
	require.NoError(t, err)
	require.Nil(t, res.Selected)
	require.Equal(t, CodeNothingFound, res.Decision.Code)
	require.Equal(t, 0, probes)
	require.Len(t, res.APIs, 1)
	require.False(t, res.APIs[0].Selectable)
}

func TestQuoteDropsSplitsPayeeMismatchAndPriceMismatch(t *testing.T) {
	payee := bech("listedpayee")
	other := bech("otherpayee")
	cases := []struct {
		name   string
		req    x402.PaymentRequirements
		reason string
	}{
		{"splits", exactReq(payee, "500", "", map[string]any{"splits": []any{map[string]any{}}}), "split"},
		{"bad splits", exactReq(payee, "500", "", map[string]any{"splits": "nope"}), "split"},
		{"payee", exactReq(other, "500", "", nil), "different account"},
		{"mismatch", exactReq(payee, "4000", "", nil), "higher than the listed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(serve402(tc.req))
			defer srv.Close()
			res, err := Quote(context.Background(), QuoteInput{
				ChainID: "aether-testnet-1", Assets: testAssets(t), Policy: testPolicy(),
				Listings: []directory.Listing{listing(srv.URL, "Clock", "block time", payee, "500", 1)},
				Request:  Request{Query: "block", MaxPrice: "0.1 AETH"},
				Probe: func(ctx context.Context, method, raw string, body []byte) (ProbeResult, error) {
					return ProbePayment(ctx, srv.Client(), method, srv.URL, body)
				},
			})
			require.NoError(t, err)
			require.Nil(t, res.Selected)
			require.Contains(t, res.APIs[0].Reason, tc.reason)
		})
	}
}

func TestQuoteEmptySplitsIsAllowed(t *testing.T) {
	payee := bech("listedpayee")
	srv := httptest.NewServer(serve402(exactReq(payee, "500", "", map[string]any{"splits": []any{}})))
	defer srv.Close()
	res, err := Quote(context.Background(), QuoteInput{
		ChainID: "aether-testnet-1", Assets: testAssets(t), Policy: testPolicy(),
		Listings: []directory.Listing{listing(srv.URL, "Clock", "block time", payee, "500", 1)},
		Request:  Request{Query: "block"},
		Probe: func(ctx context.Context, method, raw string, body []byte) (ProbeResult, error) {
			return ProbePayment(ctx, srv.Client(), method, srv.URL, body)
		},
	})
	require.NoError(t, err)
	require.NotNil(t, res.Selected)
	require.Equal(t, CodeNeedsConfirm, res.Decision.Code)
}

func TestQuotePaysTheLowerLivePrice(t *testing.T) {
	payee := bech("listedpayee")
	srv := httptest.NewServer(serve402(exactReq(payee, "100", "", nil)))
	defer srv.Close()
	res, err := Quote(context.Background(), QuoteInput{
		ChainID: "aether-testnet-1", Assets: testAssets(t), Policy: testPolicy(),
		Listings: []directory.Listing{listing(srv.URL, "Clock", "block time", payee, "500", 1)},
		Request:  Request{Query: "block"},
		Probe: func(ctx context.Context, method, raw string, body []byte) (ProbeResult, error) {
			return ProbePayment(ctx, srv.Client(), method, srv.URL, body)
		},
	})
	require.NoError(t, err)
	require.Equal(t, "100", res.Selected.Amount)
}

func TestQuoteHistoryBeatsACheaperStranger(t *testing.T) {
	good := bech("goodpayee")
	cheap := bech("cheappayee")
	goodURL := "https://example.test/good"
	cheapURL := "https://example.test/cheap"
	res, err := Quote(context.Background(), QuoteInput{
		ChainID: "aether-testnet-1", Assets: testAssets(t), Policy: testPolicy(),
		Listings: []directory.Listing{
			listing(cheapURL, "Cheap block", "block time", cheap, "100", 1),
			listing(goodURL, "Known block", "block time", good, "500", 2),
		},
		Request: Request{Query: "block", MaxPrice: "0.1 AETH"},
		History: func(u string) Experience {
			if u == goodURL {
				return Experience{Purchases: 4, Failed: 0}
			}
			return Experience{}
		},
		Probe: func(ctx context.Context, method, raw string, body []byte) (ProbeResult, error) {
			pay, amt := cheap, "100"
			if strings.Contains(raw, "good") {
				pay, amt = good, "500"
			}
			return ProbeResult{Status: http.StatusPaymentRequired, Required: &x402.PaymentRequired{
				X402Version: x402.Version, Accepts: []x402.PaymentRequirements{exactReq(pay, amt, "", nil)},
			}}, nil
		},
	})
	require.NoError(t, err)
	require.Equal(t, goodURL, res.Selected.URL)
}

func TestQuoteTrustedRatingSortsUnknownDoesNot(t *testing.T) {
	payer := bech("payer")
	stranger := bech("stranger")
	trustedURL := "https://example.test/trusted"
	plainURL := "https://example.test/plain"
	pol := testPolicy()
	pol.Payer = payer
	rated := listing(trustedURL, "Rated block", "block time", bech("ratedpayee"), "500", 5)
	rated.Reputation = &directory.Reputation{Ratings: []directory.Rating{
		{Rater: payer, URL: trustedURL, Score: 5, Height: 9},
		{Rater: stranger, URL: plainURL, Score: 5, Height: 9},
	}}
	plain := listing(plainURL, "Plain block", "block time", bech("plainpayee"), "500", 4)
	plain.Reputation = &directory.Reputation{Ratings: []directory.Rating{
		{Rater: stranger, URL: plainURL, Score: 5, Height: 8},
	}}
	res, err := Quote(context.Background(), QuoteInput{
		ChainID: "aether-testnet-1", Assets: testAssets(t), Policy: pol,
		Listings: []directory.Listing{plain, rated},
		Request:  Request{Query: "block"},
		Probe: func(ctx context.Context, method, raw string, body []byte) (ProbeResult, error) {
			pay := plain.Announcer
			if strings.Contains(raw, "trusted") {
				pay = rated.Announcer
			}
			return ProbeResult{Status: http.StatusPaymentRequired, Required: &x402.PaymentRequired{
				X402Version: x402.Version, Accepts: []x402.PaymentRequirements{exactReq(pay, "500", "", nil)},
			}}, nil
		},
	})
	require.NoError(t, err)
	require.Equal(t, trustedURL, res.Selected.URL)
	require.Greater(t, res.Selected.Score, 0.0)
}

func TestQuoteUSDCIsNotSelectedForAnAETHSearch(t *testing.T) {
	usdc, ok := testAssets(t).BySymbol("USDC")
	require.True(t, ok)
	payee := bech("usdcpayee")
	res, err := Quote(context.Background(), QuoteInput{
		ChainID: "aether-testnet-1", Assets: testAssets(t), Policy: testPolicy(),
		Listings: []directory.Listing{{
			Announcement: directory.Announcement{URL: "https://example.test/usdc", Announcer: payee, Height: 1},
			Manifest: paywall.Manifest{
				Name: "Hello USDC", Description: "practice", PayTo: payee, Price: "1000",
				Asset: usdc.Denom, Network: "aether-testnet-1",
			},
		}},
		Request: Request{Query: "practice", MaxPrice: "0.1 AETH"},
		Probe: func(context.Context, string, string, []byte) (ProbeResult, error) {
			return ProbeResult{Status: http.StatusPaymentRequired, Required: &x402.PaymentRequired{
				X402Version: x402.Version, Accepts: []x402.PaymentRequirements{exactReq(payee, "1000", usdc.Denom, nil)},
			}}, nil
		},
	})
	require.NoError(t, err)
	require.Nil(t, res.Selected)
	require.False(t, res.APIs[0].Selectable)
}

func TestQuoteOverCapIsNotApprovable(t *testing.T) {
	payee := bech("pulsepayee")
	pol := testPolicy()
	pol.MaxPerPayment = sdkmath.NewInt(1) // 1 uaeth
	res, err := Quote(context.Background(), QuoteInput{
		ChainID: "aether-testnet-1", Assets: testAssets(t), Policy: pol,
		Listings: []directory.Listing{listing("https://example.test/pulse", "Pulse", "block time", payee, "500", 1)},
		Request:  Request{Query: "block", MaxPrice: "0.1 AETH"},
		Probe: func(context.Context, string, string, []byte) (ProbeResult, error) {
			return ProbeResult{Status: http.StatusPaymentRequired, Required: &x402.PaymentRequired{
				X402Version: x402.Version, Accepts: []x402.PaymentRequirements{exactReq(payee, "500", "", nil)},
			}}, nil
		},
	})
	require.NoError(t, err)
	require.Equal(t, CodeOverCap, res.Decision.Code)
	require.False(t, res.Selected.Buyable)
	require.False(t, res.Decision.Spent)
}

func TestQuoteAutoStillReportsInPolicyWithoutSpending(t *testing.T) {
	payee := bech("hellopayee")
	pol := testPolicy()
	pol.Auto = true
	res, err := Quote(context.Background(), QuoteInput{
		ChainID: "aether-testnet-1", Assets: testAssets(t), Policy: pol,
		Listings: []directory.Listing{listing("https://example.test/hello", "Hello", "practice", payee, "1", 1)},
		Request:  Request{Query: "practice"},
		Probe: func(context.Context, string, string, []byte) (ProbeResult, error) {
			return ProbeResult{Status: http.StatusPaymentRequired, Required: &x402.PaymentRequired{
				X402Version: x402.Version, Accepts: []x402.PaymentRequirements{exactReq(payee, "1", "", nil)},
			}}, nil
		},
	})
	require.NoError(t, err)
	require.Equal(t, CodeInPolicy, res.Decision.Code)
	require.False(t, res.Decision.Spent, "a quote never spends, even when auto is on")
}

func TestQuoteJobsAreShownAndNotBuyable(t *testing.T) {
	syns, err := LoadSynonyms()
	require.NoError(t, err)
	res, err := Quote(context.Background(), QuoteInput{
		ChainID: "aether-testnet-1", Assets: testAssets(t), Policy: testPolicy(), Synonyms: syns,
		Jobs: []JobOffer{{
			ID: "0123456789abcdef", Title: "Name a cat", Description: "three names",
			Price: "0.002 AETH", Seller: bech("seller"),
		}},
		Request: Request{Query: "cat"},
	})
	require.NoError(t, err)
	require.Nil(t, res.Selected)
	require.Len(t, res.Jobs, 1)
	require.False(t, res.Jobs[0].Buyable)
	require.Equal(t, CodeNotBuyable, testPolicy().Judge(res.Jobs[0]).Code)
}

func TestFetchOpenJobs(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/jobs", func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Query().Get("q"), "cat") && r.URL.Query().Get("q") != "" {
			_, _ = w.Write([]byte(`{"jobs":[]}`))
			return
		}
		_, _ = w.Write([]byte(`{"jobs":[{"id":"0123456789abcdef","title":"Name a cat"}]}`))
	})
	mux.HandleFunc("/jobs/0123456789abcdef", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"id":"0123456789abcdef","offers":[{"seller":"aether1seller","price":"0.002 AETH","note":"I can do it"}],"spec":{"title":"Name a cat","description":"three names","tags":["names"]}}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	got, err := FetchOpenJobs(context.Background(), srv.Client(), srv.URL+"/jobs", []string{"cat"})
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, "0.002 AETH", got[0].Price)
	require.Equal(t, "I can do it", got[0].Note)
}

func TestQuoteProbeCap(t *testing.T) {
	var listings []directory.Listing
	for i := 0; i < MaxProbes+1; i++ {
		listings = append(listings, listing(
			"https://example.test/s/"+string(rune('a'+i%26)),
			"Block feed", "block time service number", bech("payee"), "500", int64(i+1),
		))
	}
	// Distinct URLs: the helper above collides. Rebuild.
	listings = nil
	for i := 0; i < MaxProbes+1; i++ {
		listings = append(listings, listing(
			"https://example.test/s/"+strings.Repeat("x", i+1),
			"Block feed", "block time", bech("payee"), "500", int64(i+1),
		))
	}
	probes := 0
	_, err := Quote(context.Background(), QuoteInput{
		ChainID: "aether-testnet-1", Assets: testAssets(t), Policy: testPolicy(),
		Listings: listings, Request: Request{Query: "block"},
		Probe: func(context.Context, string, string, []byte) (ProbeResult, error) {
			probes++
			return ProbeResult{Status: http.StatusPaymentRequired, Required: &x402.PaymentRequired{
				X402Version: x402.Version, Accepts: []x402.PaymentRequirements{exactReq(bech("payee"), "500", "", nil)},
			}}, nil
		},
	})
	require.NoError(t, err)
	require.Equal(t, MaxProbes, probes)
}
