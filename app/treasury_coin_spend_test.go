package app

import (
	"testing"
	"time"

	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	"github.com/stretchr/testify/require"

	"github.com/whoyoujoshin/aether/gates"
	"github.com/whoyoujoshin/aether/x/governance"
	"github.com/whoyoujoshin/aether/x/pow"
	"github.com/whoyoujoshin/aether/x/treasury"
)

// Treasury coin spends switch on with the fees that bring the coins in.
func TestTreasuryCoinSpend_MovesWithFeesToTreasury(t *testing.T) {
	require.Equal(t, FeesToTreasuryActivationHeight, governance.TreasuryCoinSpendActivationHeight)
}

// AET-27, end to end on a fresh chain ID (every gate open): the treasury
// holds USDC, a proposal to pay some of it passes through the real
// x/governance, and the recipient gets it from the treasury's bank
// balance while the uaeth ledger doesn't move. One for more than the
// treasury holds passes the vote but fails execution.
func TestTreasuryCoinSpend_GovernancePaysUSDC(t *testing.T) {
	f := newLigaseFixtureOn(t, 0, gates.RehearsalPrefix+"test-a")
	app := f.appA
	ctx := f.a.GetContext()
	treasuryAddr := authtypes.NewModuleAddress(treasury.ModuleName)
	held := sdk.NewInt64Coin(testUSDC, 3_000_000)
	require.NoError(t, app.BankKeeper.MintCoins(ctx, pow.ModuleName, sdk.NewCoins(held)))
	require.NoError(t, app.BankKeeper.SendCoinsFromModuleToModule(ctx, pow.ModuleName, treasury.ModuleName, sdk.NewCoins(held)))
	ledger := app.TreasuryKeeper.GetTreasuryBalance(ctx)

	voter := f.a.SenderAccount.GetAddress()
	gov := app.GovernanceKeeper
	app.PowKeeper.SetActiveValidator(ctx, voter)
	gov.SetVotingPeriod(ctx, 60)
	f.coord.IncrementTimeBy(time.Hour)
	f.coord.CommitBlock(f.a)
	srv := governance.NewMsgServerImpl(gov)

	recipient := sdk.AccAddress("usdc_spend_recipient")
	propose := func(amount string) uint64 {
		res, err := srv.SubmitProposal(f.a.GetContext(), &governance.MsgSubmitProposal{
			Proposer: voter.String(), Recipient: recipient.String(), Amount: amount, Deposit: "25000000",
		})
		require.NoError(t, err)
		for _, v := range app.PowKeeper.IterateActiveValidators(f.a.GetContext()) {
			_, err = srv.Vote(f.a.GetContext(), &governance.MsgVote{ProposalId: res.ProposalId, Voter: v.String(), Option: governance.VoteOption_VOTE_OPTION_YES})
			require.NoError(t, err)
		}
		return res.ProposalId
	}
	ok := propose("1000000" + testUSDC)
	tooMuch := propose("5000000" + testUSDC)
	f.coord.IncrementTimeBy(2 * time.Minute)
	f.coord.CommitBlock(f.a) // both voting periods end; EndBlock resolves them

	ctx = f.a.GetContext()
	p, _ := gov.GetProposal(ctx, ok)
	require.Equal(t, governance.ProposalStatus_PROPOSAL_STATUS_PASSED, p.Status)
	p, _ = gov.GetProposal(ctx, tooMuch)
	require.Equal(t, governance.ProposalStatus_PROPOSAL_STATUS_EXECUTION_FAILED, p.Status, "more USDC than the treasury holds")

	require.Equal(t, sdkmath.NewInt(1_000_000), app.BankKeeper.GetBalance(ctx, recipient, testUSDC).Amount)
	require.Equal(t, sdkmath.NewInt(2_000_000), app.BankKeeper.GetBalance(ctx, treasuryAddr, testUSDC).Amount)
	require.True(t, app.BankKeeper.GetBalance(ctx, recipient, "uaeth").IsZero())
	require.Equal(t, ledger, app.TreasuryKeeper.GetTreasuryBalance(ctx), "the uaeth ledger doesn't move for USDC")
}
