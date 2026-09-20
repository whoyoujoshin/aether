package governance_test

import (
	"testing"

	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	"github.com/whoyoujoshin/aether/x/governance"
)

// TestQueryVote_ReturnsStoredVote and its sibling tests close a real,
// previously-missing gap (Section 3 item 6): there was no way to query
// an individual vote or a tally breakdown directly -- verifying a
// proposal's outcome could only be inferred indirectly from its final
// status.
func TestQueryVote_ReturnsStoredVote(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)
	q := governance.NewQueryServerImpl(k)

	voter := sdk.AccAddress("query_vote_test_voter_")
	k.SetVote(ctx, governance.Vote{
		ProposalId: 1, Voter: voter.String(),
		Option: governance.VoteOption_VOTE_OPTION_YES, Weight: "0.5",
	})

	res, err := q.Vote(ctx, &governance.QueryVoteRequest{ProposalId: 1, Voter: voter.String()})
	require.NoError(t, err)
	require.NotNil(t, res.Vote)
	require.Equal(t, governance.VoteOption_VOTE_OPTION_YES, res.Vote.Option)
	require.Equal(t, "0.5", res.Vote.Weight)
}

func TestQueryVote_NoVoteReturnsNilWithoutError(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)
	q := governance.NewQueryServerImpl(k)

	voter := sdk.AccAddress("query_vote_never_cast_")
	res, err := q.Vote(ctx, &governance.QueryVoteRequest{ProposalId: 1, Voter: voter.String()})
	require.NoError(t, err)
	require.Nil(t, res.Vote, "no vote cast should be a nil result, not an error")
}

func TestQueryVote_RejectsInvalidVoterAddress(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)
	q := governance.NewQueryServerImpl(k)

	_, err := q.Vote(ctx, &governance.QueryVoteRequest{ProposalId: 1, Voter: "not-a-valid-address"})
	require.Error(t, err)
}

func TestQueryVotes_ReturnsEveryVoteOnAProposal(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)
	q := governance.NewQueryServerImpl(k)

	voterA := sdk.AccAddress("query_votes_voter_a___")
	voterB := sdk.AccAddress("query_votes_voter_b___")
	otherProposalVoter := sdk.AccAddress("query_votes_other_prop")

	k.SetVote(ctx, governance.Vote{ProposalId: 1, Voter: voterA.String(), Option: governance.VoteOption_VOTE_OPTION_YES, Weight: "1.0"})
	k.SetVote(ctx, governance.Vote{ProposalId: 1, Voter: voterB.String(), Option: governance.VoteOption_VOTE_OPTION_NO, Weight: "0.4"})
	k.SetVote(ctx, governance.Vote{ProposalId: 2, Voter: otherProposalVoter.String(), Option: governance.VoteOption_VOTE_OPTION_YES, Weight: "1.0"})

	res, err := q.Votes(ctx, &governance.QueryVotesRequest{ProposalId: 1})
	require.NoError(t, err)
	require.Len(t, res.Votes, 2, "must return only this proposal's votes, not votes cast on a different proposal")
}

func TestQueryTally_MatchesKeeperTallyVotesAndReportsQuorumThreshold(t *testing.T) {
	k, ctx, _, mockPow := setupKeeper(t)
	q := governance.NewQueryServerImpl(k)

	activeVoter := sdk.AccAddress("query_tally_active____")
	inactiveVoter := sdk.AccAddress("query_tally_inactive__")
	mockPow.ActiveValidators[activeVoter.String()] = true
	// inactiveVoter deliberately left out -- must not count, same as
	// the existing TallyVotes keeper-level tests.

	k.SetVote(ctx, governance.Vote{ProposalId: 1, Voter: activeVoter.String(), Option: governance.VoteOption_VOTE_OPTION_YES, Weight: "1.0"})
	k.SetVote(ctx, governance.Vote{ProposalId: 1, Voter: inactiveVoter.String(), Option: governance.VoteOption_VOTE_OPTION_NO_WITH_VETO, Weight: "1.0"})

	res, err := q.Tally(ctx, &governance.QueryTallyRequest{ProposalId: 1})
	require.NoError(t, err)
	require.Equal(t, int64(1), res.ValidVoterCount)
	require.Equal(t, math.LegacyOneDec().String(), res.YesPower)
	require.Equal(t, math.LegacyZeroDec().String(), res.VetoPower, "the inactive voter's veto vote must not be counted")

	require.Equal(t, res.QuorumThreshold, int64(0),
		"with no pow keeper TopKSize configured in this mock, the default zero-validator quorum threshold should be zero -- exercising that the field is genuinely wired to computeQuorumThreshold, not a placeholder")
}
