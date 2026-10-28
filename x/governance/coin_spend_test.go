package governance_test

import (
	"errors"
	"testing"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	"github.com/whoyoujoshin/aether/x/governance"
)

const testUSDC = "ibc/064D82A67318DD30F54A4E17B6E487C2864E8B075A5871D3ACAD7A7129F32C5C"

func TestParseSpendAmount(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want sdk.Coin
	}{
		{"5000000", sdk.NewInt64Coin("uaeth", 5_000_000)},
		{"5000000uaeth", sdk.NewInt64Coin("uaeth", 5_000_000)},
		{"1000000" + testUSDC, sdk.NewInt64Coin(testUSDC, 1_000_000)},
		{"7stake", sdk.NewInt64Coin("stake", 7)},
	} {
		got, err := governance.ParseSpendAmount(tc.in)
		require.NoError(t, err, tc.in)
		require.Equal(t, tc.want, got, tc.in)
	}
	for _, bad := range []string{"", "0", "-5", "0uaeth", "1.5uaeth", "5 uaeth", "5uaeth,3stake", "uaeth", "5u", "5" + testUSDC + " ", "not-a-number", "-5uaeth"} {
		_, err := governance.ParseSpendAmount(bad)
		require.Error(t, err, "%q", bad)
	}
}

// Below TreasuryCoinSpendActivationHeight a coin amount is refused at
// submission exactly as it always was; from it, it's accepted, and a plain
// integer still means uaeth.
func TestSubmitProposal_CoinAmountGate(t *testing.T) {
	_, proposerStr := validProposerAddr(t)
	submit := func(height int64, amount string) error {
		k, ctx, _, _ := setupKeeper(t)
		ctx = ctx.WithBlockHeight(height)
		_, err := governance.NewMsgServerImpl(k).SubmitProposal(ctx, &governance.MsgSubmitProposal{
			Proposer: proposerStr, Recipient: proposerStr, Amount: amount, Deposit: "0",
		})
		return err
	}
	before := governance.TreasuryCoinSpendActivationHeight - 1
	for _, a := range []string{"1000000" + testUSDC, "5000000uaeth"} {
		err := submit(before, a)
		require.True(t, errors.Is(err, governance.ErrInvalidAmount), "%q before the height: %v", a, err)
	}
	require.NoError(t, submit(before, "5000000"))

	at := governance.TreasuryCoinSpendActivationHeight
	for _, a := range []string{"1000000" + testUSDC, "5000000uaeth", "5000000"} {
		require.NoError(t, submit(at, a), "%q at the height", a)
	}
	for _, a := range []string{"0" + testUSDC, "1.5" + testUSDC, "not-a-number", "0"} {
		err := submit(at, a)
		require.True(t, errors.Is(err, governance.ErrInvalidAmount), "%q at the height: %v", a, err)
	}
}

// A passed proposal pays uaeth through the treasury's ledger (Spend) and
// any other denom from its bank balance (SpendCoin), from the height.
func TestResolveProposal_CoinSpend(t *testing.T) {
	for _, tc := range []struct {
		amount     string
		ledger     bool
		coin       sdk.Coin
		beforeFail bool
	}{
		{"10000000", true, sdk.NewInt64Coin("uaeth", 10_000_000), false},
		{"10000000uaeth", true, sdk.NewInt64Coin("uaeth", 10_000_000), true},
		{"2500000" + testUSDC, false, sdk.NewInt64Coin(testUSDC, 2_500_000), true},
	} {
		for _, height := range []int64{governance.TreasuryCoinSpendActivationHeight - 1, governance.TreasuryCoinSpendActivationHeight} {
			k, ctx, _, mockPow, mockTreasury := setupKeeperWithTreasury(t)
			ctx = ctx.WithBlockHeight(height)
			recipient := sdk.AccAddress("coin_spend_recipient")
			proposal := governance.Proposal{
				Id: 1, Recipient: recipient.String(), Amount: tc.amount, TotalDeposit: "25000000",
				Status: governance.ProposalStatus_PROPOSAL_STATUS_VOTING_PERIOD,
			}
			k.SetProposal(ctx, proposal)
			voter := sdk.AccAddress("coin_spend_voter_yes")
			mockPow.ActiveValidators[voter.String()] = true
			k.SetVote(ctx, governance.Vote{ProposalId: 1, Voter: voter.String(), Option: governance.VoteOption_VOTE_OPTION_YES, Weight: "1"})

			require.NoError(t, k.ResolveProposal(ctx, proposal, 1))
			updated, _ := k.GetProposal(ctx, 1)
			before := height < governance.TreasuryCoinSpendActivationHeight
			if before && tc.beforeFail {
				// A coin amount can't have been submitted below the
				// height; if one were stored, execution fails as before.
				require.NotEqual(t, governance.ProposalStatus_PROPOSAL_STATUS_PASSED, updated.Status, "%s at %d", tc.amount, height)
				require.Empty(t, mockTreasury.SpendCalls)
				require.Empty(t, mockTreasury.SpendCoinCalls)
				continue
			}
			require.Equal(t, governance.ProposalStatus_PROPOSAL_STATUS_PASSED, updated.Status, "%s at %d", tc.amount, height)
			if tc.ledger {
				require.Len(t, mockTreasury.SpendCalls, 1)
				require.True(t, mockTreasury.SpendCalls[0].Amount.Equal(tc.coin.Amount))
				require.Empty(t, mockTreasury.SpendCoinCalls)
			} else {
				require.Empty(t, mockTreasury.SpendCalls)
				require.Len(t, mockTreasury.SpendCoinCalls, 1)
				require.Equal(t, tc.coin, mockTreasury.SpendCoinCalls[0].Coin)
				require.True(t, mockTreasury.SpendCoinCalls[0].Recipient.Equals(recipient))
			}
		}
	}
}
