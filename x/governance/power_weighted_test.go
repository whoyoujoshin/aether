package governance_test

import (
	"fmt"
	"testing"

	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	"github.com/whoyoujoshin/aether/x/governance"
	"github.com/whoyoujoshin/aether/x/governance/testutil"
)

// GHSA-3cvf: from PowerWeightedVotingHeight a vote weighs its voter's power
// times its tenure, read at tally, and quorum is 60% of the active set's
// weight. Before it, the old head-count rule still decides (replays match).

type govValidator struct {
	addr   sdk.AccAddress
	power  int64
	tenure string
}

func seatValidators(mockPow *testutil.MockPowKeeper, vals []govValidator) {
	for _, v := range vals {
		mockPow.ActiveValidators[v.addr.String()] = true
		mockPow.Powers[v.addr.String()] = v.power
		mockPow.TenureRatios[v.addr.String()] = math.LegacyMustNewDecFromStr(v.tenure)
	}
}

func govAddrs(prefix string, n int) []sdk.AccAddress {
	out := make([]sdk.AccAddress, n)
	for i := range out {
		out[i] = sdk.AccAddress([]byte(fmt.Sprintf("%s_%02d______________", prefix, i))[:20])
	}
	return out
}

// reporterScenario seats the report's 4 honest validators (full tenure,
// almost all the power) and 13 attacker seats that entered moments ago
// (tenure 0.000002, sharing the 10% a boundary lets newcomers take).
func reporterScenario(t *testing.T, mockPow *testutil.MockPowKeeper) (honest, attackers []sdk.AccAddress) {
	t.Helper()
	honest = govAddrs("honest", 4)
	attackers = govAddrs("attacker", 13)
	var vals []govValidator
	for _, a := range honest {
		vals = append(vals, govValidator{a, 22_500_000, "1"})
	}
	for _, a := range attackers {
		vals = append(vals, govValidator{a, 769_230, "0.000002"})
	}
	seatValidators(mockPow, vals)
	return honest, attackers
}

func spendProposal(t *testing.T, k governance.Keeper, ctx sdk.Context) governance.Proposal {
	t.Helper()
	p := governance.Proposal{
		Id:           1,
		Recipient:    sdk.AccAddress("spend_recipient_____").String(),
		Amount:       "1000000",
		TotalDeposit: "25000000",
		Status:       governance.ProposalStatus_PROPOSAL_STATUS_VOTING_PERIOD,
	}
	k.SetProposal(ctx, p)
	k.SetDeposit(ctx, governance.Deposit{ProposalId: 1, Depositor: sdk.AccAddress("spend_depositor_____").String(), Amount: "25000000"})
	return p
}

func castVote(t *testing.T, k governance.Keeper, ctx sdk.Context, voter sdk.AccAddress, opt governance.VoteOption) {
	t.Helper()
	_, err := governance.NewMsgServerImpl(k).Vote(ctx, &governance.MsgVote{ProposalId: 1, Voter: voter.String(), Option: opt})
	require.NoError(t, err)
}

func resolveStatus(t *testing.T, k governance.Keeper, ctx sdk.Context, p governance.Proposal, headQuorum int64) governance.ProposalStatus {
	t.Helper()
	require.NoError(t, k.ResolveProposal(ctx, p, headQuorum))
	got, ok := k.GetProposal(ctx, p.Id)
	require.True(t, ok)
	return got.Status
}

const reporterHeadQuorum = 11 // ceil(0.6 * 17 active validators)

func TestPowerWeighted_SeatsWithNoWeightCantPassASpend(t *testing.T) {
	for _, tc := range []struct {
		name   string
		height int64
		want   governance.ProposalStatus
	}{
		{"before the height the report's attack passes", governance.PowerWeightedVotingHeight - 1, governance.ProposalStatus_PROPOSAL_STATUS_PASSED},
		{"from the height it fails quorum", governance.PowerWeightedVotingHeight, governance.ProposalStatus_PROPOSAL_STATUS_FAILED_QUORUM},
	} {
		t.Run(tc.name, func(t *testing.T) {
			k, ctx, _, mockPow, treasury := setupKeeperWithTreasury(t)
			ctx = ctx.WithBlockHeight(tc.height)
			_, attackers := reporterScenario(t, mockPow)
			p := spendProposal(t, k, ctx)
			for _, a := range attackers {
				castVote(t, k, ctx, a, governance.VoteOption_VOTE_OPTION_YES)
			}
			require.Equal(t, tc.want, resolveStatus(t, k, ctx, p, reporterHeadQuorum))
			if tc.want != governance.ProposalStatus_PROPOSAL_STATUS_PASSED {
				require.Empty(t, treasury.SpendCalls, "the treasury must not pay")
			}
		})
	}
}

