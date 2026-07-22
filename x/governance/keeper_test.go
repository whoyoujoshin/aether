package governance_test

import (
	"errors"
	"testing"

	"cosmossdk.io/log"
	"cosmossdk.io/math"
	"cosmossdk.io/store/metrics"
	storetypes "cosmossdk.io/store/types"
	tmproto "github.com/cometbft/cometbft/proto/tendermint/types"
	dbm "github.com/cosmos/cosmos-db"
	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	"cosmossdk.io/store"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	"github.com/whoyoujoshin/aether/x/governance"
	"github.com/whoyoujoshin/aether/x/governance/testutil"
)

func setupKeeper(t *testing.T) (governance.Keeper, sdk.Context, *testutil.MockBankKeeper, *testutil.MockPowKeeper) {
	t.Helper()

	storeKey := storetypes.NewKVStoreKey(governance.StoreKey)

	db := dbm.NewMemDB()
	stateStore := store.NewCommitMultiStore(db, log.NewNopLogger(), metrics.NewNoOpMetrics())
	stateStore.MountStoreWithDB(storeKey, storetypes.StoreTypeIAVL, db)
	require.NoError(t, stateStore.LoadLatestVersion())

	ctx := sdk.NewContext(stateStore, tmproto.Header{}, false, log.NewNopLogger())

	interfaceRegistry := codectypes.NewInterfaceRegistry()
	cdc := codec.NewProtoCodec(interfaceRegistry)

	mockBank := testutil.NewMockBankKeeper()
	mockPow := testutil.NewMockPowKeeper()
	k := governance.NewKeeper(cdc, storeKey, mockBank, mockPow)

	return k, ctx, mockBank, mockPow
}

func validProposerAddr(t *testing.T) (sdk.AccAddress, string) {
	t.Helper()
	addr := sdk.AccAddress("valid_proposer_address")
	return addr, addr.String()
}

// --- Params ---

func TestMinDeposit_RoundTrip(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)
	k.SetMinDeposit(ctx, 25_000_000)
	require.Equal(t, int64(25_000_000), k.GetMinDeposit(ctx))
}

func TestDepositPeriod_RoundTrip(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)
	k.SetDepositPeriod(ctx, 14*24*60*60)
	require.Equal(t, int64(14*24*60*60), k.GetDepositPeriod(ctx))
}

func TestVotingPeriod_RoundTrip(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)
	k.SetVotingPeriod(ctx, 7*24*60*60)
	require.Equal(t, int64(7*24*60*60), k.GetVotingPeriod(ctx))
}

// --- Proposal storage ---

func TestGetProposal_NotFoundReturnsFalse(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)
	_, ok := k.GetProposal(ctx, 999)
	require.False(t, ok)
}

func TestSetProposal_RoundTrip(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)
	proposal := governance.Proposal{
		Id:           1,
		Recipient:    "cosmos1testaddress",
		Amount:       "10000000",
		TotalDeposit: "0",
		Status:       governance.ProposalStatus_PROPOSAL_STATUS_DEPOSIT_PERIOD,
		SubmitTime:   1000,
	}
	k.SetProposal(ctx, proposal)

	stored, ok := k.GetProposal(ctx, 1)
	require.True(t, ok)
	require.Equal(t, proposal.Recipient, stored.Recipient)
	require.Equal(t, proposal.Amount, stored.Amount)
	require.Equal(t, proposal.Status, stored.Status)
}

func TestIterateProposals_ReturnsAllStored(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)
	k.SetProposal(ctx, governance.Proposal{Id: 1, Recipient: "addr1"})
	k.SetProposal(ctx, governance.Proposal{Id: 2, Recipient: "addr2"})
	k.SetProposal(ctx, governance.Proposal{Id: 3, Recipient: "addr3"})

	proposals := k.IterateProposals(ctx)
	require.Len(t, proposals, 3)
}

func TestIterateProposals_EmptyStoreReturnsNoEntries(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)
	proposals := k.IterateProposals(ctx)
	require.Empty(t, proposals)
}

// --- Deposit storage ---

func TestGetDeposit_NotFoundReturnsFalse(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)
	depositor := sdk.AccAddress("no_deposit_here_______")
	_, ok := k.GetDeposit(ctx, 1, depositor)
	require.False(t, ok)
}

