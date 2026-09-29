package main

import (
	"context"
	"fmt"
	"testing"

	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	"github.com/stretchr/testify/require"

	"github.com/whoyoujoshin/aether/wallet"
)

// useUSDC turns USDC on (over channel-3) with the given limits, and back
// off after the test.
func useUSDC(t *testing.T, perTx, daily, threshold string) wallet.Asset {
	t.Helper()
	require.NoError(t, configureUSDC("channel-3", perTx, daily, threshold))
	t.Cleanup(func() {
		assets, _ = wallet.NewAssets("")
		usdcPerTxLimit, usdcDailyLimit, usdcApprovalThreshold = 0, 0, math.Int{}
	})
	usdc, ok := assets.BySymbol("USDC")
	require.True(t, ok)
	return usdc
}

// sentCoins is what the last broadcast transaction's MsgSend moves.
func sentCoins(t *testing.T, f *fakeChain) sdk.Coins {
	t.Helper()
	f.mu.Lock()
	bz := f.broadcasts[len(f.broadcasts)-1]
	f.mu.Unlock()
	msgs := decodeTx(t, bz).GetMsgs()
	require.Len(t, msgs, 1)
	return msgs[0].(*banktypes.MsgSend).Amount
}

func TestUSDC_OffUntilTheOwnerEnablesIt(t *testing.T) {
	setupAgent(t)

	// No channel: USDC is an unknown unit, never guessed at.
	_, err := send(t, "k1", "5 USDC", "")
	requireCode(t, err, codeInvalidAmount)

	// A channel but no limits: known, but not spendable.
	useUSDC(t, "", "", "")
	_, err = send(t, "k2", "5 USDC", "")
	requireCode(t, err, codeAssetNotEnabled)

	// AETH is unaffected either way.
	_, err = send(t, "k3", "0.5 AETH", "")
	require.NoError(t, err)
}

func TestUSDC_SendsTheRightDenomUnderItsOwnLimits(t *testing.T) {
	f := setupAgent(t)
	usdc := useUSDC(t, "5 USDC", "12 USDC", "")

	_, err := send(t, "too-big", "6 USDC", "")
	requireCode(t, err, codePerTxLimit)

	out, err := send(t, "u1", "5 USDC", "invoice 1")
	require.NoError(t, err)
	require.Equal(t, amountDTO{Asset: "USDC", Amount: "5", Base: "5000000", Denom: usdc.Denom}, out.Amount)
	require.Equal(t, sdk.NewCoins(sdk.NewInt64Coin(usdc.Denom, 5_000_000)), sentCoins(t, f), "a USDC payment moves USDC, never uaeth")

	_, err = send(t, "u2", "5000000uusdc", "")
	require.NoError(t, err)
	_, err = send(t, "u3", "3 USDC", "")
	ae := requireCode(t, err, codeDailyLimit)
	require.Contains(t, ae.Message, "12 USDC")

	// AETH has its own, untouched budget: all five 1 AETH payments fit.
	for i := 0; i < 5; i++ {
		_, err = send(t, fmt.Sprintf("a%d", i), "1 AETH", "")
		require.NoError(t, err)
	}
	require.Equal(t, sdk.NewCoins(sdk.NewInt64Coin("uaeth", 1_000_000)), sentCoins(t, f))

	_, status, err := toolGetSpendingStatus(context.Background(), nil, getSpendingStatusInput{})
	require.NoError(t, err)
	require.Equal(t, "5", status.SpentLast24h.Aeth, "the AETH fields stay AETH-only")
	require.Len(t, status.Assets, 2)
	u := status.Assets[1]
	require.Equal(t, "USDC", u.Asset)
	require.True(t, u.Enabled)
	require.Equal(t, "10", u.SpentLast24h.Amount)
	require.Equal(t, "2", u.Remaining.Amount)
}

