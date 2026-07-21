package governance_test

import (
	"testing"
	"errors"

	"cosmossdk.io/log"
	storetypes "cosmossdk.io/store/types"
	tmproto "github.com/cometbft/cometbft/proto/tendermint/types"
	dbm "github.com/cosmos/cosmos-db"
	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	"cosmossdk.io/store"
	"cosmossdk.io/store/metrics"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	"github.com/whoyoujoshin/aether/x/governance"
	"github.com/whoyoujoshin/aether/x/governance/testutil"
)

func setupKeeper(t *testing.T) (governance.Keeper, sdk.Context, *testutil.MockBankKeeper) {
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
	k := governance.NewKeeper(cdc, storeKey, mockBank)

	return k, ctx, mockBank
}

// --- Params ---

func TestMinDeposit_RoundTrip(t *testing.T) {
	k, ctx, _ := setupKeeper(t)
	k.SetMinDeposit(ctx, 25_000_000)
	require.Equal(t, int64(25_000_000), k.GetMinDeposit(ctx))
}

func TestDepositPeriod_RoundTrip(t *testing.T) {
	k, ctx, _ := setupKeeper(t)
	k.SetDepositPeriod(ctx, 14*24*60*60)
	require.Equal(t, int64(14*24*60*60), k.GetDepositPeriod(ctx))
}

func TestVotingPeriod_RoundTrip(t *testing.T) {
	k, ctx, _ := setupKeeper(t)
	k.SetVotingPeriod(ctx, 7*24*60*60)
	require.Equal(t, int64(7*24*60*60), k.GetVotingPeriod(ctx))
}

// --- Proposal storage ---

func TestGetProposal_NotFoundReturnsFalse(t *testing.T) {
	k, ctx, _ := setupKeeper(t)
	_, ok := k.GetProposal(ctx, 999)
	require.False(t, ok)
}

func TestSetProposal_RoundTrip(t *testing.T) {
	k, ctx, _ := setupKeeper(t)
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
	k, ctx, _ := setupKeeper(t)
	k.SetProposal(ctx, governance.Proposal{Id: 1, Recipient: "addr1"})
	k.SetProposal(ctx, governance.Proposal{Id: 2, Recipient: "addr2"})
	k.SetProposal(ctx, governance.Proposal{Id: 3, Recipient: "addr3"})

	proposals := k.IterateProposals(ctx)
	require.Len(t, proposals, 3)
}

func TestIterateProposals_EmptyStoreReturnsNoEntries(t *testing.T) {
	k, ctx, _ := setupKeeper(t)
	proposals := k.IterateProposals(ctx)
	require.Empty(t, proposals)
}

func TestGetDeposit_NotFoundReturnsFalse(t *testing.T) {
	k, ctx, _ := setupKeeper(t)
	depositor := sdk.AccAddress("no_deposit_here_______")
	_, ok := k.GetDeposit(ctx, 1, depositor)
	require.False(t, ok)
}

func TestSetDeposit_RoundTrip(t *testing.T) {
	k, ctx, _ := setupKeeper(t)
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
	k, ctx, _ := setupKeeper(t)
	depositorA := sdk.AccAddress("deposit_iterate_a_____")
	depositorB := sdk.AccAddress("deposit_iterate_b_____")

	k.SetDeposit(ctx, governance.Deposit{ProposalId: 1, Depositor: depositorA.String(), Amount: "1000000"})
	k.SetDeposit(ctx, governance.Deposit{ProposalId: 1, Depositor: depositorB.String(), Amount: "2000000"})
	k.SetDeposit(ctx, governance.Deposit{ProposalId: 2, Depositor: depositorA.String(), Amount: "3000000"})

	deposits := k.IterateDeposits(ctx, 1)
	require.Len(t, deposits, 2, "must not include proposal 2's deposit")
}

func TestIterateDeposits_EmptyForProposalWithNoDeposits(t *testing.T) {
	k, ctx, _ := setupKeeper(t)
	deposits := k.IterateDeposits(ctx, 999)
	require.Empty(t, deposits)
}

func validProposerAddr(t *testing.T) (sdk.AccAddress, string) {
	t.Helper()
	addr := sdk.AccAddress("valid_proposer_address")
	return addr, addr.String()
}

func TestSubmitProposal_Success_CreatesProposalInDepositPeriod(t *testing.T) {
	k, ctx, mockBank := setupKeeper(t)
	srv := governance.NewMsgServerImpl(k)
	k.SetMinDeposit(ctx, 25_000_000)
	k.SetDepositPeriod(ctx, 14*24*60*60)

	_, proposerStr := validProposerAddr(t)
	msg := &governance.MsgSubmitProposal{
		Proposer:  proposerStr,
		Recipient: proposerStr,
		Amount:    "1000000",
		Deposit:   "5000000", // below MinDeposit -- should stay in deposit period
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
	k, ctx, _ := setupKeeper(t)
	srv := governance.NewMsgServerImpl(k)
	k.SetMinDeposit(ctx, 25_000_000)
	k.SetVotingPeriod(ctx, 7*24*60*60)

	_, proposerStr := validProposerAddr(t)
	msg := &governance.MsgSubmitProposal{
		Proposer:  proposerStr,
		Recipient: proposerStr,
		Amount:    "1000000",
		Deposit:   "25000000", // exactly meets MinDeposit
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
	k, ctx, mockBank := setupKeeper(t)
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
	k, ctx, _ := setupKeeper(t)
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
	k, ctx, _ := setupKeeper(t)
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
	k, ctx, _ := setupKeeper(t)
	srv := governance.NewMsgServerImpl(k)

	depositor := sdk.AccAddress("depositor_for_missing_")
	_, err := srv.Deposit(ctx, &governance.MsgDeposit{
		ProposalId: 999, Depositor: depositor.String(), Amount: "1000000",
	})
	require.Error(t, err)
	require.True(t, errors.Is(err, governance.ErrProposalNotFound))
}

func TestDeposit_RejectsContributionAfterVotingPeriodStarted(t *testing.T) {
	k, ctx, _ := setupKeeper(t)
	srv := governance.NewMsgServerImpl(k)
	k.SetMinDeposit(ctx, 25_000_000)

	_, proposerStr := validProposerAddr(t)
	_, err := srv.SubmitProposal(ctx, &governance.MsgSubmitProposal{
		Proposer: proposerStr, Recipient: proposerStr, Amount: "1000000", Deposit: "25000000",
	})
	require.NoError(t, err) // already in voting period after this

	lateDepositor := sdk.AccAddress("late_depositor________")
	_, err = srv.Deposit(ctx, &governance.MsgDeposit{
		ProposalId: 1, Depositor: lateDepositor.String(), Amount: "1000000",
	})
	require.Error(t, err)
	require.True(t, errors.Is(err, governance.ErrDepositPeriodEnded))
}