func TestSetDeposit_RoundTrip(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)
	depositor := sdk.AccAddress("deposit_roundtrip_test")

	deposit := governance.Deposit{
		ProposalId: 1,
		Depositor:  depositor.String(),
		Amount:     "5000000",
	}
	k.SetDeposit(ctx, deposit)

	stored, ok := k.GetDeposit(ctx, 1, depositor)
	require.True(t, ok)
	require.Equal(t, "5000000", stored.Amount)
}

func TestIterateDeposits_ReturnsOnlyThisProposalsDeposits(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)
	depositorA := sdk.AccAddress("deposit_iterate_a_____")
	depositorB := sdk.AccAddress("deposit_iterate_b_____")

	k.SetDeposit(ctx, governance.Deposit{ProposalId: 1, Depositor: depositorA.String(), Amount: "1000000"})
	k.SetDeposit(ctx, governance.Deposit{ProposalId: 1, Depositor: depositorB.String(), Amount: "2000000"})
	k.SetDeposit(ctx, governance.Deposit{ProposalId: 2, Depositor: depositorA.String(), Amount: "3000000"})

	deposits := k.IterateDeposits(ctx, 1)
	require.Len(t, deposits, 2, "must not include proposal 2's deposit")
}

func TestIterateDeposits_EmptyForProposalWithNoDeposits(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)
	deposits := k.IterateDeposits(ctx, 999)
	require.Empty(t, deposits)
}

// --- SubmitProposal / Deposit message handlers ---

func TestSubmitProposal_Success_CreatesProposalInDepositPeriod(t *testing.T) {
	k, ctx, mockBank, _ := setupKeeper(t)
	srv := governance.NewMsgServerImpl(k)
	k.SetMinDeposit(ctx, 25_000_000)
	k.SetDepositPeriod(ctx, 14*24*60*60)

	_, proposerStr := validProposerAddr(t)
	msg := &governance.MsgSubmitProposal{
		Proposer:  proposerStr,
		Recipient: proposerStr,
		Amount:    "1000000",
		Deposit:   "5000000",
	}

	resp, err := srv.SubmitProposal(ctx, msg)
	require.NoError(t, err)
	require.Equal(t, uint64(1), resp.ProposalId)

	proposal, ok := k.GetProposal(ctx, 1)
	require.True(t, ok)
	require.Equal(t, governance.ProposalStatus_PROPOSAL_STATUS_DEPOSIT_PERIOD, proposal.Status)
	require.Equal(t, "5000000", proposal.TotalDeposit)

	require.Len(t, mockBank.SendCalls, 1)
	require.Equal(t, "5000000aeth", mockBank.SendCalls[0].Coins.String())
}

func TestSubmitProposal_MeetingMinDepositImmediatelyEntersVotingPeriod(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)
	srv := governance.NewMsgServerImpl(k)
	k.SetMinDeposit(ctx, 25_000_000)
	k.SetVotingPeriod(ctx, 7*24*60*60)

	_, proposerStr := validProposerAddr(t)
	msg := &governance.MsgSubmitProposal{
		Proposer:  proposerStr,
		Recipient: proposerStr,
		Amount:    "1000000",
		Deposit:   "25000000",
	}

	_, err := srv.SubmitProposal(ctx, msg)
	require.NoError(t, err)

	proposal, ok := k.GetProposal(ctx, 1)
	require.True(t, ok)
	require.Equal(t, governance.ProposalStatus_PROPOSAL_STATUS_VOTING_PERIOD, proposal.Status,
		"meeting MinDeposit at creation time should immediately enter voting period")
	require.Equal(t, ctx.BlockTime().Unix()+7*24*60*60, proposal.VotingEndTime)
}

func TestSubmitProposal_ZeroDepositStaysInDepositPeriodWithNoBankCall(t *testing.T) {
	k, ctx, mockBank, _ := setupKeeper(t)
	srv := governance.NewMsgServerImpl(k)
	k.SetMinDeposit(ctx, 25_000_000)

	_, proposerStr := validProposerAddr(t)
	msg := &governance.MsgSubmitProposal{
		Proposer:  proposerStr,
		Recipient: proposerStr,
		Amount:    "1000000",
		Deposit:   "0",
	}

	_, err := srv.SubmitProposal(ctx, msg)
	require.NoError(t, err)
	require.Empty(t, mockBank.SendCalls, "zero deposit should not trigger a bank transfer")
}

