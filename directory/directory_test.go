package directory

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"strings"
	"sync/atomic"
	"testing"

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

func addr(s string) string { return sdk.AccAddress(s + strings.Repeat("_", 20-len(s))).String() }

func pay(height int64, from, memo string, uaeth int64, code uint32) wallet.IncomingPayment {
	return wallet.IncomingPayment{Hash: fmt.Sprintf("TX%d", height), Height: height, From: from, Memo: memo, Code: code,
		Amount: sdk.NewCoins(sdk.NewInt64Coin("uaeth", uaeth))}
}

func TestAddress_IsFixedAndKeyless(t *testing.T) {
	require.Equal(t, Address(), Address())
	require.True(t, strings.HasPrefix(Address(), "aether1"))
}

func TestNormalizeURL(t *testing.T) {
	for in, want := range map[string]string{
		"https://Weather.Example/":     "https://weather.example",
		" http://api.example:8402/v1 ": "http://api.example:8402/v1",
	} {
		got, err := NormalizeURL(in)
		require.NoError(t, err, in)
		require.Equal(t, want, got)
	}
	for _, bad := range []string{"", "ftp://x", "//x", "https://u:p@x", "https://x/?a=1", "https://x/#f", "https://" + strings.Repeat("a", 250)} {
		_, err := NormalizeURL(bad)
		require.Error(t, err, bad)
	}
}

func TestAnnouncements(t *testing.T) {
	alice, bob := addr("alice"), addr("bob")
	got := Announcements([]wallet.IncomingPayment{
		pay(1, alice, AnnouncePrefix+"https://a.example", 1, 0),
		pay(2, bob, AnnouncePrefix+"https://b.example/", 1, 0),
		pay(3, alice, AnnouncePrefix+"https://gone.example", 1, 0),
		pay(4, alice, DelistPrefix+"https://gone.example", 1, 0),
		pay(5, bob, DelistPrefix+"https://a.example", 1, 0), // only alice can delist alice's listing
		pay(6, alice, AnnouncePrefix+"https://failed.example", 1, 5),
		pay(7, alice, AnnouncePrefix+"https://free.example", 0, 0),
		pay(8, alice, AnnouncePrefix+"not a url", 1, 0),
		pay(9, alice, "hello", 1, 0),
		pay(10, alice, AnnouncePrefix+"https://a.example", 1, 0), // re-announced: newest wins
	})
	require.Len(t, got, 2)
	require.Equal(t, Announcement{URL: "https://a.example", Announcer: alice, TxHash: "TX10", Height: 10}, got[0])
	require.Equal(t, "https://b.example", got[1].URL)
}

func manifestServer(t *testing.T, m paywall.Manifest, hits *atomic.Int32) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits != nil {
			hits.Add(1)
		}
		if r.URL.Path != paywall.ManifestPath {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(m)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestSafeFetcher_RefusesInternalAddresses(t *testing.T) {
	var hits atomic.Int32
	srv := manifestServer(t, paywall.Manifest{X402Version: 1}, &hits)

	_, err := SafeFetcher(false)(context.Background(), srv.URL)
	require.ErrorIs(t, err, ErrPrivateAddress)
	require.Zero(t, hits.Load(), "an internal address must never be contacted")

	m, err := SafeFetcher(true)(context.Background(), srv.URL)
	require.NoError(t, err)
	require.Equal(t, 1, m.X402Version)

	// A public-looking name can't redirect into the internal network.
	redirect := httptest.NewServer(http.RedirectHandler(srv.URL+paywall.ManifestPath, http.StatusFound))
	defer redirect.Close()
	_, err = SafeFetcher(true)(context.Background(), redirect.URL)
	require.ErrorContains(t, err, "HTTP 302")
}

func TestIsInternal(t *testing.T) {
	for _, s := range []string{"127.0.0.1", "10.1.2.3", "172.16.0.1", "192.168.1.1", "169.254.169.254", "100.64.0.1", "0.0.0.0", "::1", "fe80::1", "fc00::1", "::ffff:127.0.0.1"} {
		require.True(t, isInternal(mustAddr(s)), s)
	}
	for _, s := range []string{"8.8.8.8", "157.245.252.221", "2606:4700::1111"} {
		require.False(t, isInternal(mustAddr(s)), s)
	}
}

func TestDirectory_ListsOnlyVerifiedServices(t *testing.T) {
	seller, impostor := addr("seller"), addr("impostor")
	good := manifestServer(t, paywall.Manifest{X402Version: 1, Name: "Weather", Network: "aether-testnet-1", PayTo: seller, Price: "20000"}, nil)
	// Someone announcing a service whose payee isn't them.
	stolen := manifestServer(t, paywall.Manifest{X402Version: 1, Name: "Not yours", Network: "aether-testnet-1", PayTo: seller, Price: "1"}, nil)
	wrongNet := manifestServer(t, paywall.Manifest{X402Version: 1, Network: "other", PayTo: seller, Price: "1"}, nil)

	var scans atomic.Int32
	d := &Directory{
		Network: "aether-testnet-1",
		Fetch:   SafeFetcher(true),
		Scan: func() ([]wallet.IncomingPayment, error) {
			scans.Add(1)
			return []wallet.IncomingPayment{
				pay(1, seller, AnnouncePrefix+good.URL, 1, 0),
				pay(2, impostor, AnnouncePrefix+stolen.URL, 1, 0),
				pay(3, seller, AnnouncePrefix+wrongNet.URL, 1, 0),
				pay(4, seller, AnnouncePrefix+"http://127.0.0.1:1", 1, 0), // unreachable
			}, nil
		},
	}
	got, err := d.Listings(context.Background())
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, "Weather", got[0].Manifest.Name)
	require.Equal(t, seller, got[0].Announcer)

	_, err = d.Listings(context.Background())
	require.NoError(t, err)
	require.Equal(t, int32(1), scans.Load(), "cached within the TTL")
}

func TestPaywallManifest(t *testing.T) {
	seller := addr("seller")
	p, err := paywall.New(paywall.Config{PayTo: seller, Price: sdkInt(20_000), Network: "n",
		Lookup: func(string) (*wallet.TransactionDetail, error) { return nil, nil }})
	require.NoError(t, err)
	srv := httptest.NewServer(p.ManifestHandler("Weather", "forecasts"))
	defer srv.Close()
	m, err := SafeFetcher(true)(context.Background(), srv.URL)
	require.NoError(t, err)
	require.Equal(t, paywall.Manifest{X402Version: 1, Name: "Weather", Description: "forecasts", Network: "n", PayTo: seller,
		Price: "20000", PriceAeth: "0.02", Schemes: []string{paywall.Scheme}}, *m)
	require.NoError(t, Verify(Announcement{Announcer: seller}, m, "n"))
}

func mustAddr(s string) netip.Addr { return netip.MustParseAddr(s) }