func TestUSDC_ReplayMustMatchTheAsset(t *testing.T) {
	setupAgent(t)
	useUSDC(t, "5 USDC", "12 USDC", "")

	_, err := send(t, "k", "1 USDC", "")
	require.NoError(t, err)
	// Same key, same number of base units, other asset: a different payment.
	_, err = send(t, "k", "1000000uaeth", "")
	requireCode(t, err, codeIdempotencyConflict)
	out, err := send(t, "k", "1000000uusdc", "")
	require.NoError(t, err)
	require.True(t, out.Replayed)
}

func TestUSDC_ApprovalsHaveTheirOwnThreshold(t *testing.T) {
	setupAgent(t)
	o := setupApprovals(t, 5_000_000) // AETH above 5 AETH needs approval
	useUSDC(t, "50 USDC", "100 USDC", "10 USDC")

	out, err := send(t, "u-big", "20 USDC", "")
	require.NoError(t, err)
	require.Equal(t, statusPendingApproval, out.Status)
	require.Contains(t, out.Message, "20 USDC")
	require.Contains(t, out.Message, "10 USDC")

	// Below the USDC threshold goes straight through; so does AETH below its own.
	out, err = send(t, "u-small", "8 USDC", "")
	require.NoError(t, err)
	require.NotEqual(t, statusPendingApproval, out.Status)
	out, err = send(t, "a", "0.5 AETH", "")
	require.NoError(t, err)
	require.NotEqual(t, statusPendingApproval, out.Status)

	// The owner's decision is bound to the asset.
	st, err := loadState()
	require.NoError(t, err)
	req := st.Approvals["u-big"]
	usdc, _ := assets.BySymbol("USDC")
	require.Equal(t, usdc.Denom, req.Denom)
	o.decide(t, "approve", req.ID)
	out, err = send(t, "u-big", "20 USDC", "")
	require.NoError(t, err)
	require.Equal(t, statusPending, out.Status)
}

func TestUSDC_InvoicesMatchOnlyTheirAsset(t *testing.T) {
	f := setupAgent(t)
	usdc := useUSDC(t, "", "", "") // receiving needs no spending limits
	f.payments = []wallet.IncomingPayment{
		{Hash: "AETH", Height: 101, Memo: "inv-1", Amount: sdk.NewCoins(sdk.NewInt64Coin("uaeth", 9_000_000))},
		{Hash: "USDC", Height: 102, Memo: "inv-1", Amount: sdk.NewCoins(sdk.NewInt64Coin(usdc.Denom, 2_000_000))},
	}
	found, err := findPayment(f, "addr", "inv-1", usdc, math.NewInt(2_000_000), 100)
	require.NoError(t, err)
	require.NotNil(t, found)
	require.Equal(t, "USDC", found.TxHash, "9 AETH doesn't pay a 2 USDC invoice")
	require.Equal(t, "2", found.Amount.Amount)
	require.Equal(t, "USDC", found.Amount.Asset)

	found, err = findPayment(f, "addr", "inv-1", usdc, math.NewInt(3_000_000), 100)
	require.NoError(t, err)
	require.Nil(t, found)
}

func TestConfigureUSDC_RefusesMistakes(t *testing.T) {
	t.Cleanup(func() {
		assets, _ = wallet.NewAssets("")
		usdcPerTxLimit, usdcDailyLimit, usdcApprovalThreshold = 0, 0, math.Int{}
	})
	for name, c := range map[string][4]string{
		"limits without a channel": {"", "5 USDC", "10 USDC", ""},
		"limit in AETH":            {"channel-3", "5 AETH", "10 USDC", ""},
		"only one limit":           {"channel-3", "5 USDC", "", ""},
		"bad channel":              {"3", "", "", ""},
		"threshold with no owner":  {"channel-3", "5 USDC", "10 USDC", "4 USDC"},
	} {
		usdcPerTxLimit, usdcDailyLimit = 0, 0
		require.Error(t, configureUSDC(c[0], c[1], c[2], c[3]), name)
	}
}
