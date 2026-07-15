package pow_test

import (
	"errors"
	"testing"
	"time"

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
	k := pow.NewKeeper(cdc, storeKey, log.NewNopLogger(), mockBank)

	return k, ctx, mockBank
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
	require.Equal(t, "fee_collector", treasurySend.RecipientModule)
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

	k.SetDifficulty(ctx, math.NewInt(1))
	k.SetBlockReward(ctx, math.NewInt(5_000_000))
	k.SetEpochLength(ctx, 100)
	ctx = ctx.WithBlockHeight(50) // epoch 0

	minerAddr, addrStr := validMinerAddr(t)
	msg := &pow.MsgSubmitPoW{
		Miner: addrStr, Height: 1, Timestamp: time.Now().Unix(),
		PrevHash: []byte("prev"), MerkleRoot: []byte("merkle"),
		Nonce: 1, Difficulty: 1,
	}

	_, err := srv.SubmitPoW(ctx, msg)
	require.NoError(t, err)

	require.Equal(t, uint64(1), k.GetMiningWork(ctx, 0, minerAddr))
}