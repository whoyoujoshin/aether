package app

import (
	"testing"
	"time"

	"cosmossdk.io/math"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	dbm "github.com/cosmos/cosmos-db"
	"github.com/stretchr/testify/require"

	"github.com/whoyoujoshin/aether/gates"
	pow "github.com/whoyoujoshin/aether/x/pow"
)

// On a fresh chain the reward schedule's years are years of block time
// since the genesis time, which InitChain records; on the testnet's
// chain ID nothing is recorded and the schedule keeps counting heights.
func TestRewardSchedule_FreshChainCountsYearsFromGenesisTime(t *testing.T) {
	year := time.Duration(pow.SecondsPerYear) * time.Second
	for _, chainID := range []string{gates.MainnetChainID, gates.RehearsalPrefix + "test"} {
		a := newChainApp(t, dbm.NewMemDB(), chainID)
		genesisOn(t, a, chainID)
		finalizeAndCommit(t, a, 1)

		at := func(height int64, when time.Time) math.Int {
			ctx := a.NewUncachedContext(false, cmtproto.Header{ChainID: chainID, Height: height, Time: when})
			return a.PowKeeper.GetBlockReward(ctx)
		}
		start, ok := a.PowKeeper.GetRewardScheduleStart(a.NewUncachedContext(false, cmtproto.Header{ChainID: chainID}))
		require.True(t, ok, chainID)
		require.Equal(t, testGenesisTime.Unix(), start, "%s: the schedule starts at the genesis time", chainID)

		// Past the height that ended year 1 under the old count (525,960
		// blocks), a day after genesis is still year 1.
		require.Equal(t, int64(5_000_000), at(600_000, testGenesisTime.Add(24*time.Hour)).Int64(), chainID)
		require.Equal(t, int64(5_000_000), at(5_600_000, testGenesisTime.Add(year-time.Second)).Int64(), chainID)
		require.Equal(t, int64(3_300_000), at(5_600_000, testGenesisTime.Add(year)).Int64(), chainID)
		require.Equal(t, int64(272_758), at(44_000_000, testGenesisTime.Add(8*year-time.Second)).Int64(), chainID)
		require.Equal(t, int64(200_000), at(44_000_000, testGenesisTime.Add(8*year)).Int64(), chainID)
		// A block time before the start (never on a real chain) is year 1.
		require.Equal(t, int64(5_000_000), at(2, testGenesisTime.Add(-time.Hour)).Int64(), chainID)
	}

	// The testnet's chain ID records nothing and counts heights as before.
	const testnet = "aether-testnet-1"
	a := newChainApp(t, dbm.NewMemDB(), testnet)
	genesisOn(t, a, testnet)
	finalizeAndCommit(t, a, 1)
	ctx := a.NewUncachedContext(false, cmtproto.Header{ChainID: testnet, Height: 600_000, Time: testGenesisTime.Add(24 * time.Hour)})
	_, ok := a.PowKeeper.GetRewardScheduleStart(ctx)
	require.False(t, ok)
	require.Equal(t, int64(3_300_000), a.PowKeeper.GetBlockReward(ctx).Int64(), "the testnet keeps its height-based years")
}
