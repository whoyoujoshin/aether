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

	"github.com/whoyoujoshin/aether/gates"
	"github.com/whoyoujoshin/aether/x/escrow"
	"github.com/whoyoujoshin/aether/x/governance"
	"github.com/whoyoujoshin/aether/x/inflowcap"
	"github.com/whoyoujoshin/aether/x/pow"
)

// Each invariant holds on a fresh chain, and catches the one kind of
// break it is there for: state that promises more than a module account
// holds, or a total that no longer matches its records.
func TestInvariants_EachCatchesItsBreak(t *testing.T) {
	const chainID = gates.RehearsalPrefix + "inv"
	fresh := func(t *testing.T) (*App, sdk.Context) {
		app := newChainApp(t, dbm.NewMemDB(), chainID)
		genesisOn(t, app, chainID)
		finalizeAndCommit(t, app, 1)
		ctx := app.NewUncachedContext(false, cmtproto.Header{ChainID: chainID, Height: 2, Time: testGenesisTime.Add(2 * time.Minute)})
		require.NoError(t, app.CheckInvariants(ctx))
		return app, ctx
	}
	someone := sdk.AccAddress("invariant_test_party")
	payOut := func(t *testing.T, app *App, ctx sdk.Context, module string, coins sdk.Coins) {
		require.NoError(t, app.BankKeeper.SendCoinsFromModuleToAccount(ctx, module, someone, coins))
	}
	uaeth := func(n int64) sdk.Coins { return sdk.NewCoins(sdk.NewInt64Coin("uaeth", n)) }

	cases := map[string]func(t *testing.T, app *App, ctx sdk.Context){
		"pow-escrow-covered": func(t *testing.T, app *App, ctx sdk.Context) {
			require.NoError(t, app.BankKeeper.MintCoins(ctx, pow.ModuleName, uaeth(100)))
			app.PowKeeper.AddEscrow(ctx, someone, sdkmath.NewInt(100))
			require.NoError(t, app.CheckInvariants(ctx), "escrow backed by coins")
			payOut(t, app, ctx, pow.ModuleName, uaeth(1))
		},
		"escrow-covered": func(t *testing.T, app *App, ctx sdk.Context) {
			require.NoError(t, app.BankKeeper.MintCoins(ctx, pow.ModuleName, uaeth(1_000)))
			require.NoError(t, app.BankKeeper.SendCoinsFromModuleToAccount(ctx, pow.ModuleName, someone, uaeth(1_000)))
			_, err := escrow.NewMsgServerImpl(app.EscrowKeeper).CreateEscrow(ctx, &escrow.MsgCreateEscrow{
				Payer: someone.String(), Payee: sdk.AccAddress("invariant_test_payee").String(),
				Amount: uaeth(500), ExpiresAt: ctx.BlockTime().Add(escrow.MinDuration + time.Hour).Unix(), OnExpiry: escrow.ON_EXPIRY_REFUND,
			})
			require.NoError(t, err)
			require.NoError(t, app.CheckInvariants(ctx), "an escrow created through its message is covered")
			payOut(t, app, ctx, escrow.ModuleName, uaeth(1))
		},
		"treasury-ledger-backed": func(t *testing.T, app *App, ctx sdk.Context) {
			app.TreasuryKeeper.FundTreasury(ctx, app.TreasuryKeeper.GetRealBankBalance(ctx).AddRaw(1))
		},
		"governance-deposits-covered": func(t *testing.T, app *App, ctx sdk.Context) {
			app.GovernanceKeeper.SetProposal(ctx, governance.Proposal{Id: 7, Status: governance.ProposalStatus_PROPOSAL_STATUS_VOTING_PERIOD})
			app.GovernanceKeeper.SetDeposit(ctx, governance.Deposit{ProposalId: 7, Depositor: someone.String(), Amount: "10"})
		},
		"inflowcap-in-flight-totals": func(t *testing.T, app *App, ctx sdk.Context) {
			app.InflowCapKeeper.AddInFlight(ctx, inflowcap.InFlightSend{PortId: "transfer", ChannelId: "channel-0", Sequence: 1, VoucherDenom: "ibc/ABC", Amount: sdkmath.NewInt(5)})
			require.NoError(t, app.CheckInvariants(ctx), "a recorded send matches its total")
			bz, err := sdkmath.NewInt(6).Marshal()
			require.NoError(t, err)
			ctx.KVStore(app.keys[inflowcap.StoreKey]).Set(inflowcap.InFlightTotalKey("ibc/ABC"), bz)
		},
	}
	for name, breakIt := range cases {
		t.Run(name, func(t *testing.T) {
			app, ctx := fresh(t)
			breakIt(t, app, ctx)
			err := app.CheckInvariants(ctx)
			require.Error(t, err)
			require.Contains(t, err.Error(), name+":")
			for _, inv := range Invariants {
				if inv.Name != name {
					require.Empty(t, inv.Check(app, ctx), "only %s breaks, not %s", name, inv.Name)
				}
			}
		})
	}
	require.Len(t, cases, len(Invariants)-1, "every invariant but the bank's own is broken here once")
}

// A node asked to halt on a broken invariant stops at that block; one
// asked only to check logs it and goes on; by default nothing is checked.
func TestInvariants_CheckedAtTheEndOfABlockOnlyWhenAsked(t *testing.T) {
	const chainID = gates.RehearsalPrefix + "inv"
	app := newChainApp(t, dbm.NewMemDB(), chainID)
	genesisOn(t, app, chainID)
	finalizeAndCommit(t, app, 1)
	ctx := app.NewUncachedContext(false, cmtproto.Header{ChainID: chainID, Height: 2})
	app.GovernanceKeeper.SetProposal(ctx, governance.Proposal{Id: 7, Status: governance.ProposalStatus_PROPOSAL_STATUS_VOTING_PERIOD})
	app.GovernanceKeeper.SetDeposit(ctx, governance.Deposit{ProposalId: 7, Depositor: authtypes.NewModuleAddress("x").String(), Amount: "10"})

	app.invariants = invariantCheck{every: 2, halt: true}
	require.ErrorContains(t, app.checkInvariantsAtEnd(ctx), "governance-deposits-covered")
	require.NoError(t, app.checkInvariantsAtEnd(ctx.WithBlockHeight(3)), "only every 2nd block")
	app.invariants.halt = false
	require.NoError(t, app.checkInvariantsAtEnd(ctx), "logged, not halted")
	app.invariants = invariantCheck{}
	require.NoError(t, app.checkInvariantsAtEnd(ctx))

	t.Setenv("AETHER_INVARIANTS_EVERY", "")
	require.Equal(t, invariantCheck{}, invariantsFromEnv())
	t.Setenv("AETHER_INVARIANTS_EVERY", "100")
	t.Setenv("AETHER_INVARIANTS_HALT", "")
	require.Equal(t, invariantCheck{every: 100}, invariantsFromEnv())
}
