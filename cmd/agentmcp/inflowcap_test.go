package main

import (
	"context"
	"testing"

	"cosmossdk.io/math"
	"github.com/stretchr/testify/require"

	"github.com/whoyoujoshin/aether/wallet"
)

func osmoCap(room int64) wallet.InflowCaps {
	return wallet.InflowCaps{Active: true, Caps: []wallet.InflowCap{{
		Channel: testnetOsmosisChannel, Denom: "uosmo", VoucherDenom: osmoOnAether,
		Cap: math.NewInt(1_000_000_000), Supply: math.NewInt(1_000_000_000 - room), Room: math.NewInt(room),
	}}}
}

// A swap whose OSMO couldn't come back onto Aether isn't sent: the
// return would be refused and the OSMO left owed in the contract.
func TestSwapAeth_InflowCap(t *testing.T) {
	s, _ := setupSwap(t)

	s.caps = osmoCap(100_000) // the swap brings back ~394,472 uosmo
	_, err := swap(t, swapAethInput{Amount: "1 AETH", IdempotencyKey: "cap-1"})
	requireCode(t, err, codeInflowCapped)
	require.Contains(t, err.Error(), "room for 0.1 OSMO")
	require.Empty(t, s.transfers, "nothing sent")
	require.Zero(t, spent(t), "nothing counted against the limits")

	s.caps = wallet.InflowCaps{Active: true} // nothing listed
	_, err = swap(t, swapAethInput{Amount: "1 AETH", IdempotencyKey: "cap-2"})
	requireCode(t, err, codeInflowCapped)
	require.Contains(t, err.Error(), "isn't on Aether's inflow allowlist")
	require.Empty(t, s.transfers)

	s.caps = osmoCap(394_472) // exactly enough room
	out, err := swap(t, swapAethInput{Amount: "1 AETH", IdempotencyKey: "cap-3"})
	require.NoError(t, err)
	require.Equal(t, statusSwapped, out.Status, out.Message)
	require.Len(t, s.transfers, 1)
}

func TestSwapFromTestWallet_InflowCap(t *testing.T) {
	s, token, _ := setupSandboxSwap(t)
	s.caps = osmoCap(1_000)
	_, err := sandboxSwap(t, swapFromTestWalletInput{WalletToken: token, Amount: "1 AETH"})
	requireCode(t, err, codeInflowCapped)
	require.Empty(t, s.transfers)
}

func TestGetInflowCaps(t *testing.T) {
	s, _ := setupSwap(t)
	usdc := useUSDC(t, "10 USDC", "50 USDC", "")

	_, out, err := toolGetInflowCaps(context.Background(), nil, getInflowCapsInput{})
	require.NoError(t, err)
	require.False(t, out.Active)
	require.Empty(t, out.Caps)
	require.Contains(t, out.Message, "no inflow cap yet")

	s.caps = osmoCap(250_000)
	s.caps.Caps = append(s.caps.Caps, wallet.InflowCap{
		Channel: "channel-3", Denom: "uusdc", VoucherDenom: usdc.Denom,
		Cap: math.NewInt(5_000_000_000), Supply: math.NewInt(10_000_000), Room: math.NewInt(4_990_000_000),
	}, wallet.InflowCap{
		Channel: "channel-9", Denom: "uatom", VoucherDenom: "ibc/ATOM", Cap: math.NewInt(5), Supply: math.ZeroInt(), Room: math.NewInt(5),
	})
	_, out, err = toolGetInflowCaps(context.Background(), nil, getInflowCapsInput{})
	require.NoError(t, err)
	require.True(t, out.Active)
	require.Len(t, out.Caps, 3)
	require.Equal(t, "OSMO", out.Caps[0].Symbol)
	require.Equal(t, "0.25 OSMO", out.Caps[0].RoomDisplay)
	require.Equal(t, "USDC", out.Caps[1].Symbol)
	require.Equal(t, "4990 USDC", out.Caps[1].RoomDisplay)
	require.Equal(t, "4990000000", out.Caps[1].Room)
	require.Empty(t, out.Caps[2].Symbol, "a token this server doesn't know isn't named")
	require.Contains(t, out.Message, "refused")
}