func TestPowerWeighted_AbstainsDontPadQuorum(t *testing.T) {
	for _, tc := range []struct {
		name   string
		height int64
		want   governance.ProposalStatus
	}{
		{"before the height", governance.PowerWeightedVotingHeight - 1, governance.ProposalStatus_PROPOSAL_STATUS_PASSED},
		{"from the height", governance.PowerWeightedVotingHeight, governance.ProposalStatus_PROPOSAL_STATUS_FAILED_QUORUM},
	} {
		t.Run(tc.name, func(t *testing.T) {
			k, ctx, _, mockPow, treasury := setupKeeperWithTreasury(t)
			ctx = ctx.WithBlockHeight(tc.height)
			_, attackers := reporterScenario(t, mockPow)
			p := spendProposal(t, k, ctx)
			castVote(t, k, ctx, attackers[0], governance.VoteOption_VOTE_OPTION_YES)
			for _, a := range attackers[1:] {
				castVote(t, k, ctx, a, governance.VoteOption_VOTE_OPTION_ABSTAIN)
			}
			require.Equal(t, tc.want, resolveStatus(t, k, ctx, p, reporterHeadQuorum))
			if tc.want != governance.ProposalStatus_PROPOSAL_STATUS_PASSED {
				require.Empty(t, treasury.SpendCalls)
			}
		})
	}
}

func TestPowerWeighted_HonestValidatorsStillDecide(t *testing.T) {
	cases := []struct {
		name  string
		yes   int // honest validators voting yes (each holds 25% of the weight)
		no    int
		abst  int
		want  governance.ProposalStatus
		spent bool
	}{
		{"3 of 4 yes: 75% voted, all yes", 3, 0, 0, governance.ProposalStatus_PROPOSAL_STATUS_PASSED, true},
		{"2 of 4 yes: 50% voted, short of 60%", 2, 0, 0, governance.ProposalStatus_PROPOSAL_STATUS_FAILED_QUORUM, false},
		{"2 yes and 1 abstain: abstain makes quorum", 2, 0, 1, governance.ProposalStatus_PROPOSAL_STATUS_PASSED, true},
		{"2 yes, 1 no: exactly 2/3 yes passes", 2, 1, 0, governance.ProposalStatus_PROPOSAL_STATUS_PASSED, true},
		{"2 yes, 2 no: rejected", 2, 2, 0, governance.ProposalStatus_PROPOSAL_STATUS_REJECTED, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			k, ctx, _, mockPow, treasury := setupKeeperWithTreasury(t)
			ctx = ctx.WithBlockHeight(governance.PowerWeightedVotingHeight)
			honest := govAddrs("honest", 4)
			for _, a := range honest {
				seatValidators(mockPow, []govValidator{{a, 25_000_000, "1"}})
			}
			p := spendProposal(t, k, ctx)
			var opts []governance.VoteOption
			for opt, n := range []int{tc.yes, tc.no, tc.abst} {
				for j := 0; j < n; j++ {
					opts = append(opts, []governance.VoteOption{
						governance.VoteOption_VOTE_OPTION_YES,
						governance.VoteOption_VOTE_OPTION_NO,
						governance.VoteOption_VOTE_OPTION_ABSTAIN,
					}[opt])
				}
			}
			for i, opt := range opts {
				castVote(t, k, ctx, honest[i], opt)
			}
			require.Equal(t, tc.want, resolveStatus(t, k, ctx, p, 1))
			require.Equal(t, tc.spent, len(treasury.SpendCalls) == 1)
		})
	}
}

func TestPowerWeighted_PowerCountsNotHeads(t *testing.T) {
	big := govAddrs("big", 1)[0]
	small := govAddrs("small", 3)
	setup := func(t *testing.T) (governance.Keeper, sdk.Context, governance.Proposal) {
		k, ctx, _, mockPow, _ := setupKeeperWithTreasury(t)
		ctx = ctx.WithBlockHeight(governance.PowerWeightedVotingHeight)
		seatValidators(mockPow, []govValidator{{big, 70_000_000, "1"}})
		for _, a := range small {
			seatValidators(mockPow, []govValidator{{a, 10_000_000, "1"}})
		}
		return k, ctx, spendProposal(t, k, ctx)
	}

	t.Run("the 70% validator alone makes quorum and passes", func(t *testing.T) {
		k, ctx, p := setup(t)
		castVote(t, k, ctx, big, governance.VoteOption_VOTE_OPTION_YES)
		require.Equal(t, governance.ProposalStatus_PROPOSAL_STATUS_PASSED, resolveStatus(t, k, ctx, p, 1))
	})
	t.Run("three of four heads with 30% fail quorum", func(t *testing.T) {
		k, ctx, p := setup(t)
		for _, a := range small {
			castVote(t, k, ctx, a, governance.VoteOption_VOTE_OPTION_YES)
		}
		require.Equal(t, governance.ProposalStatus_PROPOSAL_STATUS_FAILED_QUORUM, resolveStatus(t, k, ctx, p, 1))
	})
	t.Run("the 70% validator's No outweighs three Yes heads", func(t *testing.T) {
		k, ctx, p := setup(t)
		castVote(t, k, ctx, big, governance.VoteOption_VOTE_OPTION_NO)
		for _, a := range small {
			castVote(t, k, ctx, a, governance.VoteOption_VOTE_OPTION_YES)
		}
		require.Equal(t, governance.ProposalStatus_PROPOSAL_STATUS_REJECTED, resolveStatus(t, k, ctx, p, 1))
	})
}