func TestSubmitProposal_RejectsInvalidRecipient(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)
	srv := governance.NewMsgServerImpl(k)

	_, proposerStr := validProposerAddr(t)
	msg := &governance.MsgSubmitProposal{
		Proposer:  proposerStr,
		Recipient: "not-a-valid-address",
		Amount:    "1000000",
		Deposit:   "0",
	}

	_, err := srv.SubmitProposal(ctx, msg)
	require.Error(t, err)
	require.True(t, errors.Is(err, governance.ErrInvalidRecipient))
}

func TestDeposit_AccumulatesAcrossMultipleContributors(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)
	srv := governance.NewMsgServerImpl(k)
	k.SetMinDeposit(ctx, 25_000_000)

	_, proposerStr := validProposerAddr(t)
	_, err := srv.SubmitProposal(ctx, &governance.MsgSubmitProposal{
		Proposer: proposerStr, Recipient: proposerStr, Amount: "1000000", Deposit: "10000000",
	})
	require.NoError(t, err)

	secondDepositor := sdk.AccAddress("second_depositor______")
	_, err = srv.Deposit(ctx, &governance.MsgDeposit{
		ProposalId: 1, Depositor: secondDepositor.String(), Amount: "15000000",
	})
	require.NoError(t, err)

	proposal, ok := k.GetProposal(ctx, 1)
	require.True(t, ok)
	require.Equal(t, "25000000", proposal.TotalDeposit)
	require.Equal(t, governance.ProposalStatus_PROPOSAL_STATUS_VOTING_PERIOD, proposal.Status,
		"combined deposits from multiple contributors should trigger the transition")
}

func TestDeposit_RejectsContributionToNonexistentProposal(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)
	srv := governance.NewMsgServerImpl(k)

	depositor := sdk.AccAddress("depositor_for_missing_")
	_, err := srv.Deposit(ctx, &governance.MsgDeposit{
		ProposalId: 999, Depositor: depositor.String(), Amount: "1000000",
	})
	require.Error(t, err)
	require.True(t, errors.Is(err, governance.ErrProposalNotFound))
}

func TestDeposit_RejectsContributionAfterVotingPeriodStarted(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)
	srv := governance.NewMsgServerImpl(k)
	k.SetMinDeposit(ctx, 25_000_000)

	_, proposerStr := validProposerAddr(t)
	_, err := srv.SubmitProposal(ctx, &governance.MsgSubmitProposal{
		Proposer: proposerStr, Recipient: proposerStr, Amount: "1000000", Deposit: "25000000",
	})
	require.NoError(t, err)

	lateDepositor := sdk.AccAddress("late_depositor________")
	_, err = srv.Deposit(ctx, &governance.MsgDeposit{
		ProposalId: 1, Depositor: lateDepositor.String(), Amount: "1000000",
	})
	require.Error(t, err)
	require.True(t, errors.Is(err, governance.ErrDepositPeriodEnded))
}

// --- Deposit-period expiry ---

func TestExpireProposal_BurnsAllAccumulatedDeposits(t *testing.T) {
	k, ctx, mockBank, _ := setupKeeper(t)

	proposal := governance.Proposal{
		Id:           1,
		Recipient:    "cosmos1testaddress",
		Amount:       "1000000",
		TotalDeposit: "15000000",
		Status:       governance.ProposalStatus_PROPOSAL_STATUS_DEPOSIT_PERIOD,
	}
	k.SetProposal(ctx, proposal)

	depositorA := sdk.AccAddress("expiry_depositor_a____")
	depositorB := sdk.AccAddress("expiry_depositor_b____")
	k.SetDeposit(ctx, governance.Deposit{ProposalId: 1, Depositor: depositorA.String(), Amount: "10000000"})
	k.SetDeposit(ctx, governance.Deposit{ProposalId: 1, Depositor: depositorB.String(), Amount: "5000000"})

	err := k.ExpireProposal(ctx, proposal)
	require.NoError(t, err)

	updated, ok := k.GetProposal(ctx, 1)
	require.True(t, ok)
	require.Equal(t, governance.ProposalStatus_PROPOSAL_STATUS_EXPIRED, updated.Status)

	require.Len(t, mockBank.BurnCalls, 2)
	totalBurned := math.ZeroInt()
	for _, c := range mockBank.BurnCalls {
		amt := c.Coins.AmountOf("aeth")
		totalBurned = totalBurned.Add(amt)
	}
	require.True(t, totalBurned.Equal(math.NewInt(15_000_000)))
}

