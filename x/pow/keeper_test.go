package pow_test

import (
	"errors"
	"testing"
	"time"
	"fmt"

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
	"crypto/ed25519"
	"github.com/whoyoujoshin/aether/x/pow"
	"github.com/whoyoujoshin/aether/x/pow/testutil"
	"github.com/whoyoujoshin/aether/x/pow/types"
	cometed25519 "github.com/cometbft/cometbft/crypto/ed25519"
	cometencoding "github.com/cometbft/cometbft/crypto/encoding"
	"cosmossdk.io/core/comet"
	"github.com/whoyoujoshin/aether/x/treasury"
)

// setupKeeper builds a pow.Keeper against an in-memory store with a
// MockBankKeeper, independent of the rest of the app.

func setupKeeper(t *testing.T) (pow.Keeper, sdk.Context, *testutil.MockBankKeeper) {
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
	k := pow.NewKeeper(cdc, storeKey, log.NewNopLogger(), mockBank, mockTreasury)

	return k, ctx, mockBank
}

func setupKeeperWithTreasury(t *testing.T) (pow.Keeper, sdk.Context, *testutil.MockBankKeeper, *testutil.MockTreasuryKeeper) {
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
	k := pow.NewKeeper(cdc, storeKey, log.NewNopLogger(), mockBank, mockTreasury)

	return k, ctx, mockBank, mockTreasury
}

func testHeader(difficulty, nonce uint64) pow.MiningHeader {
	return pow.MiningHeader{
		Height:       1,
		Timestamp:    time.Now().Unix(),
		PrevHash:     []byte("prev-hash-placeholder"),
		MerkleRoot:   []byte("merkle-root-placeholder"),
		Nonce:        nonce,
		Difficulty:   difficulty,
		MinerAddress: sdk.AccAddress("miner_address_bytes_"),
	}
}

// --- VerifyMiningHeader ---

func TestVerifyMiningHeader_RejectsZeroDifficulty(t *testing.T) {
	k, ctx, _ := setupKeeper(t)
	header := testHeader(0, 12345)
	require.False(t, k.VerifyMiningHeader(ctx, header))
}

func TestVerifyMiningHeader_PassesAtTrivialDifficulty(t *testing.T) {
	k, ctx, _ := setupKeeper(t)
	// difficulty == 1 means the target equals the maximum possible value,
	// so any hash below the absolute max satisfies it — effectively always.
	header := testHeader(1, 42)
	require.True(t, k.VerifyMiningHeader(ctx, header))
}

func TestVerifyMiningHeader_FailsAtExtremeDifficulty(t *testing.T) {
	k, ctx, _ := setupKeeper(t)
	// At MaxDifficulty (1<<40), the target is astronomically small.
	// An arbitrary nonce should not satisfy it — this is what "real mining"
	// (searching for a satisfying nonce) exists to solve.
	params := pow.DefaultGenesisState().Params
	header := testHeader(uint64(params.MaxDifficulty), 42)
	require.False(t, k.VerifyMiningHeader(ctx, header))
}

func TestVerifyMiningHeader_AllowsDifficultyAbove256(t *testing.T) {
	k, ctx, _ := setupKeeper(t)
	// Regression test: the old implementation rejected any difficulty >= 256
	// outright (header.Difficulty >= 256 { return false }), which was
	// incompatible with the large difficulty values used in Params (e.g.
	// InitialDifficulty = 1<<20). A difficulty of 300 must now be evaluated
	// on its actual hash target, not auto-rejected. We search for a
	// satisfying nonce (as real mining would) rather than asserting a
	// single arbitrary nonce succeeds, since at difficulty 300 the target
	// is still wide enough that a satisfying nonce should turn up quickly.
	const difficulty = 300
	found := false
	for nonce := uint64(0); nonce < 100_000; nonce++ {
		header := testHeader(difficulty, nonce)
		if k.VerifyMiningHeader(ctx, header) {
			found = true
			break
		}
	}
	require.True(t, found, "expected to find a satisfying nonce at difficulty %d within 100,000 attempts", difficulty)
}

// --- DistributeBlockReward ---

func TestDistributeBlockReward_SplitsCorrectly(t *testing.T) {
	k, ctx, mockBank := setupKeeper(t)
	miner := sdk.AccAddress("miner_address_bytes_")

	k.SetBlockReward(ctx, math.NewInt(5_000_000))

	err := k.DistributeBlockReward(ctx, miner)
	require.NoError(t, err)

	require.Len(t, mockBank.MintCalls, 1)
	require.Equal(t, types.ModuleName, mockBank.MintCalls[0].Module)
	require.Equal(t, "5000000aeth", mockBank.MintCalls[0].Coins.String())

	require.Len(t, mockBank.SendCalls, 2)

	minerSend := mockBank.SendCalls[0]
	require.Equal(t, miner, minerSend.RecipientAddr)
	require.Equal(t, "4250000aeth", minerSend.Coins.String())

	treasurySend := mockBank.SendCalls[1]
	require.Equal(t, "treasury", treasurySend.RecipientModule)
	require.Equal(t, "750000aeth", treasurySend.Coins.String())
}

func TestDistributeBlockReward_ZeroRewardNoOps(t *testing.T) {
	k, ctx, mockBank := setupKeeper(t)
	miner := sdk.AccAddress("miner_address_bytes_")

	k.SetBlockReward(ctx, math.ZeroInt())

	err := k.DistributeBlockReward(ctx, miner)
	require.NoError(t, err)
	require.Empty(t, mockBank.MintCalls)
	require.Empty(t, mockBank.SendCalls)
}

func TestDistributeBlockReward_SkipsTreasurySendWhenCutTruncatesToZero(t *testing.T) {
	k, ctx, mockBank := setupKeeper(t)
	miner := sdk.AccAddress("miner_address_bytes_")

	// reward=1: 15% truncates to 0, so only the miner send should occur.
	k.SetBlockReward(ctx, math.NewInt(1))

	err := k.DistributeBlockReward(ctx, miner)
	require.NoError(t, err)
	require.Len(t, mockBank.MintCalls, 1)
	require.Len(t, mockBank.SendCalls, 1, "treasury send should be skipped when the cut truncates to zero")
	require.Equal(t, "1aeth", mockBank.SendCalls[0].Coins.String())
}

func TestDistributeBlockReward_PropagatesMintError(t *testing.T) {
	k, ctx, mockBank := setupKeeper(t)
	miner := sdk.AccAddress("miner_address_bytes_")

	mockBank.MintErr = errors.New("mint failed")
	k.SetBlockReward(ctx, math.NewInt(5_000_000))

	err := k.DistributeBlockReward(ctx, miner)
	require.Error(t, err)
	require.Empty(t, mockBank.SendCalls, "no sends should happen if minting fails")
}

func TestDistributeBlockReward_PropagatesSendError(t *testing.T) {
	k, ctx, mockBank := setupKeeper(t)
	miner := sdk.AccAddress("miner_address_bytes_")

	mockBank.SendErr = errors.New("send failed")
	k.SetBlockReward(ctx, math.NewInt(5_000_000))

	err := k.DistributeBlockReward(ctx, miner)
	require.Error(t, err)
}

// --- AdjustDifficulty ---

func TestAdjustDifficulty_NoLastBlockTimeReturnsCurrent(t *testing.T) {
	k, ctx, _ := setupKeeper(t)
	current := math.NewInt(int64(pow.DefaultGenesisState().Params.Difficulty))
	k.SetDifficulty(ctx, current)

	adjusted := k.AdjustDifficulty(ctx)
	require.True(t, adjusted.Equal(current))
}

func TestAdjustDifficulty_FastBlocksIncreaseDifficulty(t *testing.T) {
	k, ctx, _ := setupKeeper(t)
	params := pow.DefaultGenesisState().Params

	current := math.NewInt(int64(params.Difficulty))
	k.SetDifficulty(ctx, current)

	lastTime := time.Now().Unix()
	k.SetLastBlockTime(ctx, lastTime)

	// Block arrived in half the target time -> difficulty should rise.
	fastElapsed := params.TargetBlockTime / 2
	ctx = ctx.WithBlockTime(time.Unix(lastTime+fastElapsed, 0))

	adjusted := k.AdjustDifficulty(ctx)
	require.True(t, adjusted.GT(current), "difficulty should increase when blocks arrive faster than target")
}

