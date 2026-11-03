package app

import (
	"testing"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"
)

func TestTxPriority_MultipleOfTheMinimum(t *testing.T) {
	usdc := "ibc/064D82A6"
	floor, err := sdk.ParseDecCoins("0.0001uaeth,0.0025" + usdc)
	require.NoError(t, err)
	fee := func(s string) sdk.Coins { c, err := sdk.ParseCoinsNormalized(s); require.NoError(t, err); return c }

	// The minimum is 1,000,000 in either denom; twice it is 2,000,000.
	require.Equal(t, int64(1_000_000), txPriority(floor, fee("40uaeth"), 400_000))
	require.Equal(t, int64(1_000_000), txPriority(floor, fee("1000"+usdc), 400_000))
	require.Equal(t, int64(2_000_000), txPriority(floor, fee("80uaeth"), 400_000))
	// The better denom counts; one the node doesn't price counts for nothing.
	require.Equal(t, int64(3_000_000), txPriority(floor, fee("40uaeth,3000"+usdc), 400_000))
	require.Equal(t, int64(0), txPriority(floor, fee("1000000uother"), 400_000))
	// A tiny tip still ranks: the SDK's integer price would be 0 for all of these.
	require.Greater(t, txPriority(floor, fee("41uaeth"), 400_000), txPriority(floor, fee("40uaeth"), 400_000))
	// No minimum: price per gas in millionths.
	require.Equal(t, int64(100), txPriority(nil, fee("40uaeth"), 400_000))
	require.Equal(t, int64(0), txPriority(nil, nil, 400_000))
	// Never at or past the mining priority.
	require.Equal(t, MiningPriority-1, txPriority(floor, fee("9999999999999999999999uaeth"), 1))
}

type opts map[string]interface{}

func (o opts) Get(k string) interface{} { return o[k] }

func TestTippingMempool_Size(t *testing.T) {
	for _, c := range []struct {
		opt  interface{}
		want int
	}{{nil, DefaultMempoolMaxTxs}, {-1, DefaultMempoolMaxTxs}, {0, 0}, {20_000, 20_000}, {"7", 7}} {
		o := opts{}
		if c.opt != nil {
			o["mempool.max-txs"] = c.opt
		}
		require.Equal(t, c.want, mempoolMaxTxs(o), "%v", c.opt)
	}
}
