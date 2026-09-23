package pow

import (
	"crypto/ed25519"
	"testing"
	"time"

	"cosmossdk.io/store"
	"cosmossdk.io/store/metrics"
	storetypes "cosmossdk.io/store/types"
	tmproto "github.com/cometbft/cometbft/proto/tendermint/types"
	dbm "github.com/cosmos/cosmos-db"
	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	"cosmossdk.io/log"

	"github.com/whoyoujoshin/aether/x/pow/testutil"
	"github.com/whoyoujoshin/aether/x/pow/types"
)

func genBeaconTestPubkey(t *testing.T) []byte {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(nil)
	require.NoError(t, err)
	return pub
}

// newBeaconTestKeeper mirrors keeper_test.go's setupKeeper -- duplicated
// rather than shared because that helper lives in the external
// pow_test package, unreachable from here (this file needs
// package-internal access to unexported helpers like qualifiedEntry,
// getBeaconState, and setBeaconSeed).
func newBeaconTestKeeper(t *testing.T) (Keeper, sdk.Context) {
	t.Helper()

	storeKey := storetypes.NewKVStoreKey(types.ModuleName)

	db := dbm.NewMemDB()
	stateStore := store.NewCommitMultiStore(db, log.NewNopLogger(), metrics.NewNoOpMetrics())
	stateStore.MountStoreWithDB(storeKey, storetypes.StoreTypeIAVL, db)
	require.NoError(t, stateStore.LoadLatestVersion())

	ctx := sdk.NewContext(stateStore, tmproto.Header{Time: time.Now()}, false, log.NewNopLogger())

	interfaceRegistry := codectypes.NewInterfaceRegistry()
	cdc := codec.NewProtoCodec(interfaceRegistry)

	mockBank := testutil.NewMockBankKeeper()
	mockTreasury := testutil.NewMockTreasuryKeeper()
	k := NewKeeper(cdc, storeKey, log.NewNopLogger(), mockBank, mockTreasury)

	return k, ctx
}

func mkCandidate(name string, work uint64) qualifiedEntry {
	return qualifiedEntry{
		MinerAddr: sdk.AccAddress(name),
		Pubkey:    []byte(name),
		Work:      work,
	}
}

// --- SampleValidatorsWithBeacon (pure function, no store needed) ---

func TestSampleValidatorsWithBeacon_Deterministic(t *testing.T) {
	k := Keeper{}
	candidates := []qualifiedEntry{
		mkCandidate("candidate_one_________", 10),
		mkCandidate("candidate_two_________", 20),
		mkCandidate("candidate_three_______", 30),
		mkCandidate("candidate_four________", 40),
	}
	seed := []byte("a fixed deterministic seed")

	result1 := k.SampleValidatorsWithBeacon(seed, candidates, 2)
	result2 := k.SampleValidatorsWithBeacon(seed, candidates, 2)

	require.Equal(t, result1, result2, "the same seed and candidate set must always produce the same selection")
}

func TestSampleValidatorsWithBeacon_RespectsTopK(t *testing.T) {
	k := Keeper{}
	candidates := []qualifiedEntry{
		mkCandidate("candidate_one_________", 10),
		mkCandidate("candidate_two_________", 20),
		mkCandidate("candidate_three_______", 30),
		mkCandidate("candidate_four________", 40),
		mkCandidate("candidate_five________", 50),
	}

	result := k.SampleValidatorsWithBeacon([]byte("seed"), candidates, 3)
	require.Len(t, result, 3)

	seen := make(map[string]bool)
	for _, r := range result {
		require.False(t, seen[r.MinerAddr.String()], "no candidate should be selected twice")
		seen[r.MinerAddr.String()] = true
	}
}

func TestSampleValidatorsWithBeacon_TopKExceedsCandidates_ReturnsAllExactlyOnce(t *testing.T) {
	k := Keeper{}
	candidates := []qualifiedEntry{
		mkCandidate("candidate_one_________", 10),
		mkCandidate("candidate_two_________", 20),
	}

	result := k.SampleValidatorsWithBeacon([]byte("seed"), candidates, 5)
	require.Len(t, result, 2, "requesting more seats than candidates must return every candidate, not panic or pad")
}

func TestSampleValidatorsWithBeacon_DoesNotMutateInputSlice(t *testing.T) {
	k := Keeper{}
	candidates := []qualifiedEntry{
		mkCandidate("candidate_one_________", 10),
		mkCandidate("candidate_two_________", 20),
		mkCandidate("candidate_three_______", 30),
	}
	original := make([]qualifiedEntry, len(candidates))
	copy(original, candidates)

	k.SampleValidatorsWithBeacon([]byte("seed"), candidates, 2)

	require.Equal(t, original, candidates, "the caller's candidate slice must be left untouched")
}

