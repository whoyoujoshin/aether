package app

import (
	"testing"
	"time"

	sdkmath "cosmossdk.io/math"
	abci "github.com/cometbft/cometbft/abci/types"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	dbm "github.com/cosmos/cosmos-db"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	"github.com/stretchr/testify/require"

	"github.com/whoyoujoshin/aether/x/pow"
	"github.com/whoyoujoshin/aether/x/treasury"
)

// A stand-in for the USDC voucher: what denom it is doesn't matter here.
const testUSDC = "ibc/064D82A67318DD30F54A4E17B6E487C2864E8B075A5871D3ACAD7A7129F32C5C"

// payFees puts coins in the fee collector the way x/auth's fee
// deduction does: from an account to the fee_collector module.
func payFees(t *testing.T, app *App, ctx sdk.Context, coins sdk.Coins) {
	t.Helper()
	payer := sdk.AccAddress("fee_test_payer______")
	require.NoError(t, app.BankKeeper.MintCoins(ctx, pow.ModuleName, coins))
	require.NoError(t, app.BankKeeper.SendCoinsFromModuleToAccount(ctx, pow.ModuleName, payer, coins))
	require.NoError(t, app.BankKeeper.SendCoinsFromAccountToModule(ctx, payer, authtypes.FeeCollectorName, coins))
}

func balances(app *App, ctx sdk.Context, module string) sdk.Coins {
	return app.BankKeeper.GetAllBalances(ctx, authtypes.NewModuleAddress(module))
}

// Before the height fees stay in the fee collector, as they always have.
// The activation block moves the backlog, in every denom, to the
// treasury and credits the uaeth to its ledger; every block after moves
// that block's fees.
func TestFeesToTreasury_Activation(t *testing.T) {
	const activation = 3
	defer func(orig int64) { feesToTreasuryActivationHeight = orig }(feesToTreasuryActivationHeight)
	feesToTreasuryActivationHeight = activation

	app := newTestApp(t, dbm.NewMemDB())
	initTestChain(t, app)

	require.NoError(t, finalizeBlock(app, 1))
	ctx := app.NewUncachedContext(false, cmtproto.Header{Height: 1, Time: testGenesisTime.Add(time.Minute)})
	backlog := sdk.NewCoins(sdk.NewInt64Coin("uaeth", 16_374), sdk.NewInt64Coin(testUSDC, 2_500))
	payFees(t, app, ctx, backlog)
	ledgerBefore := app.TreasuryKeeper.GetTreasuryBalance(ctx)
	treasuryBefore := balances(app, ctx, treasury.ModuleName)
	_, err := app.Commit()
	require.NoError(t, err)

	check := func(h int64) sdk.Context {
		return app.NewUncachedContext(false, cmtproto.Header{Height: h, Time: testGenesisTime.Add(time.Duration(h) * time.Minute)})
	}
	// Block 2 ends with fees in the collector, below the height.
	finalizeAndCommit(t, app, 2)
	require.Equal(t, backlog, balances(app, check(3), authtypes.FeeCollectorName), "before the height fees stay put")
	require.Equal(t, ledgerBefore, app.TreasuryKeeper.GetTreasuryBalance(check(3)))

	res, err := app.FinalizeBlock(&abci.RequestFinalizeBlock{Height: activation, Time: testGenesisTime.Add(activation * time.Minute)})
	require.NoError(t, err)
	_, err = app.Commit()
	require.NoError(t, err)
	ctx = check(activation + 1)
	require.True(t, balances(app, ctx, authtypes.FeeCollectorName).IsZero(), "the backlog moved")
	require.Equal(t, treasuryBefore.Add(backlog...), balances(app, ctx, treasury.ModuleName))
	require.Equal(t, ledgerBefore.AddRaw(16_374), app.TreasuryKeeper.GetTreasuryBalance(ctx), "only uaeth is on the ledger")
	require.True(t, hasEvent(res.Events, EventTypeFeesToTreasury, backlog.String()), "the move is in an event")

	res2, err := treasury.NewQueryServerImpl(app.TreasuryKeeper).Balance(ctx, &treasury.QueryBalanceRequest{})
	require.NoError(t, err)
	require.Equal(t, sdkmath.NewInt(2_500), res2.RealBankBalances.AmountOf(testUSDC), "the query shows the USDC")

	// A later block's fees move at its end.
	require.NoError(t, finalizeBlock(app, activation+1))
	ctx = check(activation + 1)
	payFees(t, app, ctx, sdk.NewCoins(sdk.NewInt64Coin("uaeth", 40)))
	_, err = app.Commit()
	require.NoError(t, err)
	finalizeAndCommit(t, app, activation+2)
	ctx = check(activation + 3)
	require.True(t, balances(app, ctx, authtypes.FeeCollectorName).IsZero())
	require.Equal(t, ledgerBefore.AddRaw(16_374+40), app.TreasuryKeeper.GetTreasuryBalance(ctx))
}