func TestAdjustDifficulty_SlowBlocksDecreaseDifficulty(t *testing.T) {
	k, ctx, _ := setupKeeper(t)
	params := pow.DefaultGenesisState().Params

	current := math.NewInt(int64(params.Difficulty))
	k.SetDifficulty(ctx, current)

	lastTime := time.Now().Unix()
	k.SetLastBlockTime(ctx, lastTime)

	// Block arrived in double the target time -> difficulty should fall.
	slowElapsed := params.TargetBlockTime * 2
	ctx = ctx.WithBlockTime(time.Unix(lastTime+slowElapsed, 0))

	adjusted := k.AdjustDifficulty(ctx)
	require.True(t, adjusted.LT(current), "difficulty should decrease when blocks arrive slower than target")
}

func TestAdjustDifficulty_ClampsAtMinimum(t *testing.T) {
	k, ctx, _ := setupKeeper(t)
	params := pow.DefaultGenesisState().Params

	current := math.NewInt(int64(params.MinDifficulty))
	k.SetDifficulty(ctx, current)

	lastTime := time.Now().Unix()
	k.SetLastBlockTime(ctx, lastTime)

	// Extremely slow block (1000x target time) should try to push difficulty
	// far below MinDifficulty, but it must be clamped.
	slowElapsed := params.TargetBlockTime * 1000
	ctx = ctx.WithBlockTime(time.Unix(lastTime+slowElapsed, 0))

	adjusted := k.AdjustDifficulty(ctx)
	minD := math.NewInt(int64(params.MinDifficulty))
	require.True(t, adjusted.GTE(minD), "difficulty should never drop below MinDifficulty")
}

func TestAdjustDifficulty_ClampsAtMaximum(t *testing.T) {
	k, ctx, _ := setupKeeper(t)
	params := pow.DefaultGenesisState().Params

	current := math.NewInt(int64(params.MaxDifficulty))
	k.SetDifficulty(ctx, current)

	lastTime := time.Now().Unix()
	k.SetLastBlockTime(ctx, lastTime)

	// Extremely fast block (target/1000) should try to push difficulty
	// far above MaxDifficulty, but it must be clamped.
	fastElapsed := params.TargetBlockTime / 1000
	if fastElapsed < 1 {
		fastElapsed = 1
	}
	ctx = ctx.WithBlockTime(time.Unix(lastTime+fastElapsed, 0))

	adjusted := k.AdjustDifficulty(ctx)
	maxD := math.NewInt(int64(params.MaxDifficulty))
	require.True(t, adjusted.LTE(maxD), "difficulty should never exceed MaxDifficulty")
}

// --- Min/Max/TargetBlockTime persistence ---

func TestMinMaxTargetBlockTime_FallbackToDefaults(t *testing.T) {
	k, ctx, _ := setupKeeper(t)
	params := pow.DefaultGenesisState().Params

	require.True(t, k.GetMinDifficulty(ctx).Equal(math.NewInt(int64(params.MinDifficulty))))
	require.True(t, k.GetMaxDifficulty(ctx).Equal(math.NewInt(int64(params.MaxDifficulty))))
	require.Equal(t, params.TargetBlockTime, k.GetTargetBlockTime(ctx))
}

func TestMinMaxTargetBlockTime_RoundTrip(t *testing.T) {
	k, ctx, _ := setupKeeper(t)

	// Deliberately use values that differ from DefaultGenesisState(), so a
	// regression back to reading hardcoded defaults (instead of persisted
	// state) would be caught by this test failing.
	customMin := int64(777)
	customMax := int64(999_999_999)
	customTarget := int64(42)

	k.SetMinDifficulty(ctx, customMin)
	k.SetMaxDifficulty(ctx, customMax)
	k.SetTargetBlockTime(ctx, customTarget)

	require.True(t, k.GetMinDifficulty(ctx).Equal(math.NewInt(customMin)))
	require.True(t, k.GetMaxDifficulty(ctx).Equal(math.NewInt(customMax)))
	require.Equal(t, customTarget, k.GetTargetBlockTime(ctx))
}

func TestAdjustDifficulty_UsesPersistedCustomValues_NotHardcodedDefaults(t *testing.T) {
	k, ctx, _ := setupKeeper(t)

	// Regression guard: AdjustDifficulty previously read
	// DefaultGenesisState().Params directly, silently ignoring whatever
	// was actually persisted (and therefore ignoring genesis.json entirely).
	// This test uses custom min/max/target that differ sharply from the
	// real defaults, so if AdjustDifficulty ever reverts to reading
	// defaults again, this test will fail clearly rather than passing
	// by coincidence.
	customMin := int64(10)
	customMax := int64(20) // deliberately tiny, far below real defaults
	customTarget := int64(100)

	k.SetMinDifficulty(ctx, customMin)
	k.SetMaxDifficulty(ctx, customMax)
	k.SetTargetBlockTime(ctx, customTarget)

	k.SetDifficulty(ctx, math.NewInt(15))
	lastTime := time.Now().Unix()
	k.SetLastBlockTime(ctx, lastTime)

	// Huge elapsed time should try to push difficulty far below customMin,
	// but must clamp there instead of the real DefaultGenesisState min.
	ctx = ctx.WithBlockTime(time.Unix(lastTime+100_000, 0))
	adjusted := k.AdjustDifficulty(ctx)
	require.True(t, adjusted.Equal(math.NewInt(customMin)),
		"expected clamp at custom min %d, got %s", customMin, adjusted.String())

	// Reset and test the opposite direction: fast submission should clamp
	// at customMax, not the real (vastly larger) DefaultGenesisState max.
	k.SetDifficulty(ctx, math.NewInt(15))
	k.SetLastBlockTime(ctx, lastTime)
	ctx = ctx.WithBlockTime(time.Unix(lastTime+1, 0))
	adjusted = k.AdjustDifficulty(ctx)
	require.True(t, adjusted.Equal(math.NewInt(customMax)),
		"expected clamp at custom max %d, got %s", customMax, adjusted.String())
}

func TestValidatorPubkey_NotFoundReturnsFalse(t *testing.T) {
	k, ctx, _ := setupKeeper(t)
	minerAddr := sdk.AccAddress("no_registration_here__")

	_, ok := k.GetValidatorPubkey(ctx, minerAddr)
	require.False(t, ok)
}

func TestValidatorPubkey_RoundTrip(t *testing.T) {
	k, ctx, _ := setupKeeper(t)
	minerAddr := sdk.AccAddress("test_miner_address___")
	pubkey := make([]byte, ed25519.PublicKeySize)
	pubkey[5] = 0x42

	k.SetValidatorPubkey(ctx, minerAddr, pubkey)

	stored, ok := k.GetValidatorPubkey(ctx, minerAddr)
	require.True(t, ok)
	require.Equal(t, pubkey, stored)
}

func TestValidatorPubkey_DifferentMinersDoNotCollide(t *testing.T) {
	k, ctx, _ := setupKeeper(t)
	minerA := sdk.AccAddress("miner_a_address______")
	minerB := sdk.AccAddress("miner_b_address______")

	pubkeyA := make([]byte, ed25519.PublicKeySize)
	pubkeyA[0] = 0xAA
	pubkeyB := make([]byte, ed25519.PublicKeySize)
	pubkeyB[0] = 0xBB

	k.SetValidatorPubkey(ctx, minerA, pubkeyA)
	k.SetValidatorPubkey(ctx, minerB, pubkeyB)

	storedA, _ := k.GetValidatorPubkey(ctx, minerA)
	storedB, _ := k.GetValidatorPubkey(ctx, minerB)
	require.Equal(t, pubkeyA, storedA)
	require.Equal(t, pubkeyB, storedB)
}

// --- Epoch length persistence ---

func TestEpochLength_FallbackToDefault(t *testing.T) {
	k, ctx, _ := setupKeeper(t)
	params := pow.DefaultGenesisState().Params
	require.Equal(t, params.EpochLength, k.GetEpochLength(ctx))
}

func TestEpochLength_RoundTrip(t *testing.T) {
	k, ctx, _ := setupKeeper(t)
	k.SetEpochLength(ctx, 500)
	require.Equal(t, int64(500), k.GetEpochLength(ctx))
}

// --- CurrentEpoch ---

func TestCurrentEpoch_DerivedFromRealBlockHeight(t *testing.T) {
	k, ctx, _ := setupKeeper(t)
	k.SetEpochLength(ctx, 100)

	ctx = ctx.WithBlockHeight(0)
	require.Equal(t, int64(0), k.CurrentEpoch(ctx))

	ctx = ctx.WithBlockHeight(99)
	require.Equal(t, int64(0), k.CurrentEpoch(ctx))

	ctx = ctx.WithBlockHeight(100)
	require.Equal(t, int64(1), k.CurrentEpoch(ctx))

	ctx = ctx.WithBlockHeight(250)
	require.Equal(t, int64(2), k.CurrentEpoch(ctx))
}

