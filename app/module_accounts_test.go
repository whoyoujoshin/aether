package app

import (
	"testing"
	"time"

	sdkmath "cosmossdk.io/math"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	dbm "github.com/cosmos/cosmos-db"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	ibctransfertypes "github.com/cosmos/ibc-go/v8/modules/apps/transfer/types"
	"github.com/stretchr/testify/require"

	"github.com/whoyoujoshin/aether/gates"
	"github.com/whoyoujoshin/aether/x/governance"
	"github.com/whoyoujoshin/aether/x/pow"
	"github.com/whoyoujoshin/aether/x/treasury"
)

func isModuleAccount(app *App, ctx sdk.Context, name string) bool {
	_, ok := app.AccountKeeper.GetAccount(ctx, authtypes.NewModuleAddress(name)).(sdk.ModuleAccountI)
	return ok
}

// On a fresh chain every module account exists from block 1, before any
// transaction could leave an ordinary account at one of their addresses.
func TestModuleAccounts_FreshChainFromBlockOne(t *testing.T) {
	const chainID = gates.RehearsalPrefix + "macc"
	app := newChainApp(t, dbm.NewMemDB(), chainID)
	genesisOn(t, app, chainID)
	finalizeAndCommit(t, app, 1)
	ctx := app.NewUncachedContext(false, cmtproto.Header{ChainID: chainID, Height: 2, Time: testGenesisTime.Add(2 * time.Minute)})
	for _, name := range moduleAccountNames {
		require.True(t, isModuleAccount(app, ctx, name), name)
	}
}

// Before the height a plain send leaves an ordinary account at a module
// address, and the module then panics: for the treasury, in the fee sweep
// at the end of every block. At the height each is converted in place,
// keeping its coins, and the sweep, deposits and the rest work again.
func TestModuleAccounts_SquattedAccountsConvertedAtTheHeight(t *testing.T) {
	const activation = 4
	defer func(orig int64) { moduleAccountsActivationHeight = orig }(moduleAccountsActivationHeight)
	moduleAccountsActivationHeight = activation
	defer func(orig int64) { feesToTreasuryActivationHeight = orig }(feesToTreasuryActivationHeight)
	feesToTreasuryActivationHeight = 2 // the sweep runs every block, as on the testnet now

	app := newTestApp(t, dbm.NewMemDB())
	initTestChain(t, app)
	finalizeAndCommit(t, app, 1)
	require.NoError(t, finalizeBlock(app, 2))
	ctx := app.NewUncachedContext(false, cmtproto.Header{Height: 2, Time: testGenesisTime.Add(2 * time.Minute)})
	squatter := sdk.AccAddress("macc_squatter_______")
	coins := sdk.NewCoins(sdk.NewInt64Coin("uaeth", 1_000))
	require.NoError(t, app.BankKeeper.MintCoins(ctx, ibctransfertypes.ModuleName, coins))
	require.NoError(t, app.BankKeeper.SendCoinsFromModuleToAccount(ctx, ibctransfertypes.ModuleName, squatter, coins))
	one := sdk.NewCoins(sdk.NewInt64Coin("uaeth", 1))
	for _, name := range []string{treasury.ModuleName, governance.ModuleName} {
		require.NoError(t, app.BankKeeper.SendCoins(ctx, squatter, authtypes.NewModuleAddress(name), one))
		require.False(t, isModuleAccount(app, ctx, name), "%s squatted before the height", name)
	}
	_, err := app.Commit()
	require.NoError(t, err)
	finalizeAndCommit(t, app, 3)

	// What the squat does below the height: the sweep panics.
	pre := app.NewUncachedContext(false, cmtproto.Header{Height: 3, Time: testGenesisTime.Add(3 * time.Minute)})
	pre, _ = pre.CacheContext()
	require.NoError(t, app.BankKeeper.SendCoinsFromAccountToModule(pre, squatter, authtypes.FeeCollectorName, one))
	app.invariants.halt = false
	require.Panics(t, func() { _, _ = app.EndBlocker(pre) }, "a squatted treasury stops the block")

	finalizeAndCommit(t, app, activation)
	ctx = app.NewUncachedContext(false, cmtproto.Header{Height: activation + 1, Time: testGenesisTime.Add((activation + 1) * time.Minute)})
	for _, name := range moduleAccountNames {
		require.True(t, isModuleAccount(app, ctx, name), name)
	}
	require.Equal(t, sdkmath.OneInt(), app.BankKeeper.GetBalance(ctx, authtypes.NewModuleAddress(governance.ModuleName), "uaeth").Amount, "its coins stayed")

	// The sweep works, and governance takes a deposit.
	require.NoError(t, app.BankKeeper.SendCoinsFromAccountToModule(ctx, squatter, authtypes.FeeCollectorName, one))
	require.NotPanics(t, func() {
		_, err := app.EndBlocker(ctx)
		require.NoError(t, err)
	})
	h := app.MsgServiceRouter()
	msg := &governance.MsgSubmitProposal{Proposer: squatter.String(), Recipient: squatter.String(), Amount: "1", Deposit: "10"}
	require.NotPanics(t, func() {
		_, err := h.Handler(msg)(ctx, msg)
		require.NoError(t, err)
	})
}

// On a chain where every module account already exists, as on the testnet
// (checked on the seed at 609,151, docs/REVIEW.md), reaching the height
// changes nothing: the app hash at every block matches a chain that never
// reaches it. So a binary carrying the height and one that doesn't agree
// across it there.
func TestModuleAccounts_NoChangeWhereTheyAllExist(t *testing.T) {
	const activation = 4
	defer func(orig int64) { moduleAccountsActivationHeight = orig }(moduleAccountsActivationHeight)
	gated, never := newTestApp(t, dbm.NewMemDB()), newTestApp(t, dbm.NewMemDB())
	run := func(app *App, at int64, f func()) {
		moduleAccountsActivationHeight = at
		f()
	}
	for _, app := range []*App{gated, never} {
		initTestChain(t, app)
		finalizeAndCommit(t, app, 1)
		require.NoError(t, finalizeBlock(app, 2))
		ctx := app.NewUncachedContext(false, cmtproto.Header{Height: 2, Time: testGenesisTime.Add(2 * time.Minute)})
		for _, name := range moduleAccountNames {
			app.ensureModuleAccount(ctx, name)
		}
		_, err := app.Commit()
		require.NoError(t, err)
	}
	for h := int64(3); h <= activation+2; h++ {
		run(gated, activation, func() { finalizeAndCommit(t, gated, h) })
		run(never, 1<<62, func() { finalizeAndCommit(t, never, h) })
		require.Equal(t, never.LastCommitID().Hash, gated.LastCommitID().Hash, "app hash at %d", h)
	}
}

// The testnet turns both review fixes on at one height, with one swap
// (docs/UPGRADE-INPUT-GUARDS.md).
func TestModuleAccounts_HeightMatchesTheAuxPoWGuard(t *testing.T) {
	require.Equal(t, pow.AuxPoWInputGuardHeight, ModuleAccountsActivationHeight)
}
