package pow_test

import (
	"testing"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	"github.com/whoyoujoshin/aether/x/pow"
)

func TestQueryParams_ReportsLiveKeeperValues(t *testing.T) {
	k, ctx, _ := setupKeeper(t)
	k.SetEpochLength(ctx, 2880)
	k.SetTopKSize(ctx, 31)
	k.SetBondCooldown(ctx, 8640)
	k.SetRecencyWindowK(ctx, 120)
	k.SetBeaconRoundsPerBlock(ctx, 10_000)

	q := pow.NewQueryServerImpl(k)
	resp, err := q.Params(ctx, &pow.QueryParamsRequest{})
	require.NoError(t, err)

	require.Equal(t, int64(2880), resp.EpochLength)
	require.Equal(t, int64(31), resp.TopKSize)
	require.Equal(t, int64(8640), resp.BondCooldown)
	require.Equal(t, int64(120), resp.RecencyWindowK)
	require.Equal(t, int64(10_000), resp.BeaconRoundsPerBlock)
	require.NotEmpty(t, resp.Difficulty)
	require.NotEmpty(t, resp.MinDifficulty)
	require.NotEmpty(t, resp.MaxDifficulty)
}

func TestQueryParams_FallsBackToDefaultsWhenNothingSet(t *testing.T) {
	k, ctx, _ := setupKeeper(t)
	params := pow.DefaultGenesisState().Params

	q := pow.NewQueryServerImpl(k)
	resp, err := q.Params(ctx, &pow.QueryParamsRequest{})
	require.NoError(t, err)

	require.Equal(t, params.TargetBlockTime, resp.TargetBlockTime)
	require.Equal(t, params.EpochLength, resp.EpochLength)
	require.Equal(t, params.TopKSize, resp.TopKSize)
	require.Equal(t, params.BondCooldown, resp.BondCooldown)
	require.Equal(t, params.RecencyWindowK, resp.RecencyWindowK)
	require.Equal(t, params.BeaconRoundsPerBlock, resp.BeaconRoundsPerBlock)
}

func TestQueryValidatorInfo_ReportsRealTenureData(t *testing.T) {
	k, ctx, _ := setupKeeper(t)
	addr := sdk.AccAddress("validator_info_test___")
	k.SetActiveValidator(ctx, addr)

	q := pow.NewQueryServerImpl(k)
	resp, err := q.ValidatorInfo(ctx, &pow.QueryValidatorInfoRequest{})
	require.NoError(t, err)
	require.Len(t, resp.Validators, 1)
	require.Equal(t, addr.String(), resp.Validators[0].Address)
	require.NotZero(t, resp.Validators[0].EnteredAtUnix)
}

func TestQueryValidatorInfo_EmptyActiveSetReturnsEmptyList(t *testing.T) {
	k, ctx, _ := setupKeeper(t)

	q := pow.NewQueryServerImpl(k)
	resp, err := q.ValidatorInfo(ctx, &pow.QueryValidatorInfoRequest{})
	require.NoError(t, err)
	require.Empty(t, resp.Validators)
}

func TestQueryMinerLeaderboard_RanksHighestWorkFirst(t *testing.T) {
	k, ctx, _ := setupKeeper(t)
	epoch := k.CurrentEpoch(ctx)

	low := sdk.AccAddress("leaderboard_low_worker")
	high := sdk.AccAddress("leaderboard_high_worke")
	k.AddMiningWork(ctx, epoch, low, 10)
	k.AddMiningWork(ctx, epoch, high, 50)

	q := pow.NewQueryServerImpl(k)
	resp, err := q.MinerLeaderboard(ctx, &pow.QueryMinerLeaderboardRequest{})
	require.NoError(t, err)
	require.Equal(t, epoch, resp.Epoch)
	require.Len(t, resp.Entries, 2)
	require.Equal(t, high.String(), resp.Entries[0].Address)
	require.Equal(t, uint64(50), resp.Entries[0].Work)
	require.Equal(t, low.String(), resp.Entries[1].Address)
}

func TestQueryMinerLeaderboard_ExplicitEpochOverridesCurrent(t *testing.T) {
	k, ctx, _ := setupKeeper(t)
	addr := sdk.AccAddress("leaderboard_past_epoch")
	k.AddMiningWork(ctx, 5, addr, 30)

	q := pow.NewQueryServerImpl(k)
	resp, err := q.MinerLeaderboard(ctx, &pow.QueryMinerLeaderboardRequest{Epoch: 5})
	require.NoError(t, err)
	require.Equal(t, int64(5), resp.Epoch)
	require.Len(t, resp.Entries, 1)
	require.Equal(t, addr.String(), resp.Entries[0].Address)
}