func TestCurrentEpoch_HandlesZeroEpochLengthDefensively(t *testing.T) {
	k, ctx, _ := setupKeeper(t)
	k.SetEpochLength(ctx, 0)
	ctx = ctx.WithBlockHeight(50)

	// Must not panic (divide by zero) -- falls back to treating each
	// block as its own epoch rather than crashing on misconfiguration.
	require.NotPanics(t, func() {
		k.CurrentEpoch(ctx)
	})
}

// --- Mining work tracking ---

func TestMiningWork_StartsAtZero(t *testing.T) {
	k, ctx, _ := setupKeeper(t)
	minerAddr := sdk.AccAddress("unrecorded_miner_____")
	require.Equal(t, uint64(0), k.GetMiningWork(ctx, 5, minerAddr))
}

func TestMiningWork_AccumulatesWithinSameEpoch(t *testing.T) {
	k, ctx, _ := setupKeeper(t)
	minerAddr := sdk.AccAddress("accumulating_miner___")

	k.AddMiningWork(ctx, 3, minerAddr, 1)
	k.AddMiningWork(ctx, 3, minerAddr, 1)
	k.AddMiningWork(ctx, 3, minerAddr, 1)

	require.Equal(t, uint64(3), k.GetMiningWork(ctx, 3, minerAddr))
}

func TestMiningWork_SeparateEpochsDoNotMix(t *testing.T) {
	k, ctx, _ := setupKeeper(t)
	minerAddr := sdk.AccAddress("cross_epoch_miner____")

	k.AddMiningWork(ctx, 1, minerAddr, 5)
	k.AddMiningWork(ctx, 2, minerAddr, 7)

	require.Equal(t, uint64(5), k.GetMiningWork(ctx, 1, minerAddr))
	require.Equal(t, uint64(7), k.GetMiningWork(ctx, 2, minerAddr))
}

func TestMiningWork_SeparateMinersDoNotMix(t *testing.T) {
	k, ctx, _ := setupKeeper(t)
	minerA := sdk.AccAddress("miner_a_for_work_test")
	minerB := sdk.AccAddress("miner_b_for_work_test")

	k.AddMiningWork(ctx, 1, minerA, 10)
	k.AddMiningWork(ctx, 1, minerB, 20)

	require.Equal(t, uint64(10), k.GetMiningWork(ctx, 1, minerA))
	require.Equal(t, uint64(20), k.GetMiningWork(ctx, 1, minerB))
}

// --- IterateEpochWork ---

func TestIterateEpochWork_ReturnsAllEntriesForGivenEpoch(t *testing.T) {
	k, ctx, _ := setupKeeper(t)
	minerA := sdk.AccAddress("iterate_test_miner_a_")
	minerB := sdk.AccAddress("iterate_test_miner_b_")
	minerC := sdk.AccAddress("iterate_test_miner_c_")

	k.AddMiningWork(ctx, 7, minerA, 3)
	k.AddMiningWork(ctx, 7, minerB, 8)
	k.AddMiningWork(ctx, 7, minerC, 1)

	entries := k.IterateEpochWork(ctx, 7)
	require.Len(t, entries, 3)

	totalWork := uint64(0)
	for _, e := range entries {
		totalWork += e.Work
	}
	require.Equal(t, uint64(12), totalWork)
}

func TestIterateEpochWork_DoesNotLeakOtherEpochs(t *testing.T) {
	k, ctx, _ := setupKeeper(t)
	minerA := sdk.AccAddress("leak_test_miner_a____")
	minerB := sdk.AccAddress("leak_test_miner_b____")

	k.AddMiningWork(ctx, 4, minerA, 1)
	k.AddMiningWork(ctx, 5, minerB, 1)

	entries := k.IterateEpochWork(ctx, 4)
	require.Len(t, entries, 1, "epoch 4 iteration must not include epoch 5's entry")
	require.Equal(t, uint64(1), entries[0].Work)
}

func TestIterateEpochWork_EmptyEpochReturnsNoEntries(t *testing.T) {
	k, ctx, _ := setupKeeper(t)
	entries := k.IterateEpochWork(ctx, 999)
	require.Empty(t, entries)
}

func TestSubmitPoW_Success_RecordsMiningWorkForCurrentEpoch(t *testing.T) {
	k, ctx, _ := setupKeeper(t)
	srv := pow.NewMsgServerImpl(k)

	k.SetBlockReward(ctx, math.NewInt(5_000_000))
	k.SetEpochLength(ctx, 100)

	realHash := []byte("real-hash-for-epoch-work-test")
	ctx = setupRecentBlock(k, ctx, 50, realHash, 1) // height 50, still epoch 0 (50/100=0)
	ctx = ctx.WithBlockHeight(51)                   // 1 block later, well within recency window

	minerAddr, addrStr := validMinerAddr(t)
	msg := &pow.MsgSubmitPoW{
		Miner: addrStr, Height: 50, Timestamp: time.Now().Unix(),
		PrevHash: realHash, MerkleRoot: []byte("merkle"),
		Nonce: 1, Difficulty: 1,
	}

	_, err := srv.SubmitPoW(ctx, msg)
	require.NoError(t, err)

	require.Equal(t, uint64(1), k.GetMiningWork(ctx, 0, minerAddr))
}

// --- Active validator set storage ---

func TestActiveValidator_SetGetRemoveRoundTrip(t *testing.T) {
	k, ctx, _ := setupKeeper(t)
	minerAddr := sdk.AccAddress("active_validator_test")

	require.False(t, k.IsActiveValidator(ctx, minerAddr))

	k.SetActiveValidator(ctx, minerAddr)
	require.True(t, k.IsActiveValidator(ctx, minerAddr))

	k.RemoveActiveValidator(ctx, minerAddr)
	require.False(t, k.IsActiveValidator(ctx, minerAddr))
}

func TestIterateActiveValidators_ReturnsAllActive(t *testing.T) {
	k, ctx, _ := setupKeeper(t)
	minerA := sdk.AccAddress("iterate_active_miner_a")
	minerB := sdk.AccAddress("iterate_active_miner_b")

	k.SetActiveValidator(ctx, minerA)
	k.SetActiveValidator(ctx, minerB)

	addrs := k.IterateActiveValidators(ctx)
	require.Len(t, addrs, 2)
}

// --- ComputeValidatorUpdates ---

func genValidatorPubkey(t *testing.T) []byte {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(nil)
	require.NoError(t, err)
	return pub
}

func TestComputeValidatorUpdates_EmptyEpochReturnsNil(t *testing.T) {
	k, ctx, _ := setupKeeper(t)
	updates := k.ComputeValidatorUpdates(ctx, 1)
	require.Nil(t, updates)
}

func TestComputeValidatorUpdates_ExcludesMinersWithoutRegisteredPubkey(t *testing.T) {
	k, ctx, _ := setupKeeper(t)
	minerAddr := sdk.AccAddress("no_pubkey_registered__")

	// Mined, but never called RegisterValidatorPubkey.
	k.AddMiningWork(ctx, 1, minerAddr, 5)

	updates := k.ComputeValidatorUpdates(ctx, 1)
	require.Nil(t, updates, "a miner with no registered consensus pubkey must not be selectable")
}

func TestComputeValidatorUpdates_SingleQualifiedCandidateGetsPower(t *testing.T) {
	k, ctx, _ := setupKeeper(t)
	minerAddr := sdk.AccAddress("single_qualified_miner")
	pubkey := genValidatorPubkey(t)

	k.SetValidatorPubkey(ctx, minerAddr, pubkey)
	k.AddMiningWork(ctx, 1, minerAddr, 5)

	updates := k.ComputeValidatorUpdates(ctx, 1)
	require.Len(t, updates, 1)
	require.Equal(t, int64(pow.ValidatorVotingPower), updates[0].Power)
	require.True(t, k.IsActiveValidator(ctx, minerAddr))
}