func TestExpireProposal_NoDepositsIsANoOpBurnButStillExpires(t *testing.T) {
	k, ctx, mockBank, _ := setupKeeper(t)

	proposal := governance.Proposal{
		Id:     1,
		Status: governance.ProposalStatus_PROPOSAL_STATUS_DEPOSIT_PERIOD,
	}
	k.SetProposal(ctx, proposal)

	err := k.ExpireProposal(ctx, proposal)
	require.NoError(t, err)

	require.Empty(t, mockBank.BurnCalls)

	updated, ok := k.GetProposal(ctx, 1)
	require.True(t, ok)
	require.Equal(t, governance.ProposalStatus_PROPOSAL_STATUS_EXPIRED, updated.Status)
}

func TestProcessProposalExpiry_ExpiresOnlyProposalsPastDepositEndTime(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)

	now := ctx.BlockTime().Unix()

	expiredProposal := governance.Proposal{
		Id: 1, Status: governance.ProposalStatus_PROPOSAL_STATUS_DEPOSIT_PERIOD,
		DepositEndTime: now - 100,
	}
	stillOpenProposal := governance.Proposal{
		Id: 2, Status: governance.ProposalStatus_PROPOSAL_STATUS_DEPOSIT_PERIOD,
		DepositEndTime: now + 100,
	}
	k.SetProposal(ctx, expiredProposal)
	k.SetProposal(ctx, stillOpenProposal)

	k.ProcessProposalLifecycle(ctx)

	updated1, _ := k.GetProposal(ctx, 1)
	updated2, _ := k.GetProposal(ctx, 2)
	require.Equal(t, governance.ProposalStatus_PROPOSAL_STATUS_EXPIRED, updated1.Status)
	require.Equal(t, governance.ProposalStatus_PROPOSAL_STATUS_DEPOSIT_PERIOD, updated2.Status,
		"proposal still within its deposit window must not be expired")
}

func TestProcessProposalExpiry_IgnoresProposalsNotInDepositPeriod(t *testing.T) {
	k, ctx, mockBank, _ := setupKeeper(t)

	now := ctx.BlockTime().Unix()
	votingProposal := governance.Proposal{
		Id: 1, Status: governance.ProposalStatus_PROPOSAL_STATUS_VOTING_PERIOD,
		DepositEndTime: now - 100,
	}
	k.SetProposal(ctx, votingProposal)

	k.ProcessProposalLifecycle(ctx)

	updated, _ := k.GetProposal(ctx, 1)
	require.Equal(t, governance.ProposalStatus_PROPOSAL_STATUS_VOTING_PERIOD, updated.Status,
		"a proposal already in voting period must not be touched by expiry processing")
	require.Empty(t, mockBank.BurnCalls)
}

// --- Vote storage ---

func TestGetVote_NotFoundReturnsFalse(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)
	voter := sdk.AccAddress("no_vote_here__________")
	_, ok := k.GetVote(ctx, 1, voter)
	require.False(t, ok)
}

func TestSetVote_RoundTrip(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)
	voter := sdk.AccAddress("vote_roundtrip_voter__")

	vote := governance.Vote{
		ProposalId: 1,
		Voter:      voter.String(),
		Option:     governance.VoteOption_VOTE_OPTION_YES,
		Weight:     "0.5",
	}
	k.SetVote(ctx, vote)

	stored, ok := k.GetVote(ctx, 1, voter)
	require.True(t, ok)
	require.Equal(t, governance.VoteOption_VOTE_OPTION_YES, stored.Option)
	require.Equal(t, "0.5", stored.Weight)
}

func TestSetVote_OverwritesPreviousVoteFromSameVoter(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)
	voter := sdk.AccAddress("changing_my_mind_voter")

	k.SetVote(ctx, governance.Vote{ProposalId: 1, Voter: voter.String(), Option: governance.VoteOption_VOTE_OPTION_NO, Weight: "0.2"})
	k.SetVote(ctx, governance.Vote{ProposalId: 1, Voter: voter.String(), Option: governance.VoteOption_VOTE_OPTION_YES, Weight: "0.2"})

	stored, ok := k.GetVote(ctx, 1, voter)
	require.True(t, ok)
	require.Equal(t, governance.VoteOption_VOTE_OPTION_YES, stored.Option, "second vote should overwrite the first")
}