func TestSampleValidatorsWithBeacon_ZeroWorkCandidates_ReturnsEmptyWithoutPanic(t *testing.T) {
	k := Keeper{}
	candidates := []qualifiedEntry{
		mkCandidate("candidate_one_________", 0),
		mkCandidate("candidate_two_________", 0),
	}

	require.NotPanics(t, func() {
		result := k.SampleValidatorsWithBeacon([]byte("seed"), candidates, 2)
		require.Empty(t, result, "zero total weight has nothing valid to draw, must not divide by zero")
	})
}

func TestSampleValidatorsWithBeacon_HigherWorkSelectedMoreOftenAcrossManySeeds(t *testing.T) {
	k := Keeper{}
	heavy := mkCandidate("heavy_worker__________", 1000)
	light := mkCandidate("light_worker__________", 1)
	candidates := []qualifiedEntry{heavy, light}

	heavyWins, lightWins := 0, 0
	const trials = 300
	for i := 0; i < trials; i++ {
		seed := sdk.Uint64ToBigEndian(uint64(i))
		result := k.SampleValidatorsWithBeacon(seed, candidates, 1)
		require.Len(t, result, 1)
		if result[0].MinerAddr.Equals(heavy.MinerAddr) {
			heavyWins++
		} else {
			lightWins++
		}
	}

	require.Greater(t, heavyWins, lightWins,
		"a candidate with ~1000x the work should win the single seat far more often across many independent seeds")
	require.Greater(t, heavyWins, trials*8/10,
		"with a 1000:1 work ratio, the heavy candidate should win comfortably more than 80%% of draws")
}

// --- AdvanceBeacon / epoch finalization ---

func TestAdvanceBeacon_NoOpBeforeActivationHeight(t *testing.T) {
	k, ctx := newBeaconTestKeeper(t)
	ctx = ctx.WithBlockHeight(RandomnessBeaconActivationHeight - 1).WithHeaderHash([]byte("some-hash"))

	k.AdvanceBeacon(ctx)

	epoch := k.CurrentEpoch(ctx)
	_, ok := k.GetBeaconSeed(ctx, epoch)
	require.False(t, ok, "the beacon must not run at all before its activation height")
}

func TestAdvanceBeacon_AccumulatesAndFinalizesAtEpochBoundary(t *testing.T) {
	k, ctx := newBeaconTestKeeper(t)
	k.SetEpochLength(ctx, 3)

	base := RandomnessBeaconActivationHeight
	// Align to a fresh epoch boundary for a length-3 epoch.
	start := base - (base % 3)
	epoch := start / 3

	ctx1 := ctx.WithBlockHeight(start).WithHeaderHash([]byte("block-hash-1"))
	k.AdvanceBeacon(ctx1)
	_, ok := k.GetBeaconSeed(ctx1, epoch)
	require.False(t, ok, "epoch's seed must not be finalized before its last block")

	ctx2 := ctx.WithBlockHeight(start + 1).WithHeaderHash([]byte("block-hash-2"))
	k.AdvanceBeacon(ctx2)
	_, ok = k.GetBeaconSeed(ctx2, epoch)
	require.False(t, ok)

	ctx3 := ctx.WithBlockHeight(start + 2).WithHeaderHash([]byte("block-hash-3"))
	k.AdvanceBeacon(ctx3)
	seed, ok := k.GetBeaconSeed(ctx3, epoch)
	require.True(t, ok, "epoch's seed must be finalized on its last block")
	require.Len(t, seed, 32, "a SHA-256 output")

	// The in-progress accumulator is cleared once sealed -- a fresh read
	// falls back to the initial per-epoch value, not the finalized one.
	require.Equal(t, initialBeaconState(epoch), k.getBeaconState(ctx3, epoch))
}

func TestAdvanceBeacon_DifferentBlockHashesProduceDifferentFinalSeed(t *testing.T) {
	runEpoch := func(hashes []string) []byte {
		k, ctx := newBeaconTestKeeper(t)
		k.SetEpochLength(ctx, int64(len(hashes)))
		for i, h := range hashes {
			blockCtx := ctx.WithBlockHeight(RandomnessBeaconActivationHeight + int64(i)).WithHeaderHash([]byte(h))
			k.AdvanceBeacon(blockCtx)
		}
		epoch := k.CurrentEpoch(ctx.WithBlockHeight(RandomnessBeaconActivationHeight))
		seed, ok := k.GetBeaconSeed(ctx.WithBlockHeight(RandomnessBeaconActivationHeight), epoch)
		require.True(t, ok)
		return seed
	}

	seedA := runEpoch([]string{"hash-a-1", "hash-a-2"})
	seedB := runEpoch([]string{"hash-b-1", "hash-b-2"})

	require.NotEqual(t, seedA, seedB, "different real block hashes across the epoch must produce different finalized seeds")
}