func TestComputeValidatorUpdates_LimitsToTopK(t *testing.T) {
	k, ctx, _ := setupKeeper(t)
	k.SetTopKSize(ctx, 2)

	addrs := []sdk.AccAddress{
		sdk.AccAddress("top_k_candidate_one___"),
		sdk.AccAddress("top_k_candidate_two___"),
		sdk.AccAddress("top_k_candidate_three_"),
	}
	work := []uint64{10, 30, 20} // candidate two should rank first, three second, one excluded

	for i, addr := range addrs {
		k.SetValidatorPubkey(ctx, addr, genValidatorPubkey(t))
		k.AddMiningWork(ctx, 1, addr, work[i])
	}

	updates := k.ComputeValidatorUpdates(ctx, 1)
	require.Len(t, updates, 2, "only top-2 by work should be selected when TopKSize=2")

	require.True(t, k.IsActiveValidator(ctx, addrs[1]), "highest-work candidate should be selected")
	require.True(t, k.IsActiveValidator(ctx, addrs[2]), "second-highest-work candidate should be selected")
	require.False(t, k.IsActiveValidator(ctx, addrs[0]), "lowest-work candidate should be excluded")
}

func TestComputeValidatorUpdates_DeterministicTiebreakOnEqualWork(t *testing.T) {
	k, ctx, _ := setupKeeper(t)
	k.SetTopKSize(ctx, 1)

	addrLow := sdk.AccAddress{0x01}
	addrHigh := sdk.AccAddress{0xFF}

	k.SetValidatorPubkey(ctx, addrLow, genValidatorPubkey(t))
	k.SetValidatorPubkey(ctx, addrHigh, genValidatorPubkey(t))
	k.AddMiningWork(ctx, 1, addrLow, 10)
	k.AddMiningWork(ctx, 1, addrHigh, 10) // exactly equal work -- must break the tie deterministically

	updates := k.ComputeValidatorUpdates(ctx, 1)
	require.Len(t, updates, 1)
	// Lower raw address bytes wins the tiebreak, per ComputeValidatorUpdates'
	// sort comparator -- this must be identical on every node, or the
	// chain forks on who "actually" won the tie.
	require.True(t, k.IsActiveValidator(ctx, addrLow))
	require.False(t, k.IsActiveValidator(ctx, addrHigh))
}

func TestComputeValidatorUpdates_RemovesValidatorThatFallsOutOfTopK(t *testing.T) {
	k, ctx, _ := setupKeeper(t)
	k.SetTopKSize(ctx, 1)

	fallingOut := sdk.AccAddress("falling_out_validator_")
	replacement := sdk.AccAddress("replacement_validator_")

	// Simulate this address having been selected in a prior epoch.
	k.SetValidatorPubkey(ctx, fallingOut, genValidatorPubkey(t))
	k.SetActiveValidator(ctx, fallingOut)
	require.True(t, k.IsActiveValidator(ctx, fallingOut))

	// This epoch, a different, higher-work address qualifies instead.
	k.SetValidatorPubkey(ctx, replacement, genValidatorPubkey(t))
	k.AddMiningWork(ctx, 2, replacement, 100)

	updates := k.ComputeValidatorUpdates(ctx, 2)

	var sawRemoval, sawAddition bool
	for _, u := range updates {
		if u.Power == 0 {
			sawRemoval = true
		}
		if u.Power == int64(pow.ValidatorVotingPower) {
			sawAddition = true
		}
	}
	require.True(t, sawRemoval, "expected an explicit power=0 removal update for the validator that fell out")
	require.True(t, sawAddition, "expected the new candidate to receive a power grant")
	require.False(t, k.IsActiveValidator(ctx, fallingOut))
	require.True(t, k.IsActiveValidator(ctx, replacement))
}

func TestComputeValidatorUpdates_SafetyGuard_EmptyQualifiedPoolLeavesActiveSetUntouched(t *testing.T) {
	k, ctx, _ := setupKeeper(t)

	// Simulate an existing validator set from a prior epoch.
	existingValidator := sdk.AccAddress("existing_validator____")
	k.SetValidatorPubkey(ctx, existingValidator, genValidatorPubkey(t))
	k.SetActiveValidator(ctx, existingValidator)

	// This epoch: nobody mined at all (empty work), so the qualified pool
	// is empty. This is the exact scenario that could halt a chain if
	// handled wrong -- ComputeValidatorUpdates must NOT emit a removal
	// for the existing validator with nothing to replace it.
	updates := k.ComputeValidatorUpdates(ctx, 99)

	require.Nil(t, updates, "empty qualified pool must produce no updates at all")
	require.True(t, k.IsActiveValidator(ctx, existingValidator),
		"existing validator must remain active when there's no qualified replacement")
}

// --- Consensus address reverse index ---

func TestConsensusToMiner_NotFoundReturnsFalse(t *testing.T) {
	k, ctx, _ := setupKeeper(t)
	_, ok := k.GetMinerByConsensusAddr(ctx, []byte("no_such_consensus_addr"))
	require.False(t, ok)
}

func TestConsensusToMiner_RoundTrip(t *testing.T) {
	k, ctx, _ := setupKeeper(t)
	minerAddr := sdk.AccAddress("reverse_index_test_mi")
	consensusAddr := []byte("some_consensus_address")

	k.SetConsensusToMiner(ctx, consensusAddr, minerAddr)

	found, ok := k.GetMinerByConsensusAddr(ctx, consensusAddr)
	require.True(t, ok)
	require.Equal(t, minerAddr, found)
}

// --- Permanent ban ---

func TestBanned_DefaultsFalse(t *testing.T) {
	k, ctx, _ := setupKeeper(t)
	minerAddr := sdk.AccAddress("never_banned_miner____")
	require.False(t, k.IsBanned(ctx, minerAddr))
}

func TestBanned_SetIsPermanent(t *testing.T) {
	k, ctx, _ := setupKeeper(t)
	minerAddr := sdk.AccAddress("banned_miner_test_____")

	k.SetBanned(ctx, minerAddr)
	require.True(t, k.IsBanned(ctx, minerAddr))

	// There is deliberately no "unban" method -- permanent means permanent.
	// This test exists to make that design choice explicit and testable,
	// not just implicit in the absence of a method.
	require.True(t, k.IsBanned(ctx, minerAddr), "ban must remain set with no way to clear it")
}

// --- Bond cooldown persistence ---

func TestBondCooldown_FallbackToDefault(t *testing.T) {
	k, ctx, _ := setupKeeper(t)
	params := pow.DefaultGenesisState().Params
	require.Equal(t, params.BondCooldown, k.GetBondCooldown(ctx))
}

func TestBondCooldown_RoundTrip(t *testing.T) {
	k, ctx, _ := setupKeeper(t)
	k.SetBondCooldown(ctx, 250)
	require.Equal(t, int64(250), k.GetBondCooldown(ctx))
}

// --- Escrow ---

func TestEscrow_StartsAtZero(t *testing.T) {
	k, ctx, _ := setupKeeper(t)
	minerAddr := sdk.AccAddress("no_escrow_yet_________")
	require.True(t, k.GetEscrowBalance(ctx, minerAddr).IsZero())

	_, ok := k.GetEscrowUnlockHeight(ctx, minerAddr)
	require.False(t, ok)
}

func TestEscrow_AddAccumulates(t *testing.T) {
	k, ctx, _ := setupKeeper(t)
	minerAddr := sdk.AccAddress("accumulating_escrow___")

	k.AddEscrow(ctx, minerAddr, math.NewInt(100))
	k.AddEscrow(ctx, minerAddr, math.NewInt(50))

	require.True(t, k.GetEscrowBalance(ctx, minerAddr).Equal(math.NewInt(150)))
}

func TestEscrow_AddRefreshesUnlockHeight(t *testing.T) {
	k, ctx, _ := setupKeeper(t)
	k.SetBondCooldown(ctx, 100)
	minerAddr := sdk.AccAddress("unlock_refresh_test___")

	ctx = ctx.WithBlockHeight(10)
	k.AddEscrow(ctx, minerAddr, math.NewInt(100))
	unlockHeight, ok := k.GetEscrowUnlockHeight(ctx, minerAddr)
	require.True(t, ok)
	require.Equal(t, int64(110), unlockHeight)

	// A second contribution later must push the unlock height forward
	// again, not leave the original (earlier) one in place -- otherwise
	// a validator could "lock in" an early unlock height and then keep
	// earning escrowed rewards past it with no real cooldown enforced
	// on the newer funds.
	ctx = ctx.WithBlockHeight(50)
	k.AddEscrow(ctx, minerAddr, math.NewInt(50))
	unlockHeight, ok = k.GetEscrowUnlockHeight(ctx, minerAddr)
	require.True(t, ok)
	require.Equal(t, int64(150), unlockHeight)
}

func TestEscrow_ClearRemovesBalanceAndUnlockHeight(t *testing.T) {
	k, ctx, _ := setupKeeper(t)
	minerAddr := sdk.AccAddress("clear_escrow_test_____")

	k.AddEscrow(ctx, minerAddr, math.NewInt(100))
	k.ClearEscrow(ctx, minerAddr)

	require.True(t, k.GetEscrowBalance(ctx, minerAddr).IsZero())
	_, ok := k.GetEscrowUnlockHeight(ctx, minerAddr)
	require.False(t, ok)
}

