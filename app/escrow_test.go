package app

import (
	"testing"
	"time"

	sdkmath "cosmossdk.io/math"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	dbm "github.com/cosmos/cosmos-db"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	"github.com/stretchr/testify/require"

	"github.com/whoyoujoshin/aether/x/escrow"
	"github.com/whoyoujoshin/aether/x/pow"
)

func TestPlanEscrow(t *testing.T) {
	const activation = 100
	for _, c := range []struct {
		lastCommitted int64
		want          escrowPlan
	}{
		{0, escrowPlan{}},
		{98, escrowPlan{}},
		{99, escrowPlan{wire: true, addStores: true}},
		{100, escrowPlan{wire: true}},
		{5000, escrowPlan{wire: true}},
	} {
		require.Equal(t, c.want, planEscrow(c.lastCommitted, activation), "last committed height %d", c.lastCommitted)
	}
	require.Equal(t, escrowPlan{wire: true}, planEscrow(0, 0), "a chain with escrow from genesis")
}

func uaeth(n int64) sdk.Coins { return sdk.NewCoins(sdk.NewCoin("uaeth", sdkmath.NewInt(n))) }

func fund(t *testing.T, app *App, ctx sdk.Context, to sdk.AccAddress, n int64) {
	t.Helper()
	require.NoError(t, app.BankKeeper.MintCoins(ctx, pow.ModuleName, uaeth(n)))
	require.NoError(t, app.BankKeeper.SendCoinsFromModuleToAccount(ctx, pow.ModuleName, to, uaeth(n)))
}

// The restarts a live node goes through across the activation, with
// someone having sent coins to the escrow address beforehand, then real
// escrows through the real bank keeper and the real EndBlock.
func TestEscrow_ActivationAndRealEscrows(t *testing.T) {
	const activation = 3
	defer func(orig int64) { escrowActivationHeight = orig }(escrowActivationHeight)
	escrowActivationHeight = activation

	const msgCreateURL = "/aether.escrow.v1.MsgCreateEscrow"
	escrowAddr := authtypes.NewModuleAddress(escrow.ModuleName)
	db := dbm.NewMemDB()

	before := newTestApp(t, db)
	require.False(t, before.escrowWired)
	_, err := before.interfaceRegistry.Resolve(msgCreateURL)
	require.Error(t, err, "a pre-activation node doesn't know escrow messages")
	initTestChain(t, before)
	finalizeAndCommit(t, before, 1)

	// Someone sends to the escrow address before activation, leaving an
	// ordinary account there.
	require.NoError(t, finalizeBlock(before, 2))
	squat := before.NewUncachedContext(false, cmtproto.Header{Height: 2, Time: testGenesisTime.Add(2 * time.Minute)})
	fund(t, before, squat, escrowAddr, 7)
	squatted := before.AccountKeeper.GetAccount(squat, escrowAddr)
	require.NotNil(t, squatted)
	_, isModule := squatted.(sdk.ModuleAccountI)
	require.False(t, isModule)
	_, err = before.Commit()
	require.NoError(t, err)
	for _, name := range escrowStoreKeys {
		require.False(t, committedStoreNames(t, before, 2)[name], "no escrow store before activation")
	}

	require.ErrorContains(t, finalizeBlock(before, activation), "restart this node")

	atActivation := newTestApp(t, db)
	require.True(t, atActivation.escrowWired)
	_, err = atActivation.interfaceRegistry.Resolve(msgCreateURL)
	require.NoError(t, err)
	finalizeAndCommit(t, atActivation, activation)
	for _, name := range escrowStoreKeys {
		require.True(t, committedStoreNames(t, atActivation, activation)[name], "escrow store in the AppHash from activation on")
	}

	after := newTestApp(t, db)
	require.True(t, after.escrowWired)
	finalizeAndCommit(t, after, activation+1)

	ctx := after.NewUncachedContext(false, cmtproto.Header{Height: activation + 2, Time: testGenesisTime.Add((activation + 2) * time.Minute)})
	acc := after.AccountKeeper.GetAccount(ctx, escrowAddr)
	macc, isModule := acc.(sdk.ModuleAccountI)
	require.True(t, isModule, "the squatted account became the module account")
	require.Equal(t, escrow.ModuleName, macc.GetName())
	require.Equal(t, squatted.GetAccountNumber(), macc.GetAccountNumber())
	require.Equal(t, sdkmath.NewInt(7), after.BankKeeper.GetBalance(ctx, escrowAddr, "uaeth").Amount, "its coins stayed")

	payer := sdk.AccAddress("escrow_app_payer____")
	payee := sdk.AccAddress("escrow_app_payee____")
	fund(t, after, ctx, payer, 3_000_000)
	srv := escrow.NewMsgServerImpl(after.EscrowKeeper)

	released, err := srv.CreateEscrow(ctx, &escrow.MsgCreateEscrow{
		Payer: payer.String(), Payee: payee.String(), Amount: uaeth(1_000_000),
		ExpiresAt: ctx.BlockTime().Add(time.Hour).Unix(), OnExpiry: escrow.ON_EXPIRY_REFUND, Terms: "job 1",
	})
	require.NoError(t, err)
	_, err = srv.ReleaseEscrow(ctx, &escrow.MsgReleaseEscrow{Sender: payer.String(), Id: released.Id})
	require.NoError(t, err)
	require.Equal(t, sdkmath.NewInt(1_000_000), after.BankKeeper.GetBalance(ctx, payee, "uaeth").Amount)

	// This one expires two blocks on (blocks are a minute apart here).
	_, err = srv.CreateEscrow(ctx, &escrow.MsgCreateEscrow{
		Payer: payer.String(), Payee: payee.String(), Amount: uaeth(2_000_000),
		ExpiresAt: ctx.BlockTime().Add(time.Minute).Unix(), OnExpiry: escrow.ON_EXPIRY_REFUND,
	})
	require.NoError(t, err)
	require.Equal(t, sdkmath.NewInt(0), after.BankKeeper.GetBalance(ctx, payer, "uaeth").Amount)
	require.Equal(t, sdkmath.NewInt(2_000_007), after.BankKeeper.GetBalance(ctx, escrowAddr, "uaeth").Amount)

	finalizeAndCommit(t, after, activation+2) // same minute: not due
	check := after.NewUncachedContext(false, cmtproto.Header{Height: activation + 3})
	require.Equal(t, sdkmath.NewInt(0), after.BankKeeper.GetBalance(check, payer, "uaeth").Amount)

	finalizeAndCommit(t, after, activation+3) // a minute later: EndBlock refunds it
	check = after.NewUncachedContext(false, cmtproto.Header{Height: activation + 4})
	require.Equal(t, sdkmath.NewInt(2_000_000), after.BankKeeper.GetBalance(check, payer, "uaeth").Amount)
	require.Equal(t, sdkmath.NewInt(7), after.BankKeeper.GetBalance(check, escrowAddr, "uaeth").Amount, "only the stray coins are left")
	_, open := after.EscrowKeeper.GetEscrow(check, 2)
	require.False(t, open)
}
