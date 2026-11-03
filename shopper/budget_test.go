package shopper

import (
	"context"
	"testing"

	sdkmath "cosmossdk.io/math"
	"github.com/stretchr/testify/require"

	"github.com/whoyoujoshin/aether/directory"
)

func TestSearchDoesNotProbe(t *testing.T) {
	payee := bech("hellopayee")
	probes := 0
	res, err := Quote(context.Background(), QuoteInput{
		ChainID: "aether-testnet-1", SearchOnly: true, Assets: testAssets(t), Policy: testPolicy(),
		Listings: []directory.Listing{listing("https://example.test/hello", "Aether hello", "practice purchase", payee, "1", 1)},
		Request:  Request{Query: "practice"},
		Probe: func(context.Context, string, string, []byte) (ProbeResult, error) {
			probes++
			return ProbeResult{}, nil
		},
	})
	require.NoError(t, err)
	require.Equal(t, 0, probes)
	require.NotEmpty(t, res.APIs)
	require.False(t, res.APIs[0].Selectable, "a search has no live price, so it cannot be bought")
}

func TestAgentCapCannotExceedTheAccount(t *testing.T) {
	p := testPolicy()
	assets := testAssets(t)
	caps, err := ParseAgentCaps([]string{"research=0.02 AETH", "greedy=10 AETH"}, assets)
	require.NoError(t, err)
	tight := p.ForAgent("research", caps)
	require.True(t, tight.DailyCap.Equal(sdkmath.NewInt(20_000)))
	wide := p.ForAgent("greedy", caps)
	require.True(t, wide.DailyCap.Equal(p.DailyCap), "a named cap above the account cap does not raise it")
	require.True(t, p.ForAgent("unknown", caps).DailyCap.Equal(p.DailyCap))
}
