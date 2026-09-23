package pow_test

import (
	"testing"

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