func TestEscrow_SeparateMinersDoNotMix(t *testing.T) {
	k, ctx, _ := setupKeeper(t)
	minerA := sdk.AccAddress("escrow_miner_a________")
	minerB := sdk.AccAddress("escrow_miner_b________")

	k.AddEscrow(ctx, minerA, math.NewInt(100))
	k.AddEscrow(ctx, minerB, math.NewInt(200))

	require.True(t, k.GetEscrowBalance(ctx, minerA).Equal(math.NewInt(100)))
	require.True(t, k.GetEscrowBalance(ctx, minerB).Equal(math.NewInt(200)))
}

func TestIterateEscrows_ReturnsAllNonZeroEscrows(t *testing.T) {
	k, ctx, _ := setupKeeper(t)
	minerA := sdk.AccAddress("iterate_escrow_a______")
	minerB := sdk.AccAddress("iterate_escrow_b______")

	k.AddEscrow(ctx, minerA, math.NewInt(10))
	k.AddEscrow(ctx, minerB, math.NewInt(20))

	addrs := k.IterateEscrows(ctx)
	require.Len(t, addrs, 2)
}

func TestIterateEscrows_ExcludesClearedEscrows(t *testing.T) {
	k, ctx, _ := setupKeeper(t)
	minerA := sdk.AccAddress("cleared_escrow_a______")
	minerB := sdk.AccAddress("cleared_escrow_b______")

	k.AddEscrow(ctx, minerA, math.NewInt(10))
	k.AddEscrow(ctx, minerB, math.NewInt(20))
	k.ClearEscrow(ctx, minerA)

	addrs := k.IterateEscrows(ctx)
	require.Len(t, addrs, 1, "cleared escrow must not appear in iteration")
}

func TestDistributeBlockReward_ActiveValidatorSharesEscrowedNotPaidDirectly(t *testing.T) {
	k, ctx, mockBank := setupKeeper(t)
	miner := sdk.AccAddress("escrowed_validator____")

	k.SetBlockReward(ctx, math.NewInt(5_000_000))
	k.SetActiveValidator(ctx, miner)

	err := k.DistributeBlockReward(ctx, miner)
	require.NoError(t, err)

	require.Len(t, mockBank.MintCalls, 1)
	require.Len(t, mockBank.SendCalls, 1, "only the fee-collector's 2/15 share should be sent directly")

	// Since this miner is the sole active validator, their escrow
	// receives both their own 85% mining share (4,250,000) AND the full
	// 13/15 validator treasury share (650,000, since they're the only
	// validator to split it with) -- total 4,900,000.
	require.True(t, k.GetEscrowBalance(ctx, miner).Equal(math.NewInt(4_900_000)))
	_, hasUnlock := k.GetEscrowUnlockHeight(ctx, miner)
	require.True(t, hasUnlock, "escrow contribution must set an unlock height")
}

func TestDistributeBlockReward_NonValidatorStillPaidDirectly(t *testing.T) {
	k, ctx, mockBank := setupKeeper(t)
	miner := sdk.AccAddress("non_validator_miner___")

	k.SetBlockReward(ctx, math.NewInt(5_000_000))
	// Deliberately NOT calling SetActiveValidator -- this is the regular,
	// non-validator mining path, which must be unaffected by escrow logic.

	err := k.DistributeBlockReward(ctx, miner)
	require.NoError(t, err)

	require.Len(t, mockBank.SendCalls, 2, "both the miner's direct payout and the treasury-cut send should happen")
	require.True(t, k.GetEscrowBalance(ctx, miner).IsZero(), "a non-validator's reward must not be escrowed")
}

// --- Pending removal storage ---

func TestPendingRemoval_MarkAndIterate(t *testing.T) {
	k, ctx, _ := setupKeeper(t)
	minerAddr := sdk.AccAddress("pending_removal_test__")

	k.MarkPendingRemoval(ctx, minerAddr)
	addrs := k.IteratePendingRemovals(ctx)
	require.Len(t, addrs, 1)
	require.Equal(t, minerAddr, addrs[0])
}

func TestPendingRemoval_ClearRemovesIt(t *testing.T) {
	k, ctx, _ := setupKeeper(t)
	minerAddr := sdk.AccAddress("clear_pending_removal_")

	k.MarkPendingRemoval(ctx, minerAddr)
	k.ClearPendingRemoval(ctx, minerAddr)

	addrs := k.IteratePendingRemovals(ctx)
	require.Empty(t, addrs)
}

// --- ProcessMisbehavior ---

func withFakeEvidence(ctx sdk.Context, consensusAddr []byte) sdk.Context {
	evidence := testutil.FakeEvidence{
		MisbehaviorType:    comet.MisbehaviorType(1), // DUPLICATE_VOTE, per comet's constants
		OffendingValidator: testutil.FakeValidator{Addr: consensusAddr, Pow: 1_000_000},
		AtHeight:           1,
		AtTime:             time.Now(),
		VotingPowerTotal:   1_000_000,
	}
	blockInfo := testutil.FakeBlockInfo{
		Evidence: testutil.FakeEvidenceList{evidence},
	}
	return ctx.WithCometInfo(blockInfo)
}

func TestProcessMisbehavior_NoEvidenceIsNoOp(t *testing.T) {
	k, ctx, _ := setupKeeper(t)
	ctx = ctx.WithCometInfo(testutil.FakeBlockInfo{Evidence: testutil.FakeEvidenceList{}})

	require.NotPanics(t, func() {
		k.ProcessMisbehavior(ctx)
	})
}

func TestProcessMisbehavior_UnrecognizedConsensusAddressIsIgnored(t *testing.T) {
	k, ctx, _ := setupKeeper(t)
	ctx = withFakeEvidence(ctx, []byte("nobody_registered_this"))

	require.NotPanics(t, func() {
		k.ProcessMisbehavior(ctx)
	})
	// No panic, and nothing should have been banned since we can't map
	// this consensus address back to any miner.
}

func TestProcessMisbehavior_BansAndBurnsEscrow(t *testing.T) {
	k, ctx, mockBank := setupKeeper(t)
	minerAddr := sdk.AccAddress("equivocating_validator")
	consensusPub, _, err := ed25519.GenerateKey(nil)
	require.NoError(t, err)
	consensusAddr := cometed25519.PubKey(consensusPub).Address()

	k.SetValidatorPubkey(ctx, minerAddr, consensusPub)
	k.SetConsensusToMiner(ctx, consensusAddr, minerAddr)
	k.SetActiveValidator(ctx, minerAddr)
	k.AddEscrow(ctx, minerAddr, math.NewInt(1_000_000))

	ctx = withFakeEvidence(ctx, consensusAddr)
	k.ProcessMisbehavior(ctx)

	require.True(t, k.IsBanned(ctx, minerAddr), "miner must be permanently banned")
	require.True(t, k.GetEscrowBalance(ctx, minerAddr).IsZero(), "escrow must be fully forfeited")
	require.False(t, k.IsActiveValidator(ctx, minerAddr), "miner must lose active validator status immediately")

	pending := k.IteratePendingRemovals(ctx)
	require.Len(t, pending, 1, "must be queued for immediate ValidatorUpdate removal")
	require.Equal(t, minerAddr, pending[0])

	require.Len(t, mockBank.BurnCalls, 1)
	require.Equal(t, "1000000aeth", mockBank.BurnCalls[0].Coins.String())
}

func TestProcessMisbehavior_ZeroEscrowStillBansButDoesNotCallBurn(t *testing.T) {
	k, ctx, mockBank := setupKeeper(t)
	minerAddr := sdk.AccAddress("zero_escrow_offender__")
	consensusPub, _, err := ed25519.GenerateKey(nil)
	require.NoError(t, err)
	consensusAddr := cometed25519.PubKey(consensusPub).Address()

	k.SetValidatorPubkey(ctx, minerAddr, consensusPub)
	k.SetConsensusToMiner(ctx, consensusAddr, minerAddr)
	k.SetActiveValidator(ctx, minerAddr)
	// Deliberately no AddEscrow call -- nothing pending.

	ctx = withFakeEvidence(ctx, consensusAddr)
	k.ProcessMisbehavior(ctx)

	require.Empty(t, mockBank.BurnCalls, "should not attempt to burn again for an already-banned miner")
}

// --- CreditTreasuryShareToValidators ---