func TestIterateVotes_ReturnsOnlyThisProposalsVotes(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)
	voterA := sdk.AccAddress("iterate_votes_voter_a_")
	voterB := sdk.AccAddress("iterate_votes_voter_b_")

	k.SetVote(ctx, governance.Vote{ProposalId: 1, Voter: voterA.String(), Option: governance.VoteOption_VOTE_OPTION_YES})
	k.SetVote(ctx, governance.Vote{ProposalId: 1, Voter: voterB.String(), Option: governance.VoteOption_VOTE_OPTION_NO})
	k.SetVote(ctx, governance.Vote{ProposalId: 2, Voter: voterA.String(), Option: governance.VoteOption_VOTE_OPTION_ABSTAIN})

	votes := k.IterateVotes(ctx, 1)
	require.Len(t, votes, 2, "must not include proposal 2's vote")
}

// --- Vote message handler ---

func TestVote_Success_LocksInCurrentTenureRatioAsWeight(t *testing.T) {
	k, ctx, _, mockPow := setupKeeper(t)
	srv := governance.NewMsgServerImpl(k)

	proposal := governance.Proposal{Id: 1, Status: governance.ProposalStatus_PROPOSAL_STATUS_VOTING_PERIOD}
	k.SetProposal(ctx, proposal)

	voter := sdk.AccAddress("vote_handler_voter____")
	mockPow.TenureRatios[voter.String()] = math.LegacyMustNewDecFromStr("0.75")

	_, err := srv.Vote(ctx, &governance.MsgVote{
		ProposalId: 1, Voter: voter.String(), Option: governance.VoteOption_VOTE_OPTION_YES,
	})
	require.NoError(t, err)

	stored, ok := k.GetVote(ctx, 1, voter)
	require.True(t, ok)
	require.Equal(t, "0.750000000000000000", stored.Weight)
}

func TestVote_RejectsVoteOnNonexistentProposal(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)
	srv := governance.NewMsgServerImpl(k)

	voter := sdk.AccAddress("vote_for_missing_prop_")
	_, err := srv.Vote(ctx, &governance.MsgVote{
		ProposalId: 999, Voter: voter.String(), Option: governance.VoteOption_VOTE_OPTION_YES,
	})
	require.Error(t, err)
	require.True(t, errors.Is(err, governance.ErrProposalNotFound))
}

func TestVote_RejectsVoteWhenNotInVotingPeriod(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)
	srv := governance.NewMsgServerImpl(k)

	proposal := governance.Proposal{Id: 1, Status: governance.ProposalStatus_PROPOSAL_STATUS_DEPOSIT_PERIOD}
	k.SetProposal(ctx, proposal)

	voter := sdk.AccAddress("vote_too_early_voter__")
	_, err := srv.Vote(ctx, &governance.MsgVote{
		ProposalId: 1, Voter: voter.String(), Option: governance.VoteOption_VOTE_OPTION_YES,
	})
	require.Error(t, err)
	require.True(t, errors.Is(err, governance.ErrNotInVotingPeriod))
}

func TestVote_RejectsUnspecifiedOption(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)
	srv := governance.NewMsgServerImpl(k)

	proposal := governance.Proposal{Id: 1, Status: governance.ProposalStatus_PROPOSAL_STATUS_VOTING_PERIOD}
	k.SetProposal(ctx, proposal)

	voter := sdk.AccAddress("vote_unspecified______")
	_, err := srv.Vote(ctx, &governance.MsgVote{
		ProposalId: 1, Voter: voter.String(), Option: governance.VoteOption_VOTE_OPTION_UNSPECIFIED,
	})
	require.Error(t, err)
	require.True(t, errors.Is(err, governance.ErrInvalidVoteOption))
}

func TestVote_ChangingVoteOverwritesPreviousOne(t *testing.T) {
	k, ctx, _, mockPow := setupKeeper(t)
	srv := governance.NewMsgServerImpl(k)

	proposal := governance.Proposal{Id: 1, Status: governance.ProposalStatus_PROPOSAL_STATUS_VOTING_PERIOD}
	k.SetProposal(ctx, proposal)

	voter := sdk.AccAddress("changes_mind_voter____")
	mockPow.TenureRatios[voter.String()] = math.LegacyMustNewDecFromStr("0.3")

	_, err := srv.Vote(ctx, &governance.MsgVote{ProposalId: 1, Voter: voter.String(), Option: governance.VoteOption_VOTE_OPTION_NO})
	require.NoError(t, err)

	_, err = srv.Vote(ctx, &governance.MsgVote{ProposalId: 1, Voter: voter.String(), Option: governance.VoteOption_VOTE_OPTION_NO_WITH_VETO})
	require.NoError(t, err)

	stored, ok := k.GetVote(ctx, 1, voter)
	require.True(t, ok)
	require.Equal(t, governance.VoteOption_VOTE_OPTION_NO_WITH_VETO, stored.Option, "later vote must overwrite the earlier one")
}