// --- ComputeValidatorUpdates integration with the beacon ---

func TestComputeValidatorUpdates_PostActivation_FallsBackWithoutPriorFinalizedSeed(t *testing.T) {
	k, ctx := newBeaconTestKeeper(t)
	k.SetTopKSize(ctx, 2)
	ctx = ctx.WithBlockHeight(RandomnessBeaconActivationHeight)

	addrs := []sdk.AccAddress{
		sdk.AccAddress("fallback_candidate_one"),
		sdk.AccAddress("fallback_candidate_two"),
		sdk.AccAddress("fallback_candidate_thr"),
	}
	work := []uint64{10, 30, 20}
	epoch := k.CurrentEpoch(ctx)
	for i, addr := range addrs {
		k.SetValidatorPubkey(ctx, addr, genBeaconTestPubkey(t))
		k.AddMiningWork(ctx, epoch, addr, work[i])
	}

	// No beacon seed exists for epoch-1 -- must fall back to deterministic
	// top-K, identical to pre-activation behavior.
	updates := k.ComputeValidatorUpdates(ctx, epoch)
	require.Len(t, updates, 2)
	require.True(t, k.IsActiveValidator(ctx, addrs[1]), "highest-work candidate should win the deterministic fallback")
	require.True(t, k.IsActiveValidator(ctx, addrs[2]), "second-highest-work candidate should win the deterministic fallback")
	require.False(t, k.IsActiveValidator(ctx, addrs[0]))
}

func TestComputeValidatorUpdates_PostActivation_UsesBeaconSeedWhenPriorEpochFinalized(t *testing.T) {
	k, ctx := newBeaconTestKeeper(t)
	k.SetTopKSize(ctx, 2)
	ctx = ctx.WithBlockHeight(RandomnessBeaconActivationHeight)
	epoch := k.CurrentEpoch(ctx)

	k.setBeaconSeed(ctx, epoch-1, []byte("a finalized prior-epoch seed"))

	addrs := []sdk.AccAddress{
		sdk.AccAddress("beacon_candidate_one__"),
		sdk.AccAddress("beacon_candidate_two__"),
		sdk.AccAddress("beacon_candidate_three"),
		sdk.AccAddress("beacon_candidate_four_"),
	}
	work := []uint64{10, 20, 30, 40}
	for i, addr := range addrs {
		k.SetValidatorPubkey(ctx, addr, genBeaconTestPubkey(t))
		k.AddMiningWork(ctx, epoch, addr, work[i])
	}

	updates := k.ComputeValidatorUpdates(ctx, epoch)
	require.Len(t, updates, 2, "exactly TopKSize seats must be filled via beacon sampling too")

	activeCount := 0
	for _, addr := range addrs {
		if k.IsActiveValidator(ctx, addr) {
			activeCount++
		}
	}
	require.Equal(t, 2, activeCount)
}

func TestComputeValidatorUpdates_PostActivation_DifferentPriorSeedsCanChangeSelection(t *testing.T) {
	runWithSeed := func(seed []byte) map[string]bool {
		k, ctx := newBeaconTestKeeper(t)
		k.SetTopKSize(ctx, 1)
		ctx = ctx.WithBlockHeight(RandomnessBeaconActivationHeight)
		epoch := k.CurrentEpoch(ctx)
		k.setBeaconSeed(ctx, epoch-1, seed)

		addrs := []sdk.AccAddress{
			sdk.AccAddress("seed_sensitivity_cand1"),
			sdk.AccAddress("seed_sensitivity_cand2"),
			sdk.AccAddress("seed_sensitivity_cand3"),
		}
		for _, addr := range addrs {
			k.SetValidatorPubkey(ctx, addr, genBeaconTestPubkey(t))
			k.AddMiningWork(ctx, epoch, addr, 10) // equal work -- pure seed sensitivity, no weighting bias
		}

		k.ComputeValidatorUpdates(ctx, epoch)

		winners := make(map[string]bool)
		for _, addr := range addrs {
			if k.IsActiveValidator(ctx, addr) {
				winners[addr.String()] = true
			}
		}
		return winners
	}

	var sawDifferentWinner bool
	winnersA := runWithSeed([]byte("prior-epoch-seed-alpha"))
	for i := 0; i < 20; i++ {
		seed := append([]byte("prior-epoch-seed-"), byte(i))
		winnersB := runWithSeed(seed)
		if !equalWinnerSets(winnersA, winnersB) {
			sawDifferentWinner = true
			break
		}
	}

	require.True(t, sawDifferentWinner, "with equal-work candidates, different finalized prior-epoch seeds must be able to change who wins the single seat -- otherwise the beacon isn't actually influencing selection")
}

func equalWinnerSets(a, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if !b[k] {
			return false
		}
	}
	return true
}