func TestCreditTreasuryShareToValidators_NoActiveValidatorsReturnsZero(t *testing.T) {
	k, ctx, _ := setupKeeper(t)
	credited := k.CreditTreasuryShareToValidators(ctx, math.NewInt(1000))
	require.True(t, credited.IsZero())
}

func TestCreditTreasuryShareToValidators_SplitsEvenly(t *testing.T) {
	k, ctx, _ := setupKeeper(t)
	minerA := sdk.AccAddress("treasury_split_miner_a")
	minerB := sdk.AccAddress("treasury_split_miner_b")

	k.SetActiveValidator(ctx, minerA)
	k.SetActiveValidator(ctx, minerB)

	credited := k.CreditTreasuryShareToValidators(ctx, math.NewInt(1000))

	require.True(t, credited.Equal(math.NewInt(1000)), "1000 splits evenly across 2 validators with no remainder")
	require.True(t, k.GetEscrowBalance(ctx, minerA).Equal(math.NewInt(500)))
	require.True(t, k.GetEscrowBalance(ctx, minerB).Equal(math.NewInt(500)))
}

func TestCreditTreasuryShareToValidators_TruncationRemainderNotLost(t *testing.T) {
	k, ctx, _ := setupKeeper(t)
	minerA := sdk.AccAddress("truncation_miner_a____")
	minerB := sdk.AccAddress("truncation_miner_b____")
	minerC := sdk.AccAddress("truncation_miner_c____")

	k.SetActiveValidator(ctx, minerA)
	k.SetActiveValidator(ctx, minerB)
	k.SetActiveValidator(ctx, minerC)

	// 1000 / 3 = 333 remainder 1 -- the returned "actually credited" total
	// must reflect only what was really distributed (999), not the
	// original 1000, so the caller can correctly route the 1 leftover
	// unit to the fee collector instead of it silently vanishing.
	credited := k.CreditTreasuryShareToValidators(ctx, math.NewInt(1000))

	require.True(t, credited.Equal(math.NewInt(999)))
	require.True(t, k.GetEscrowBalance(ctx, minerA).Equal(math.NewInt(333)))
	require.True(t, k.GetEscrowBalance(ctx, minerB).Equal(math.NewInt(333)))
	require.True(t, k.GetEscrowBalance(ctx, minerC).Equal(math.NewInt(333)))
}

func TestCreditTreasuryShareToValidators_TooSmallToDivideReturnsZero(t *testing.T) {
	k, ctx, _ := setupKeeper(t)
	minerA := sdk.AccAddress("too_small_miner_a_____")
	minerB := sdk.AccAddress("too_small_miner_b_____")
	minerC := sdk.AccAddress("too_small_miner_c_____")

	k.SetActiveValidator(ctx, minerA)
	k.SetActiveValidator(ctx, minerB)
	k.SetActiveValidator(ctx, minerC)

	// 2 / 3 = 0 per validator -- nothing meaningful to distribute.
	credited := k.CreditTreasuryShareToValidators(ctx, math.NewInt(2))

	require.True(t, credited.IsZero())
	require.True(t, k.GetEscrowBalance(ctx, minerA).IsZero())
}

// --- DistributeBlockReward's 13/2 treasury split ---

func TestDistributeBlockReward_NoActiveValidators_TreasuryGoesEntirelyToTreasury(t *testing.T) {
	k, ctx, mockBank := setupKeeper(t)
	miner := sdk.AccAddress("no_validators_miner___")

	k.SetBlockReward(ctx, math.NewInt(5_000_000))
	// No active validators registered at all.

	err := k.DistributeBlockReward(ctx, miner)
	require.NoError(t, err)

	// Miner's own payout (non-validator, direct) + full 750000 treasury
	// cut to x/treasury -- 2 sends total, nothing escrowed for anyone.
	require.Len(t, mockBank.SendCalls, 2)

	var treasurySend *testutil.SendCall
	for i := range mockBank.SendCalls {
		if mockBank.SendCalls[i].RecipientModule == treasury.ModuleName {
			treasurySend = &mockBank.SendCalls[i]
		}
	}
	require.NotNil(t, treasurySend)
	require.Equal(t, "750000aeth", treasurySend.Coins.String(), "entire treasury cut goes to x/treasury when no validators exist")
}

func TestDistributeBlockReward_WithActiveValidators_SplitsThirteenTwo(t *testing.T) {
	k, ctx, mockBank := setupKeeper(t)
	miner := sdk.AccAddress("reward_split_miner____")
	validatorA := sdk.AccAddress("reward_split_val_a____")

	k.SetBlockReward(ctx, math.NewInt(5_000_000))
	k.SetActiveValidator(ctx, validatorA)
	// miner itself is not a validator in this test -- keeps the miner
	// payout path simple so we can isolate the treasury-split assertion.

	err := k.DistributeBlockReward(ctx, miner)
	require.NoError(t, err)

	// Treasury cut = 750000 (15% of 5,000,000).
	// Validator share = 750000 * 13/15 = 650000, all to the single active validator.
	// Treasury-fund share = 750000 - 650000 = 100000 (the 2/15 portion).
	require.True(t, k.GetEscrowBalance(ctx, validatorA).Equal(math.NewInt(650_000)))

	var treasurySend *testutil.SendCall
	for i := range mockBank.SendCalls {
		if mockBank.SendCalls[i].RecipientModule == treasury.ModuleName {
			treasurySend = &mockBank.SendCalls[i]
		}
	}
	require.NotNil(t, treasurySend)
	require.Equal(t, "100000aeth", treasurySend.Coins.String())
}

func TestComputeValidatorUpdates_ExcludesPermanentlyBannedAddress(t *testing.T) {
	k, ctx, _ := setupKeeper(t)
	bannedMiner := sdk.AccAddress("banned_but_still_mining")
	pubkey := genValidatorPubkey(t)

	k.SetValidatorPubkey(ctx, bannedMiner, pubkey)
	k.SetBanned(ctx, bannedMiner) // banned in some earlier epoch for equivocation

	// Same address somehow accumulates new mining work in a later epoch --
	// this is exactly the resurfacing scenario the ban check must prevent.
	k.AddMiningWork(ctx, 5, bannedMiner, 1000)

	updates := k.ComputeValidatorUpdates(ctx, 5)

	require.Nil(t, updates, "a banned address must never be selectable again, regardless of new mining work")
	require.False(t, k.IsActiveValidator(ctx, bannedMiner))
}

func TestComputeValidatorUpdates_BannedAddressExcludedButOthersStillQualify(t *testing.T) {
	k, ctx, _ := setupKeeper(t)
	bannedMiner := sdk.AccAddress("banned_among_others____")
	honestMiner := sdk.AccAddress("honest_miner_among_them")

	k.SetValidatorPubkey(ctx, bannedMiner, genValidatorPubkey(t))
	k.SetBanned(ctx, bannedMiner)
	k.AddMiningWork(ctx, 6, bannedMiner, 1000) // highest work, but banned

	k.SetValidatorPubkey(ctx, honestMiner, genValidatorPubkey(t))
	k.AddMiningWork(ctx, 6, honestMiner, 10) // lower work, but not banned

	updates := k.ComputeValidatorUpdates(ctx, 6)

	require.Len(t, updates, 1, "only the non-banned candidate should be selected")
	require.True(t, k.IsActiveValidator(ctx, honestMiner))
	require.False(t, k.IsActiveValidator(ctx, bannedMiner))
}

func TestReleaseMaturedEscrows_NotYetMaturedStaysLocked(t *testing.T) {
	k, ctx, mockBank := setupKeeper(t)
	k.SetBondCooldown(ctx, 100)
	minerAddr := sdk.AccAddress("not_matured_yet_______")

	ctx = ctx.WithBlockHeight(10)
	k.AddEscrow(ctx, minerAddr, math.NewInt(1000)) // unlocks at height 110

	ctx = ctx.WithBlockHeight(50) // still before unlock
	k.ReleaseMaturedEscrows(ctx)

	require.True(t, k.GetEscrowBalance(ctx, minerAddr).Equal(math.NewInt(1000)), "escrow must remain locked before cooldown completes")
	require.Empty(t, mockBank.SendCalls)
}