// uaeth sent straight to the treasury's address, which the ledger never
// saw, is credited at the activation block, so governance can spend it.
// The ledger is only raised: one above the bank balance is left as is.
func TestFeesToTreasury_CreditsDirectSends(t *testing.T) {
	const activation = 3
	defer func(orig int64) { feesToTreasuryActivationHeight = orig }(feesToTreasuryActivationHeight)
	feesToTreasuryActivationHeight = activation

	app := newTestApp(t, dbm.NewMemDB())
	initTestChain(t, app)
	require.NoError(t, finalizeBlock(app, 1))
	ctx := app.NewUncachedContext(false, cmtproto.Header{Height: 1, Time: testGenesisTime.Add(time.Minute)})
	gift := sdk.NewCoins(sdk.NewInt64Coin("uaeth", 1_000_001)) // as on the testnet on 10-28
	sender := sdk.AccAddress("treasury_gift_sender")
	require.NoError(t, app.BankKeeper.MintCoins(ctx, pow.ModuleName, gift))
	require.NoError(t, app.BankKeeper.SendCoinsFromModuleToAccount(ctx, pow.ModuleName, sender, gift))
	require.NoError(t, app.BankKeeper.SendCoins(ctx, sender, authtypes.NewModuleAddress(treasury.ModuleName), gift))
	_, err := app.Commit()
	require.NoError(t, err)

	check := func(h int64) sdk.Context {
		return app.NewUncachedContext(false, cmtproto.Header{Height: h, Time: testGenesisTime.Add(time.Duration(h) * time.Minute)})
	}
	query := func(ctx sdk.Context) *treasury.QueryBalanceResponse {
		res, err := treasury.NewQueryServerImpl(app.TreasuryKeeper).Balance(ctx, &treasury.QueryBalanceRequest{})
		require.NoError(t, err)
		return res
	}
	finalizeAndCommit(t, app, 2)
	require.False(t, query(check(3)).LedgerMatchesBankBalance, "before the height the gift is untracked")

	res, err := app.FinalizeBlock(&abci.RequestFinalizeBlock{Height: activation, Time: testGenesisTime.Add(activation * time.Minute)})
	require.NoError(t, err)
	_, err = app.Commit()
	require.NoError(t, err)
	ctx = check(activation + 1)
	require.True(t, query(ctx).LedgerMatchesBankBalance, "credited at the activation block")
	require.True(t, hasEvent(res.Events, EventTypeTreasuryCredited, gift.String()))
	for _, ev := range res.Events {
		require.NotEqual(t, EventTypeFeesToTreasury, ev.Type, "no fees, no fee event")
	}

	// A ledger above the bank balance isn't lowered.
	require.NoError(t, finalizeBlock(app, activation+1))
	ctx = check(activation + 1)
	app.TreasuryKeeper.FundTreasury(ctx, sdkmath.NewInt(5))
	over := app.TreasuryKeeper.GetTreasuryBalance(ctx)
	_, err = app.Commit()
	require.NoError(t, err)
	finalizeAndCommit(t, app, activation+2)
	ctx = check(activation + 3)
	require.Equal(t, over, app.TreasuryKeeper.GetTreasuryBalance(ctx))
	require.False(t, query(ctx).LedgerMatchesBankBalance, "a ledger over the bank balance still shows")
}

func hasEvent(events []abci.Event, typ, amount string) bool {
	for _, ev := range events {
		if ev.Type != typ {
			continue
		}
		for _, a := range ev.Attributes {
			if a.Key == sdk.AttributeKeyAmount && a.Value == amount {
				return true
			}
		}
	}
	return false
}
