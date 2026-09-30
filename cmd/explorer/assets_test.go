package main

import (
	"testing"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	"github.com/whoyoujoshin/aether/wallet"
)

func TestCoinDTOsLabelOnlyKnownAssets(t *testing.T) {
	t.Cleanup(func() { assets, _ = wallet.NewAssets("") })
	usdc, err := wallet.USDC("channel-3")
	require.NoError(t, err)
	lookalike, err := wallet.USDC("channel-9")
	require.NoError(t, err)
	coins := sdk.NewCoins(sdk.NewInt64Coin(usdc.Denom, 5_000_000), sdk.NewInt64Coin(lookalike.Denom, 1))

	// Not told about USDC: AETH (zero) first, then both tokens by denom only.
	got := coinDTOs(coins)
	require.Equal(t, coinDTO{Denom: "uaeth", Amount: "0", Symbol: "AETH", Decimals: 6, Origin: "Aether"}, got[0])
	require.Len(t, got, 3)
	require.Empty(t, got[1].Symbol)
	require.Empty(t, got[2].Symbol)

	assets, err = wallet.NewAssets("channel-3")
	require.NoError(t, err)
	got = coinDTOs(coins)
	require.Len(t, got, 3)
	require.Equal(t, coinDTO{Denom: usdc.Denom, Amount: "5000000", Symbol: "USDC", Decimals: 6, Origin: "Noble over transfer/channel-3"}, got[1])
	require.Equal(t, coinDTO{Denom: lookalike.Denom, Amount: "1"}, got[2], "USDC that came another way is never labeled USDC")
}
