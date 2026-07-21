package governance_test

import (
	"testing"

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