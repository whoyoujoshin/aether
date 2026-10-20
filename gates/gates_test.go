package gates_test

import (
	"testing"

	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	"github.com/whoyoujoshin/aether/gates"
)

func ctxAt(chainID string, height int64) sdk.Context {
	// As baseapp builds a block's context: the chain ID comes from the header.
	return sdk.NewContext(nil, cmtproto.Header{ChainID: chainID, Height: height}, false, nil)
}

func TestHeight(t *testing.T) {
	for _, id := range []string{gates.MainnetChainID, "aether-rc-1", "aether-rc-dev"} {
		require.True(t, gates.Fresh(id), id)
		require.Equal(t, gates.FreshFrom, gates.Height(id, 396_000), id)
	}
	for _, id := range []string{gates.TestnetChainID, "", "aether-devnet-1", "aether-10", "testchain-0"} {
		require.False(t, gates.Fresh(id), id)
		require.Equal(t, int64(396_000), gates.Height(id, 396_000), id)
	}
}

func TestOnAndAt(t *testing.T) {
	require.False(t, gates.On(ctxAt(gates.TestnetChainID, 395_999), 396_000))
	require.True(t, gates.On(ctxAt(gates.TestnetChainID, 396_000), 396_000))
	require.True(t, gates.At(ctxAt(gates.TestnetChainID, 396_000), 396_000))
	require.False(t, gates.At(ctxAt(gates.TestnetChainID, 396_001), 396_000))

	require.True(t, gates.On(ctxAt(gates.MainnetChainID, 1), 396_000))
	require.True(t, gates.At(ctxAt(gates.MainnetChainID, 1), 396_000), "a once-only change runs in block 1")
	require.False(t, gates.At(ctxAt(gates.MainnetChainID, 396_000), 396_000), "and never at the testnet height")
}