// --- TallyVotes ---

func TestTallyVotes_ExcludesVotesFromNoLongerActiveValidators(t *testing.T) {
	k, ctx, _, mockPow := setupKeeper(t)

	activeVoter := sdk.AccAddress("still_active_voter____")
	inactiveVoter := sdk.AccAddress("no_longer_active_voter")

	mockPow.ActiveValidators[activeVoter.String()] = true
	// inactiveVoter deliberately left out of ActiveValidators -- simulates
	// falling out of Top-K or being slashed after casting their vote.

	k.SetVote(ctx, governance.Vote{ProposalId: 1, Voter: activeVoter.String(), Option: governance.VoteOption_VOTE_OPTION_YES, Weight: "1.0"})
	k.SetVote(ctx, governance.Vote{ProposalId: 1, Voter: inactiveVoter.String(), Option: governance.VoteOption_VOTE_OPTION_YES, Weight: "1.0"})

	result := k.TallyVotes(ctx, 1)
	require.Equal(t, int64(1), result.ValidVoterCount, "the no-longer-active voter must not count")
	require.True(t, result.YesPower.Equal(math.LegacyOneDec()), "only the active voter's weight should be counted")
}

func TestTallyVotes_SumsWeightPerOption(t *testing.T) {
	k, ctx, _, mockPow := setupKeeper(t)

	voterYes := sdk.AccAddress("tally_voter_yes_______")
	voterNo := sdk.AccAddress("tally_voter_no________")
	voterAbstain := sdk.AccAddress("tally_voter_abstain___")
	voterVeto := sdk.AccAddress("tally_voter_veto______")

	for _, addr := range []sdk.AccAddress{voterYes, voterNo, voterAbstain, voterVeto} {
		mockPow.ActiveValidators[addr.String()] = true
	}

	k.SetVote(ctx, governance.Vote{ProposalId: 1, Voter: voterYes.String(), Option: governance.VoteOption_VOTE_OPTION_YES, Weight: "0.5"})
	k.SetVote(ctx, governance.Vote{ProposalId: 1, Voter: voterNo.String(), Option: governance.VoteOption_VOTE_OPTION_NO, Weight: "0.3"})
	k.SetVote(ctx, governance.Vote{ProposalId: 1, Voter: voterAbstain.String(), Option: governance.VoteOption_VOTE_OPTION_ABSTAIN, Weight: "0.2"})
	k.SetVote(ctx, governance.Vote{ProposalId: 1, Voter: voterVeto.String(), Option: governance.VoteOption_VOTE_OPTION_NO_WITH_VETO, Weight: "0.1"})

	result := k.TallyVotes(ctx, 1)
	require.Equal(t, int64(4), result.ValidVoterCount)
	require.True(t, result.YesPower.Equal(math.LegacyMustNewDecFromStr("0.5")))
	require.True(t, result.NoPower.Equal(math.LegacyMustNewDecFromStr("0.3")))
	require.True(t, result.AbstainPower.Equal(math.LegacyMustNewDecFromStr("0.2")))
	require.True(t, result.VetoPower.Equal(math.LegacyMustNewDecFromStr("0.1")))
}

// --- ResolveProposal ---

func setupResolvableProposal(t *testing.T, k governance.Keeper, ctx sdk.Context) governance.Proposal {
	t.Helper()
	proposal := governance.Proposal{
		Id:           1,
		Recipient:    "cosmos1recipient",
		Amount:       "1000000",
		TotalDeposit: "25000000",
		Status:       governance.ProposalStatus_PROPOSAL_STATUS_VOTING_PERIOD,
	}
	k.SetProposal(ctx, proposal)
	return proposal
}

func TestResolveProposal_FailsQuorumWithTooFewValidVoters(t *testing.T) {
	k, ctx, mockBank, mockPow := setupKeeper(t)
	proposal := setupResolvableProposal(t, k, ctx)

	depositor := sdk.AccAddress("quorum_fail_depositor_")
	k.SetDeposit(ctx, governance.Deposit{ProposalId: 1, Depositor: depositor.String(), Amount: "25000000"})

	voter := sdk.AccAddress("only_one_voter________")
	mockPow.ActiveValidators[voter.String()] = true
	k.SetVote(ctx, governance.Vote{ProposalId: 1, Voter: voter.String(), Option: governance.VoteOption_VOTE_OPTION_YES, Weight: "1.0"})

	err := k.ResolveProposal(ctx, proposal, 13) // needs 13, only 1 valid vote
	require.NoError(t, err)

	updated, ok := k.GetProposal(ctx, 1)
	require.True(t, ok)
	require.Equal(t, governance.ProposalStatus_PROPOSAL_STATUS_FAILED_QUORUM, updated.Status)

	require.Len(t, mockBank.BurnCalls, 1, "deposit must be burned on quorum failure")
}

