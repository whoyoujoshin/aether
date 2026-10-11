package quest

import (
	"context"
	"os"
	"testing"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	"github.com/whoyoujoshin/aether/app"
	"github.com/whoyoujoshin/aether/directory"
	"github.com/whoyoujoshin/aether/paywall"
	"github.com/whoyoujoshin/aether/wallet"
)

func TestMain(m *testing.M) {
	app.SetAddressPrefixes()
	os.Exit(m.Run())
}

func acct(s string) string { return sdk.AccAddress(s + "____________________"[len(s):]).String() }

func pay(from string, uaeth int64, h int64, memo string) wallet.IncomingPayment {
	return wallet.IncomingPayment{From: from, Amount: sdk.NewCoins(sdk.NewInt64Coin("uaeth", uaeth)), Height: h, Memo: memo, Hash: memo + from}
}

func listing(url, payee, price string, h int64) directory.Listing {
	return directory.Listing{
		Announcement: directory.Announcement{URL: url, Announcer: payee, Height: h},
		Manifest:     paywall.Manifest{Price: price},
	}
}

func TestNameIsWhatTheJoinMemoSays(t *testing.T) {
	for memo, want := range map[string]string{
		"aether-quest:Ada":                         "Ada",
		"aether-quest:  agent-007.v2 ":             "agent-007.v2",
		"aether-quest:<script>":                    "",
		"aether-quest:":                            "",
		"aether-quest:" + string(make([]byte, 40)): "",
	} {
		got, ok := Name(memo)
		require.True(t, ok, memo)
		require.Equal(t, want, got, memo)
	}
	_, ok := Name("x402-rate:5:https://a")
	require.False(t, ok)
}

func TestComputeFindsEachStepOnChain(t *testing.T) {
	seed, ada, bob, eve := acct("seed"), acct("ada"), acct("bob"), acct("eve")
	in := Input{
		StartHeight: 100,
		Joins: []wallet.IncomingPayment{
			pay(ada, 1, 101, "aether-quest:Ada"),
			pay(bob, 1, 102, "aether-quest:Bob"),
			pay(eve, 1, 50, "aether-quest:Early"), // before the quest started
			pay(eve, 0, 103, "aether-quest:Free"), // nothing sent
			pay(ada, 1, 110, "aether-quest:Ada Lovelace"),
		},
		Listings: []directory.Listing{
			listing("https://x/svc/hello", seed, "1", 10),
			listing("https://x/svc/address", seed, "1000", 10),
			listing("https://bob.example", bob, "500", 120),
		},
		Paid: map[string][]wallet.IncomingPayment{
			seed: {pay(ada, 1, 104, ""), pay(ada, 1000, 105, ""), pay(ada, 1, 106, ""), pay(ada, 1, 107, ""), pay(ada, 1, 108, ""), pay(bob, 1, 109, "")},
			bob:  {pay(ada, 499, 121, ""), pay(ada, 500, 122, ""), pay(bob, 500, 123, "")},
		},
		DirectoryPayments: []wallet.IncomingPayment{
			pay(ada, 1, 103, "x402-rate:5:https://x/svc/hello"), // before Ada paid: doesn't count
			pay(bob, 1, 125, "x402-rate:4:https://bob.example"), // rating itself: doesn't count
			pay(ada, 1, 130, "x402-rate:5:https://bob.example"),
		},
	}
	board := Compute(in)
	require.Len(t, board, 2, "eve never joined within the quest")

	// Both did four steps; Bob finished his at 122, Ada hers at 130.
	b, a := board[0], board[1]
	require.Equal(t, ada, a.Address)
	require.Equal(t, "Ada Lovelace", a.Name, "the latest join names it")
	require.Equal(t, map[string]int64{"joined": 101, "bought": 104, "regular": 108, "rated": 130}, a.Done)
	require.Equal(t, 6, a.Purchases, "5 from the seed and 1 from Bob (499 isn't his price)")
	require.Equal(t, 4, a.Score)
	require.Equal(t, int64(130), a.ReachedHeight)
	require.Equal(t, 2, a.Rank)

	require.Equal(t, bob, b.Address)
	require.Equal(t, map[string]int64{"joined": 102, "bought": 109, "seller": 120, "earned": 122}, b.Done,
		"Bob's own payment to himself earns nothing")
	require.Equal(t, 1, b.Rank)
}

func TestRankingBreaksTiesByWhoGotThereFirst(t *testing.T) {
	seed, a, b := acct("seed"), acct("a"), acct("b")
	board := Compute(Input{
		Joins:    []wallet.IncomingPayment{pay(a, 1, 1, "aether-quest:A"), pay(b, 1, 2, "aether-quest:B")},
		Listings: []directory.Listing{listing("https://x", seed, "1", 1)},
		Paid:     map[string][]wallet.IncomingPayment{seed: {pay(b, 1, 5, ""), pay(a, 1, 9, "")}},
	})
	require.Equal(t, b, board[0].Address, "same score; b bought first")
	require.Equal(t, a, board[1].Address)
}

type fakeSource struct {
	incoming map[string][]wallet.IncomingPayment
	listings []directory.Listing
	scanned  []string
}

func (f *fakeSource) Incoming(_ context.Context, a string, since int64) ([]wallet.IncomingPayment, error) {
	f.scanned = append(f.scanned, a)
	var out []wallet.IncomingPayment
	for _, p := range f.incoming[a] {
		if p.Height >= since {
			out = append(out, p)
		}
	}
	return out, nil
}

func (f *fakeSource) Listings(context.Context) ([]directory.Listing, error) { return f.listings, nil }

func TestLoadScansOneAddressPerPayee(t *testing.T) {
	seed, ada := acct("seed"), acct("ada")
	src := &fakeSource{
		incoming: map[string][]wallet.IncomingPayment{
			Address(): {pay(ada, 1, 10, "aether-quest:Ada")},
			seed:      {pay(ada, 1, 11, "")},
		},
		listings: []directory.Listing{listing("https://x/a", seed, "1", 1), listing("https://x/b", seed, "5", 1)},
	}
	b, err := Load(context.Background(), src, 0)
	require.NoError(t, err)
	require.Equal(t, []string{Address(), seed, directory.Address()}, src.scanned, "two listings, one payee, one scan")
	require.Equal(t, Steps, b.Steps)
	p := b.Find(ada)
	require.NotNil(t, p)
	require.Equal(t, 2, p.Score)
	require.Nil(t, b.Find(seed))
}