func TestReleaseMaturedEscrows_MaturedEscrowIsPaidOutAndCleared(t *testing.T) {
	k, ctx, mockBank := setupKeeper(t)
	k.SetBondCooldown(ctx, 100)
	minerAddr := sdk.AccAddress("matured_escrow_test___")

	ctx = ctx.WithBlockHeight(10)
	k.AddEscrow(ctx, minerAddr, math.NewInt(1000)) // unlocks at height 110

	ctx = ctx.WithBlockHeight(110) // exactly at unlock height
	k.ReleaseMaturedEscrows(ctx)

	require.True(t, k.GetEscrowBalance(ctx, minerAddr).IsZero(), "escrow must be cleared after release")
	require.Len(t, mockBank.SendCalls, 1)
	require.Equal(t, minerAddr, mockBank.SendCalls[0].RecipientAddr)
	require.Equal(t, "1000aeth", mockBank.SendCalls[0].Coins.String())
}

func TestReleaseMaturedEscrows_BannedAddressIsSkipped(t *testing.T) {
	k, ctx, mockBank := setupKeeper(t)
	k.SetBondCooldown(ctx, 100)
	minerAddr := sdk.AccAddress("banned_before_release_")

	ctx = ctx.WithBlockHeight(10)
	k.AddEscrow(ctx, minerAddr, math.NewInt(1000))
	k.SetBanned(ctx, minerAddr) // banned before this test's release check runs

	ctx = ctx.WithBlockHeight(110) // would otherwise be matured
	k.ReleaseMaturedEscrows(ctx)

	require.Empty(t, mockBank.SendCalls, "a banned address's escrow must never be paid out")
}

func TestReleaseMaturedEscrows_MultipleValidatorsReleasedIndependently(t *testing.T) {
	k, ctx, mockBank := setupKeeper(t)
	k.SetBondCooldown(ctx, 100)
	minerEarly := sdk.AccAddress("early_escrow_miner____")
	minerLate := sdk.AccAddress("late_escrow_miner_____")

	ctx = ctx.WithBlockHeight(0)
	k.AddEscrow(ctx, minerEarly, math.NewInt(500)) // unlocks at height 100

	ctx = ctx.WithBlockHeight(80)
	k.AddEscrow(ctx, minerLate, math.NewInt(700)) // unlocks at height 180

	ctx = ctx.WithBlockHeight(100) // minerEarly matured, minerLate is not
	k.ReleaseMaturedEscrows(ctx)

	require.True(t, k.GetEscrowBalance(ctx, minerEarly).IsZero())
	require.True(t, k.GetEscrowBalance(ctx, minerLate).Equal(math.NewInt(700)), "minerLate must remain locked")
	require.Len(t, mockBank.SendCalls, 1, "only the matured escrow should be released this call")
}

// --- RecencyWindowK persistence ---

func TestRecencyWindowK_FallbackToDefault(t *testing.T) {
	k, ctx, _ := setupKeeper(t)
	params := pow.DefaultGenesisState().Params
	require.Equal(t, params.RecencyWindowK, k.GetRecencyWindowK(ctx))
}

func TestRecencyWindowK_RoundTrip(t *testing.T) {
	k, ctx, _ := setupKeeper(t)
	k.SetRecencyWindowK(ctx, 25)
	require.Equal(t, int64(25), k.GetRecencyWindowK(ctx))
}

// --- RecordRecentBlock / GetRecentHash / GetRecentDifficulty ---

func TestRecordRecentBlock_StoresHashAndDifficulty(t *testing.T) {
	k, ctx, _ := setupKeeper(t)
	k.SetDifficulty(ctx, math.NewInt(12345))
	ctx = ctx.WithBlockHeight(50).WithHeaderHash([]byte("fake-block-hash-at-height-50"))

	k.RecordRecentBlock(ctx)

	hash, ok := k.GetRecentHash(ctx, 50)
	require.True(t, ok)
	require.Equal(t, []byte("fake-block-hash-at-height-50"), hash)

	difficulty, ok := k.GetRecentDifficulty(ctx, 50)
	require.True(t, ok)
	require.True(t, difficulty.Equal(math.NewInt(12345)))
}

func TestRecordRecentBlock_PrunesOldEntriesBeyondRetentionWindow(t *testing.T) {
	k, ctx, _ := setupKeeper(t)
	k.SetRecencyWindowK(ctx, 10) // retain = K+2 = 12

	for h := int64(0); h <= 20; h++ {
		ctx = ctx.WithBlockHeight(h).WithHeaderHash([]byte(fmt.Sprintf("hash-%d", h)))
		k.RecordRecentBlock(ctx)
	}

	// At height 20, retain=12, prune target = 20-12 = 8. Height 8 should be gone.
	_, ok := k.GetRecentHash(ctx, 8)
	require.False(t, ok, "height 8 should have been pruned by the time we reach height 20")

	// Height 9 should still exist (just inside the retention window).
	_, ok = k.GetRecentHash(ctx, 9)
	require.True(t, ok, "height 9 should still be retained")
}

func TestGetRecentHash_UnknownHeightReturnsFalse(t *testing.T) {
	k, ctx, _ := setupKeeper(t)
	_, ok := k.GetRecentHash(ctx, 999)
	require.False(t, ok)
}

// --- AcceptedWork anti-replay ---

func TestAcceptedWork_NotAcceptedByDefault(t *testing.T) {
	k, ctx, _ := setupKeeper(t)
	require.False(t, k.IsWorkAccepted(ctx, []byte("some-header-hash")))
}

func TestAcceptedWork_MarkAndCheck(t *testing.T) {
	k, ctx, _ := setupKeeper(t)
	headerHash := []byte("a-specific-header-hash")

	k.MarkWorkAccepted(ctx, headerHash)
	require.True(t, k.IsWorkAccepted(ctx, headerHash))
}

func TestAcceptedWork_DifferentHashesDoNotCollide(t *testing.T) {
	k, ctx, _ := setupKeeper(t)
	hashA := []byte("header-hash-a")
	hashB := []byte("header-hash-b")

	k.MarkWorkAccepted(ctx, hashA)

	require.True(t, k.IsWorkAccepted(ctx, hashA))
	require.False(t, k.IsWorkAccepted(ctx, hashB))
}

func TestBootstrapValidator_RegistersIntoTrackedState(t *testing.T) {
	k, ctx, _ := setupKeeper(t)

	consensusPub, _, err := ed25519.GenerateKey(nil)
	require.NoError(t, err)

	protoPubKey, err := cometencoding.PubKeyToProto(cometed25519.PubKey(consensusPub))
	require.NoError(t, err)

	err = k.BootstrapValidator(ctx, protoPubKey)
	require.NoError(t, err)

	derivedMinerAddr := sdk.AccAddress(cometed25519.PubKey(consensusPub).Address())

	require.True(t, k.IsActiveValidator(ctx, derivedMinerAddr),
		"bootstrap validator must be tracked as an active validator, or it can never be removed via Top-K")

	storedPubkey, ok := k.GetValidatorPubkey(ctx, derivedMinerAddr)
	require.True(t, ok)
	require.Equal(t, []byte(consensusPub), storedPubkey)

	consensusAddr := cometed25519.PubKey(consensusPub).Address()
	foundMiner, ok := k.GetMinerByConsensusAddr(ctx, consensusAddr)
	require.True(t, ok)
	require.Equal(t, derivedMinerAddr, foundMiner)
}

func TestBootstrapValidator_IdempotentOnRepeatedCalls(t *testing.T) {
	k, ctx, _ := setupKeeper(t)

	consensusPub, _, err := ed25519.GenerateKey(nil)
	require.NoError(t, err)
	protoPubKey, err := cometencoding.PubKeyToProto(cometed25519.PubKey(consensusPub))
	require.NoError(t, err)

	err = k.BootstrapValidator(ctx, protoPubKey)
	require.NoError(t, err)
	err = k.BootstrapValidator(ctx, protoPubKey) // simulates a node restart re-running InitChainer
	require.NoError(t, err, "must not error on repeated bootstrap of the same validator")

	derivedMinerAddr := sdk.AccAddress(cometed25519.PubKey(consensusPub).Address())
	require.True(t, k.IsActiveValidator(ctx, derivedMinerAddr))
}