func TestResolveProposal_PassesAtExactlyTwoThirdsYes(t *testing.T) {
	k, ctx, mockBank, mockPow := setupKeeper(t)
	proposal := setupResolvableProposal(t, k, ctx)

	depositor := sdk.AccAddress("pass_test_depositor___")
	k.SetDeposit(ctx, governance.Deposit{ProposalId: 1, Depositor: depositor.String(), Amount: "25000000"})

	voterYes := sdk.AccAddress("pass_test_voter_yes___")
	voterNo := sdk.AccAddress("pass_test_voter_no____")
	mockPow.ActiveValidators[voterYes.String()] = true
	mockPow.ActiveValidators[voterNo.String()] = true

	// Exactly 2/3 yes, 1/3 no -- must pass (>= 2/3, not > 2/3).
	k.SetVote(ctx, governance.Vote{ProposalId: 1, Voter: voterYes.String(), Option: governance.VoteOption_VOTE_OPTION_YES, Weight: "2"})
	k.SetVote(ctx, governance.Vote{ProposalId: 1, Voter: voterNo.String(), Option: governance.VoteOption_VOTE_OPTION_NO, Weight: "1"})

	err := k.ResolveProposal(ctx, proposal, 2)
	require.NoError(t, err)

	updated, ok := k.GetProposal(ctx, 1)
	require.True(t, ok)
	require.Equal(t, governance.ProposalStatus_PROPOSAL_STATUS_PASSED, updated.Status,
		"exactly 2/3 yes must pass, per the locked 'at least 2/3' rule")

	require.Len(t, mockBank.SendCalls, 1, "deposit must be refunded on pass")
}

func TestResolveProposal_RejectsJustBelowTwoThirds(t *testing.T) {
	k, ctx, mockBank, mockPow := setupKeeper(t)
	proposal := setupResolvableProposal(t, k, ctx)

	depositor := sdk.AccAddress("reject_test_depositor_")
	k.SetDeposit(ctx, governance.Deposit{ProposalId: 1, Depositor: depositor.String(), Amount: "25000000"})

	voterYes := sdk.AccAddress("reject_test_voter_yes_")
	voterNo := sdk.AccAddress("reject_test_voter_no__")
	mockPow.ActiveValidators[voterYes.String()] = true
	mockPow.ActiveValidators[voterNo.String()] = true

	// 60% yes, 40% no -- below 2/3, must reject.
	k.SetVote(ctx, governance.Vote{ProposalId: 1, Voter: voterYes.String(), Option: governance.VoteOption_VOTE_OPTION_YES, Weight: "0.6"})
	k.SetVote(ctx, governance.Vote{ProposalId: 1, Voter: voterNo.String(), Option: governance.VoteOption_VOTE_OPTION_NO, Weight: "0.4"})

	err := k.ResolveProposal(ctx, proposal, 2)
	require.NoError(t, err)

	updated, ok := k.GetProposal(ctx, 1)
	require.True(t, ok)
	require.Equal(t, governance.ProposalStatus_PROPOSAL_STATUS_REJECTED, updated.Status)
	require.Len(t, mockBank.BurnCalls, 1)
}

func TestResolveProposal_VetoRejectsRegardlessOfYesRatio(t *testing.T) {
	k, ctx, mockBank, mockPow := setupKeeper(t)
	proposal := setupResolvableProposal(t, k, ctx)

	depositor := sdk.AccAddress("veto_test_depositor___")
	k.SetDeposit(ctx, governance.Deposit{ProposalId: 1, Depositor: depositor.String(), Amount: "25000000"})

	voterYes := sdk.AccAddress("veto_test_voter_yes___")
	voterVeto := sdk.AccAddress("veto_test_voter_veto__")
	mockPow.ActiveValidators[voterYes.String()] = true
	mockPow.ActiveValidators[voterVeto.String()] = true

	// 2/3 yes would otherwise pass, but 1/3 veto must override it.
	k.SetVote(ctx, governance.Vote{ProposalId: 1, Voter: voterYes.String(), Option: governance.VoteOption_VOTE_OPTION_YES, Weight: "2"})
	k.SetVote(ctx, governance.Vote{ProposalId: 1, Voter: voterVeto.String(), Option: governance.VoteOption_VOTE_OPTION_NO_WITH_VETO, Weight: "1"})

	err := k.ResolveProposal(ctx, proposal, 2)
	require.NoError(t, err)

	updated, ok := k.GetProposal(ctx, 1)
	require.True(t, ok)
	require.Equal(t, governance.ProposalStatus_PROPOSAL_STATUS_REJECTED, updated.Status,
		"veto at exactly 1/3 of votes cast must reject regardless of yes ratio")
	require.Len(t, mockBank.BurnCalls, 1)
}