func TestPowerWeighted_WeightIsReadAtTally(t *testing.T) {
	k, ctx, _, mockPow, _ := setupKeeperWithTreasury(t)
	ctx = ctx.WithBlockHeight(governance.PowerWeightedVotingHeight)
	vals := govAddrs("val", 2)
	seatValidators(mockPow, []govValidator{{vals[0], 50_000_000, "0"}, {vals[1], 50_000_000, "1"}})
	p := spendProposal(t, k, ctx)

	castVote(t, k, ctx, vals[0], governance.VoteOption_VOTE_OPTION_YES) // weighs nothing when cast
	mockPow.TenureRatios[vals[0].String()] = math.LegacyOneDec()        // ramped by the tally

	tally := k.TallyVotes(ctx, 1)
	require.True(t, tally.YesPower.Equal(math.LegacyNewDec(50_000_000)), "got %s", tally.YesPower)
	require.Equal(t, governance.ProposalStatus_PROPOSAL_STATUS_FAILED_QUORUM, resolveStatus(t, k, ctx, p, 1),
		"50% of the weight voted, short of 60%")
}

func TestPowerWeighted_NothingPassesWithNoWeight(t *testing.T) {
	k, ctx, _, mockPow, _ := setupKeeperWithTreasury(t)
	ctx = ctx.WithBlockHeight(governance.PowerWeightedVotingHeight)
	vals := govAddrs("fresh", 3)
	for _, a := range vals {
		seatValidators(mockPow, []govValidator{{a, 1_000_000, "0"}})
	}
	p := spendProposal(t, k, ctx)
	for _, a := range vals {
		castVote(t, k, ctx, a, governance.VoteOption_VOTE_OPTION_YES)
	}
	require.Equal(t, governance.ProposalStatus_PROPOSAL_STATUS_FAILED_QUORUM, resolveStatus(t, k, ctx, p, 1))
}

func TestPowerWeighted_OnlyActiveValidatorsMayVote(t *testing.T) {
	outsider := sdk.AccAddress("not_a_validator_____")
	msg := &governance.MsgVote{ProposalId: 1, Voter: outsider.String(), Option: governance.VoteOption_VOTE_OPTION_YES}

	k, ctx, _, _, _ := setupKeeperWithTreasury(t)
	spendProposal(t, k, ctx.WithBlockHeight(governance.PowerWeightedVotingHeight-1))
	_, err := governance.NewMsgServerImpl(k).Vote(ctx.WithBlockHeight(governance.PowerWeightedVotingHeight-1), msg)
	require.NoError(t, err, "before the height anyone may vote, as before")

	_, err = governance.NewMsgServerImpl(k).Vote(ctx.WithBlockHeight(governance.PowerWeightedVotingHeight), msg)
	require.ErrorIs(t, err, governance.ErrNotActiveValidator)
}

func TestPowerWeighted_OnFromBlockOneOnFreshChains(t *testing.T) {
	k, ctx, _, mockPow, _ := setupKeeperWithTreasury(t)
	ctx = ctx.WithChainID("aether-rc-gov").WithBlockHeight(1)
	_, attackers := reporterScenario(t, mockPow)
	p := spendProposal(t, k, ctx)
	for _, a := range attackers {
		castVote(t, k, ctx, a, governance.VoteOption_VOTE_OPTION_YES)
	}
	require.Equal(t, governance.ProposalStatus_PROPOSAL_STATUS_FAILED_QUORUM, resolveStatus(t, k, ctx, p, reporterHeadQuorum))
}

func TestPowerWeighted_TallyQueryReportsTheWeightNeeded(t *testing.T) {
	k, ctx, _, mockPow, _ := setupKeeperWithTreasury(t)
	ctx = ctx.WithBlockHeight(governance.PowerWeightedVotingHeight)
	reporterScenario(t, mockPow)
	spendProposal(t, k, ctx)

	// 4 x 22,500,000 + 13 x 769,230 x 0.000002 = 90,000,019.99998; 60% of it
	// is 54,000,011.99999, rounded up.
	require.Equal(t, int64(54_000_012), k.QuorumWeightNeeded(ctx))
	resp, err := governance.NewQueryServerImpl(k).Tally(ctx, &governance.QueryTallyRequest{ProposalId: 1})
	require.NoError(t, err)
	require.Equal(t, int64(54_000_012), resp.QuorumThreshold)

	before, err := governance.NewQueryServerImpl(k).Tally(ctx.WithBlockHeight(governance.PowerWeightedVotingHeight-1), &governance.QueryTallyRequest{ProposalId: 1})
	require.NoError(t, err)
	require.Equal(t, int64(reporterHeadQuorum), before.QuorumThreshold, "before the height it is still validators by head")
}