func TestBootstrapValidator_SubjectToTopKRemovalLikeAnyOtherValidator(t *testing.T) {
	k, ctx, _ := setupKeeper(t)
	k.SetTopKSize(ctx, 1)

	// Bootstrap validator, but it does no mining work this epoch.
	bootstrapPub, _, err := ed25519.GenerateKey(nil)
	require.NoError(t, err)
	bootstrapProto, err := cometencoding.PubKeyToProto(cometed25519.PubKey(bootstrapPub))
	require.NoError(t, err)
	require.NoError(t, k.BootstrapValidator(ctx, bootstrapProto))

	// A real, actively-mining candidate qualifies instead.
	minerAddr := sdk.AccAddress("actively_mining_candidate")
	k.SetValidatorPubkey(ctx, minerAddr, genValidatorPubkey(t))
	k.AddMiningWork(ctx, 1, minerAddr, 100)

	updates := k.ComputeValidatorUpdates(ctx, 1)

	bootstrapMinerAddr := sdk.AccAddress(cometed25519.PubKey(bootstrapPub).Address())

	// This is the actual bug being fixed: before BootstrapValidator existed,
	// the bootstrap validator would NEVER appear in this removal check at
	// all, regardless of how the rest of Top-K evolved.
	require.False(t, k.IsActiveValidator(ctx, bootstrapMinerAddr),
		"bootstrap validator must be removable via Top-K just like any other validator")
	require.True(t, k.IsActiveValidator(ctx, minerAddr))

	var sawBootstrapRemoval bool
	for _, u := range updates {
		if u.Power == 0 {
			sawBootstrapRemoval = true
		}
	}
	require.True(t, sawBootstrapRemoval, "expected an explicit power=0 removal for the bootstrap validator")
}

// --- Tenure tracking ---

func TestGetValidatorTenureRatio_ZeroForNeverActive(t *testing.T) {
	k, ctx, _ := setupKeeper(t)
	minerAddr := sdk.AccAddress("never_active_validator")
	ratio := k.GetValidatorTenureRatio(ctx, minerAddr)
	require.True(t, ratio.IsZero())
}

func TestGetValidatorTenureRatio_ZeroImmediatelyOnEntry(t *testing.T) {
	k, ctx, _ := setupKeeper(t)
	minerAddr := sdk.AccAddress("just_entered_validator")

	ctx = ctx.WithBlockTime(time.Now())
	k.SetActiveValidator(ctx, minerAddr)

	ratio := k.GetValidatorTenureRatio(ctx, minerAddr)
	require.True(t, ratio.IsZero(), "tenure should be exactly zero the instant a validator enters")
}

func TestGetValidatorTenureRatio_RampsLinearlyOverTime(t *testing.T) {
	k, ctx, _ := setupKeeper(t)
	minerAddr := sdk.AccAddress("ramping_validator______")

	start := time.Now()
	ctx = ctx.WithBlockTime(start)
	k.SetActiveValidator(ctx, minerAddr)

	// Halfway through the 30-day ramp.
	ctx = ctx.WithBlockTime(start.Add(15 * 24 * time.Hour))
	ratio := k.GetValidatorTenureRatio(ctx, minerAddr)
	require.True(t, ratio.Sub(math.LegacyMustNewDecFromStr("0.5")).Abs().LT(math.LegacyMustNewDecFromStr("0.01")),
		"expected ratio close to 0.5 at 15 of 30 days, got %s", ratio.String())
}

func TestGetValidatorTenureRatio_CapsAtOneAfterRampDuration(t *testing.T) {
	k, ctx, _ := setupKeeper(t)
	minerAddr := sdk.AccAddress("fully_tenured_validator")

	start := time.Now()
	ctx = ctx.WithBlockTime(start)
	k.SetActiveValidator(ctx, minerAddr)

	// Well past 30 days.
	ctx = ctx.WithBlockTime(start.Add(90 * 24 * time.Hour))
	ratio := k.GetValidatorTenureRatio(ctx, minerAddr)
	require.True(t, ratio.Equal(math.LegacyOneDec()), "ratio must cap at exactly 1.0, got %s", ratio.String())
}

func TestGetValidatorTenureRatio_ResetsToZeroOnRemoval(t *testing.T) {
	k, ctx, _ := setupKeeper(t)
	minerAddr := sdk.AccAddress("removed_then_gone______")

	start := time.Now()
	ctx = ctx.WithBlockTime(start)
	k.SetActiveValidator(ctx, minerAddr)

	ctx = ctx.WithBlockTime(start.Add(20 * 24 * time.Hour))
	require.False(t, k.GetValidatorTenureRatio(ctx, minerAddr).IsZero(), "sanity check: should have nonzero tenure before removal")

	k.RemoveActiveValidator(ctx, minerAddr)
	require.True(t, k.GetValidatorTenureRatio(ctx, minerAddr).IsZero(), "tenure must reset to zero on removal")
}

func TestGetValidatorTenureRatio_ReSelectionDoesNotResetTenure(t *testing.T) {
	k, ctx, _ := setupKeeper(t)
	minerAddr := sdk.AccAddress("repeatedly_selected____")

	start := time.Now()
	ctx = ctx.WithBlockTime(start)
	k.SetActiveValidator(ctx, minerAddr) // first entry

	ctx = ctx.WithBlockTime(start.Add(10 * 24 * time.Hour))
	k.SetActiveValidator(ctx, minerAddr) // re-selected next epoch, STILL active -- must not reset

	ctx = ctx.WithBlockTime(start.Add(20 * 24 * time.Hour))
	k.SetActiveValidator(ctx, minerAddr) // re-selected again

	// Tenure should reflect the full 20 days since ORIGINAL entry, not
	// reset to zero at either re-selection.
	ratio := k.GetValidatorTenureRatio(ctx, minerAddr)
	expectedRatio := math.LegacyNewDec(20).QuoInt64(30)
	require.True(t, ratio.Sub(expectedRatio).Abs().LT(math.LegacyMustNewDecFromStr("0.01")),
		"expected ratio reflecting 20 continuous days, got %s", ratio.String())
}

func TestGetValidatorTenureRatio_DifferentValidatorsTrackedIndependently(t *testing.T) {
	k, ctx, _ := setupKeeper(t)
	minerEarly := sdk.AccAddress("early_entry_validator__")
	minerLate := sdk.AccAddress("late_entry_validator___")

	start := time.Now()
	ctx = ctx.WithBlockTime(start)
	k.SetActiveValidator(ctx, minerEarly)

	ctx = ctx.WithBlockTime(start.Add(15 * 24 * time.Hour))
	k.SetActiveValidator(ctx, minerLate)

	ctx = ctx.WithBlockTime(start.Add(30 * 24 * time.Hour))
	earlyRatio := k.GetValidatorTenureRatio(ctx, minerEarly)
	lateRatio := k.GetValidatorTenureRatio(ctx, minerLate)

	require.True(t, earlyRatio.Equal(math.LegacyOneDec()), "early validator should be fully ramped (30 of 30 days)")
	require.True(t, lateRatio.Sub(math.LegacyMustNewDecFromStr("0.5")).Abs().LT(math.LegacyMustNewDecFromStr("0.01")),
		"late validator should be at ~0.5 (15 of 30 days), got %s", lateRatio.String())
}

func TestDistributeBlockReward_CallsFundTreasuryWithCorrectAmount(t *testing.T) {
	k, ctx, _, mockTreasury := setupKeeperWithTreasury(t)
	miner := sdk.AccAddress("fund_treasury_miner___")

	k.SetBlockReward(ctx, math.NewInt(5_000_000))

	err := k.DistributeBlockReward(ctx, miner)
	require.NoError(t, err)

	require.Len(t, mockTreasury.FundCalls, 1)
	require.True(t, mockTreasury.FundCalls[0].Amount.Equal(math.NewInt(750_000)),
		"expected the full 750000 treasury cut (no active validators) to fund treasury")
}

func TestDistributeBlockReward_WithActiveValidators_FundsTreasuryWithRemainderOnly(t *testing.T) {
	k, ctx, _, mockTreasury := setupKeeperWithTreasury(t)
	miner := sdk.AccAddress("fund_treasury_miner_2_")
	validatorA := sdk.AccAddress("fund_treasury_val_a___")

	k.SetBlockReward(ctx, math.NewInt(5_000_000))
	k.SetActiveValidator(ctx, validatorA)

	err := k.DistributeBlockReward(ctx, miner)
	require.NoError(t, err)

	require.Len(t, mockTreasury.FundCalls, 1)
	require.True(t, mockTreasury.FundCalls[0].Amount.Equal(math.NewInt(100_000)),
		"expected only the 2/15 remainder (100000) to fund treasury, not the full 750000 cut")
}

func TestDistributeBlockReward_ZeroTreasuryAmountDoesNotCallFundTreasury(t *testing.T) {
	k, ctx, _, mockTreasury := setupKeeperWithTreasury(t)
	miner := sdk.AccAddress("fund_treasury_zero____")

	k.SetBlockReward(ctx, math.NewInt(0)) // zero reward -> DistributeBlockReward no-ops entirely

	err := k.DistributeBlockReward(ctx, miner)
	require.NoError(t, err)
	require.Empty(t, mockTreasury.FundCalls)
}
