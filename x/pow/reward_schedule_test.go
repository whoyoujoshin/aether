package pow_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	"github.com/stretchr/testify/require"

	pow "github.com/whoyoujoshin/aether/x/pow"
)

// --- Reward schedule on a fresh chain (reward_schedule.go) ---

func TestRewardSchedule_GenesisStartAndExport(t *testing.T) {
	k, ctx, _ := setupKeeper(t)
	am := pow.NewAppModule(codec.NewProtoCodec(codectypes.NewInterfaceRegistry()), k)
	genesisTime := time.Date(2026, 12, 1, 16, 0, 0, 0, time.UTC)
	ctx = ctx.WithChainID("aether-1").WithBlockTime(genesisTime)

	// A genesis that names a start (an exported chain) keeps it.
	original := genesisTime.Add(-400 * 24 * time.Hour).Unix()
	gs := pow.DefaultGenesisState()
	gs.RewardScheduleStart = original
	bz, err := json.Marshal(gs)
	require.NoError(t, err)
	am.InitGenesis(ctx, nil, bz)
	start, ok := k.GetRewardScheduleStart(ctx)
	require.True(t, ok)
	require.Equal(t, original, start)
	require.Equal(t, int64(3_300_000), k.GetBlockReward(ctx).Int64(), "400 days after the original start is year 2")

	var exported pow.GenesisState
	require.NoError(t, json.Unmarshal(am.ExportGenesis(ctx, nil), &exported))
	require.Equal(t, original, exported.RewardScheduleStart, "the export carries the start")
}