func TestResolveProposal_ExactTieFailsToPass(t *testing.T) {
	k, ctx, _, mockPow := setupKeeper(t)
	proposal := setupResolvableProposal(t, k, ctx)

	depositor := sdk.AccAddress("tie_test_depositor____")
	k.SetDeposit(ctx, governance.Deposit{ProposalId: 1, Depositor: depositor.String(), Amount: "25000000"})

	voterYes := sdk.AccAddress("tie_test_voter_yes____")
	voterNo := sdk.AccAddress("tie_test_voter_no_____")
	mockPow.ActiveValidators[voterYes.String()] = true
	mockPow.ActiveValidators[voterNo.String()] = true

	k.SetVote(ctx, governance.Vote{ProposalId: 1, Voter: voterYes.String(), Option: governance.VoteOption_VOTE_OPTION_YES, Weight: "1"})
	k.SetVote(ctx, governance.Vote{ProposalId: 1, Voter: voterNo.String(), Option: governance.VoteOption_VOTE_OPTION_NO, Weight: "1"})

	err := k.ResolveProposal(ctx, proposal, 2)
	require.NoError(t, err)

	updated, ok := k.GetProposal(ctx, 1)
	require.True(t, ok)
	require.Equal(t, governance.ProposalStatus_PROPOSAL_STATUS_REJECTED, updated.Status,
		"an exact 50/50 tie must fail to pass -- status quo wins")
}

func TestResolveProposal_AbstainDoesNotCountTowardThresholdRatio(t *testing.T) {
	k, ctx, _, mockPow := setupKeeper(t)
	proposal := setupResolvableProposal(t, k, ctx)

	voterYes := sdk.AccAddress("abstain_test_voter_yes")
	voterAbstain := sdk.AccAddress("abstain_test_voter_abs")
	mockPow.ActiveValidators[voterYes.String()] = true
	mockPow.ActiveValidators[voterAbstain.String()] = true

	// Yes alone is 100% of non-abstain power -- should pass even though
	// a large abstain vote was also cast (abstain must not dilute the ratio).
	k.SetVote(ctx, governance.Vote{ProposalId: 1, Voter: voterYes.String(), Option: governance.VoteOption_VOTE_OPTION_YES, Weight: "1"})
	k.SetVote(ctx, governance.Vote{ProposalId: 1, Voter: voterAbstain.String(), Option: governance.VoteOption_VOTE_OPTION_ABSTAIN, Weight: "10"})

	err := k.ResolveProposal(ctx, proposal, 2)
	require.NoError(t, err)

	updated, ok := k.GetProposal(ctx, 1)
	require.True(t, ok)
	require.Equal(t, governance.ProposalStatus_PROPOSAL_STATUS_PASSED, updated.Status,
		"abstain power must not count toward the yes/no ratio denominator")
}

// --- ProcessProposalLifecycle (combined pass) ---

func TestProcessProposalLifecycle_ComputesQuorumDynamicallyFromTopKSize(t *testing.T) {
	k, ctx, _, mockPow := setupKeeper(t)
	mockPow.TopKSize = 21

	proposal := governance.Proposal{
		Id: 1, Status: governance.ProposalStatus_PROPOSAL_STATUS_VOTING_PERIOD,
		VotingEndTime: ctx.BlockTime().Unix() - 100, // already past
	}
	k.SetProposal(ctx, proposal)

	k.ProcessProposalLifecycle(ctx)

	updated, ok := k.GetProposal(ctx, 1)
	require.True(t, ok)
	require.Equal(t, governance.ProposalStatus_PROPOSAL_STATUS_FAILED_QUORUM, updated.Status,
		"with TopKSize=21, quorum of 13 is required; zero votes must fail quorum")
}
