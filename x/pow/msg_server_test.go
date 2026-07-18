package pow_test

import (
	"errors"
	"testing"
	"time"

	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"
	"crypto/ed25519"
	"github.com/whoyoujoshin/aether/x/pow"
	"github.com/whoyoujoshin/aether/x/pow/types"
	cometed25519 "github.com/cometbft/cometbft/crypto/ed25519" 
)

// validMinerAddr is a real bech32-encoded address derived from arbitrary
// bytes, used wherever a syntactically valid miner address is needed.
func validMinerAddr(t *testing.T) (sdk.AccAddress, string) {
	t.Helper()
	addr := sdk.AccAddress("valid_miner_address_")
	return addr, addr.String()
}

func TestSubmitPoW_RejectsInvalidMinerAddress(t *testing.T) {
	k, ctx, _ := setupKeeper(t)
	srv := pow.NewMsgServerImpl(k)

	msg := &pow.MsgSubmitPoW{
		Miner:      "not-a-valid-bech32-address",
		Height:     1,
		Timestamp:  time.Now().Unix(),
		PrevHash:   []byte("prev"),
		MerkleRoot: []byte("merkle"),
		Nonce:      1,
		Difficulty: 1,
	}

	_, err := srv.SubmitPoW(ctx, msg)
	require.Error(t, err)
	require.True(t, errors.Is(err, types.ErrInvalidCreator), "expected ErrInvalidCreator, got: %v", err)
}

func TestSubmitPoW_RejectsDifficultyBelowRequired(t *testing.T) {
	k, ctx, _ := setupKeeper(t)
	srv := pow.NewMsgServerImpl(k)

	// Chain currently requires a much higher difficulty than what's submitted.
	k.SetDifficulty(ctx, math.NewInt(1_000_000))

	_, addrStr := validMinerAddr(t)
	msg := &pow.MsgSubmitPoW{
		Miner:      addrStr,
		Height:     1,
		Timestamp:  time.Now().Unix(),
		PrevHash:   []byte("prev"),
		MerkleRoot: []byte("merkle"),
		Nonce:      1,
		Difficulty: 1, // far below the required 1,000,000
	}

	_, err := srv.SubmitPoW(ctx, msg)
	require.Error(t, err)
	require.True(t, errors.Is(err, types.ErrInvalidPoW), "expected ErrInvalidPoW, got: %v", err)
}

func TestSubmitPoW_RejectsFailedVerification(t *testing.T) {
	k, ctx, _ := setupKeeper(t)
	srv := pow.NewMsgServerImpl(k)

	// Set required difficulty very high so the threshold check passes,
	// but an arbitrary nonce is astronomically unlikely to satisfy the
	// actual hash target — so VerifyMiningHeader should fail here.
	highDifficulty := uint64(1) << 40
	k.SetDifficulty(ctx, math.NewInt(int64(highDifficulty)))

	_, addrStr := validMinerAddr(t)
	msg := &pow.MsgSubmitPoW{
		Miner:      addrStr,
		Height:     1,
		Timestamp:  time.Now().Unix(),
		PrevHash:   []byte("prev"),
		MerkleRoot: []byte("merkle"),
		Nonce:      42, // essentially never satisfies a target this small
		Difficulty: highDifficulty,
	}

	_, err := srv.SubmitPoW(ctx, msg)
	require.Error(t, err)
	require.True(t, errors.Is(err, types.ErrInvalidPoW), "expected ErrInvalidPoW, got: %v", err)
}

func TestSubmitPoW_SucceedsAndDistributesReward(t *testing.T) {
	k, ctx, mockBank := setupKeeper(t)
	srv := pow.NewMsgServerImpl(k)

	// Trivial difficulty so any nonce satisfies VerifyMiningHeader, and the
	// stored difficulty matches so the threshold check passes too.
	k.SetDifficulty(ctx, math.NewInt(1))
	k.SetBlockReward(ctx, math.NewInt(5_000_000))

	minerAddr, addrStr := validMinerAddr(t)
	msg := &pow.MsgSubmitPoW{
		Miner:      addrStr,
		Height:     1,
		Timestamp:  time.Now().Unix(),
		PrevHash:   []byte("prev"),
		MerkleRoot: []byte("merkle"),
		Nonce:      1,
		Difficulty: 1,
	}

	resp, err := srv.SubmitPoW(ctx, msg)
	require.NoError(t, err)
	require.NotNil(t, resp)

	// Reward should have actually been distributed via the bank keeper.
	require.Len(t, mockBank.MintCalls, 1)
	require.Equal(t, "5000000aeth", mockBank.MintCalls[0].Coins.String())
	require.Len(t, mockBank.SendCalls, 2)
	require.Equal(t, minerAddr, mockBank.SendCalls[0].RecipientAddr)

	// LastBlockTime should be updated after a successful submission.
	lastTime, ok := k.GetLastBlockTime(ctx)
	require.True(t, ok)
	require.Equal(t, ctx.BlockTime().Unix(), lastTime)
}

func TestSubmitPoW_PropagatesRewardDistributionError(t *testing.T) {
	k, ctx, mockBank := setupKeeper(t)
	srv := pow.NewMsgServerImpl(k)

	k.SetDifficulty(ctx, math.NewInt(1))
	k.SetBlockReward(ctx, math.NewInt(5_000_000))
	mockBank.MintErr = errors.New("bank layer failure")

	_, addrStr := validMinerAddr(t)
	msg := &pow.MsgSubmitPoW{
		Miner:      addrStr,
		Height:     1,
		Timestamp:  time.Now().Unix(),
		PrevHash:   []byte("prev"),
		MerkleRoot: []byte("merkle"),
		Nonce:      1,
		Difficulty: 1,
	}

	_, err := srv.SubmitPoW(ctx, msg)
	require.Error(t, err)
	require.Contains(t, err.Error(), "bank layer failure")
}

func TestSubmitPoW_Success_UpdatesDifficultyAndLastBlockTime(t *testing.T) {
	k, ctx, _ := setupKeeper(t)
	srv := pow.NewMsgServerImpl(k)

	k.SetDifficulty(ctx, math.NewInt(1))
	k.SetBlockReward(ctx, math.NewInt(5_000_000))

	// Confirm no LastBlockTime exists yet (fresh keeper).
	_, hadLastTime := k.GetLastBlockTime(ctx)
	require.False(t, hadLastTime)

	_, addrStr := validMinerAddr(t)
	msg := &pow.MsgSubmitPoW{
		Miner:      addrStr,
		Height:     1,
		Timestamp:  time.Now().Unix(),
		PrevHash:   []byte("prev"),
		MerkleRoot: []byte("merkle"),
		Nonce:      1,
		Difficulty: 1,
	}

	_, err := srv.SubmitPoW(ctx, msg)
	require.NoError(t, err)

	// AdjustDifficulty should have run as a side effect of the successful
	// submission (previously this only happened in BeginBlocker, entirely
	// decoupled from whether a submission occurred at all).
	lastTime, ok := k.GetLastBlockTime(ctx)
	require.True(t, ok, "LastBlockTime should be set after a successful submission")
	require.Equal(t, ctx.BlockTime().Unix(), lastTime)
}

func TestSubmitPoW_Success_DifficultyRetargetsBasedOnSubmissionTiming(t *testing.T) {
	k, ctx, _ := setupKeeper(t)
	srv := pow.NewMsgServerImpl(k)

	// Set an explicit, low MinDifficulty so the default floor (1024) doesn't
	// interfere with observing the raw retargeting math below.
	k.SetMinDifficulty(ctx, 1)
	k.SetTargetBlockTime(ctx, 60)
	k.SetDifficulty(ctx, math.NewInt(1))
	priorTime := time.Now().Unix()
	k.SetLastBlockTime(ctx, priorTime)

	submissionTime := priorTime + 5
	ctx = ctx.WithBlockTime(time.Unix(submissionTime, 0))

	_, addrStr := validMinerAddr(t)
	msg := &pow.MsgSubmitPoW{
		Miner: addrStr, Height: 1, Timestamp: submissionTime,
		PrevHash: []byte("prev"), MerkleRoot: []byte("merkle"),
		Nonce: 1, Difficulty: 1,
	}

	_, err := srv.SubmitPoW(ctx, msg)
	require.NoError(t, err)

	// Expected: current(1) * target(60) / elapsed(5) = 12
	newDifficulty := k.GetDifficulty(ctx)
	require.True(t, newDifficulty.Equal(math.NewInt(12)),
		"expected difficulty to retarget to 12 (1*60/5), got %s", newDifficulty.String())
}

func TestSubmitPoW_FailedVerification_DoesNotAdjustDifficulty(t *testing.T) {
	k, ctx, _ := setupKeeper(t)
	srv := pow.NewMsgServerImpl(k)

	highDifficulty := uint64(1) << 40
	k.SetDifficulty(ctx, math.NewInt(int64(highDifficulty)))

	_, addrStr := validMinerAddr(t)
	msg := &pow.MsgSubmitPoW{
		Miner: addrStr, Height: 1, Timestamp: time.Now().Unix(),
		PrevHash: []byte("prev"), MerkleRoot: []byte("merkle"),
		Nonce: 42, Difficulty: highDifficulty, // essentially never satisfies this target
	}

	_, err := srv.SubmitPoW(ctx, msg)
	require.Error(t, err)

	// A failed PoW verification must not advance LastBlockTime or difficulty
	// -- otherwise a flood of invalid submissions could manipulate retargeting.
	_, hadLastTime := k.GetLastBlockTime(ctx)
	require.False(t, hadLastTime, "LastBlockTime must not be set on a failed submission")
}

func TestSubmitPoW_FailedDifficultyThreshold_DoesNotAdjustDifficulty(t *testing.T) {
	k, ctx, _ := setupKeeper(t)
	srv := pow.NewMsgServerImpl(k)

	k.SetDifficulty(ctx, math.NewInt(1_000_000))

	_, addrStr := validMinerAddr(t)
	msg := &pow.MsgSubmitPoW{
		Miner: addrStr, Height: 1, Timestamp: time.Now().Unix(),
		PrevHash: []byte("prev"), MerkleRoot: []byte("merkle"),
		Nonce: 1, Difficulty: 1, // far below the required 1,000,000
	}

	_, err := srv.SubmitPoW(ctx, msg)
	require.Error(t, err)

	_, hadLastTime := k.GetLastBlockTime(ctx)
	require.False(t, hadLastTime, "LastBlockTime must not be set when difficulty check fails")
}

func TestSubmitPoW_FailedRewardDistribution_DoesNotAdjustDifficulty(t *testing.T) {
	k, ctx, mockBank := setupKeeper(t)
	srv := pow.NewMsgServerImpl(k)

	k.SetDifficulty(ctx, math.NewInt(1))
	k.SetBlockReward(ctx, math.NewInt(5_000_000))
	mockBank.MintErr = errors.New("bank layer failure")

	_, addrStr := validMinerAddr(t)
	msg := &pow.MsgSubmitPoW{
		Miner: addrStr, Height: 1, Timestamp: time.Now().Unix(),
		PrevHash: []byte("prev"), MerkleRoot: []byte("merkle"),
		Nonce: 1, Difficulty: 1,
	}

	_, err := srv.SubmitPoW(ctx, msg)
	require.Error(t, err)

	// Verification passed and reward distribution was attempted, but since
	// it failed, difficulty/LastBlockTime must not be touched -- the
	// ordering in SubmitPoW places AdjustDifficulty strictly after a
	// successful DistributeBlockReward call.
	_, hadLastTime := k.GetLastBlockTime(ctx)
	require.False(t, hadLastTime, "LastBlockTime must not be set when reward distribution fails")
}

func TestRegisterValidatorPubkey_Success(t *testing.T) {
	k, ctx, _ := setupKeeper(t)
	srv := pow.NewMsgServerImpl(k)

	minerAddr, addrStr := validMinerAddr(t)
	consensusPub, consensusPriv, err := ed25519.GenerateKey(nil)
	require.NoError(t, err)

	sig := ed25519.Sign(consensusPriv, []byte(addrStr))

	msg := &pow.MsgRegisterValidatorPubkey{
		Miner:           addrStr,
		ConsensusPubkey: consensusPub,
		Signature:       sig,
	}

	_, err = srv.RegisterValidatorPubkey(ctx, msg)
	require.NoError(t, err)

	stored, ok := k.GetValidatorPubkey(ctx, minerAddr)
	require.True(t, ok)
	require.Equal(t, []byte(consensusPub), stored)
}

func TestRegisterValidatorPubkey_RejectsInvalidMinerAddress(t *testing.T) {
	k, ctx, _ := setupKeeper(t)
	srv := pow.NewMsgServerImpl(k)

	msg := &pow.MsgRegisterValidatorPubkey{
		Miner:           "not-a-valid-bech32-address",
		ConsensusPubkey: make([]byte, ed25519.PublicKeySize),
		Signature:       make([]byte, ed25519.SignatureSize),
	}

	_, err := srv.RegisterValidatorPubkey(ctx, msg)
	require.Error(t, err)
	require.True(t, errors.Is(err, types.ErrInvalidCreator))
}

func TestRegisterValidatorPubkey_RejectsWrongSizePubkey(t *testing.T) {
	k, ctx, _ := setupKeeper(t)
	srv := pow.NewMsgServerImpl(k)

	_, addrStr := validMinerAddr(t)
	msg := &pow.MsgRegisterValidatorPubkey{
		Miner:           addrStr,
		ConsensusPubkey: []byte{0x01, 0x02, 0x03}, // far too short
		Signature:       make([]byte, ed25519.SignatureSize),
	}

	_, err := srv.RegisterValidatorPubkey(ctx, msg)
	require.Error(t, err)
	require.True(t, errors.Is(err, types.ErrInvalidConsensusPubkey))
}

func TestRegisterValidatorPubkey_RejectsInvalidSignature(t *testing.T) {
	k, ctx, _ := setupKeeper(t)
	srv := pow.NewMsgServerImpl(k)

	_, addrStr := validMinerAddr(t)
	consensusPub, _, err := ed25519.GenerateKey(nil)
	require.NoError(t, err)

	msg := &pow.MsgRegisterValidatorPubkey{
		Miner:           addrStr,
		ConsensusPubkey: consensusPub,
		Signature:       make([]byte, ed25519.SignatureSize), // all zeros, not a real signature
	}

	_, err = srv.RegisterValidatorPubkey(ctx, msg)
	require.Error(t, err)
	require.True(t, errors.Is(err, types.ErrInvalidProofOfPossession))
}

func TestRegisterValidatorPubkey_RejectsSignatureFromWrongKey(t *testing.T) {
	k, ctx, _ := setupKeeper(t)
	srv := pow.NewMsgServerImpl(k)

	_, addrStr := validMinerAddr(t)

	// The pubkey being registered belongs to keypair A, but the signature
	// was produced by keypair B -- this is exactly the attack proof-of-
	// possession exists to prevent (registering a pubkey you don't
	// actually control the private key for).
	pubkeyA, _, err := ed25519.GenerateKey(nil)
	require.NoError(t, err)
	_, privB, err := ed25519.GenerateKey(nil)
	require.NoError(t, err)

	sig := ed25519.Sign(privB, []byte(addrStr))

	msg := &pow.MsgRegisterValidatorPubkey{
		Miner:           addrStr,
		ConsensusPubkey: pubkeyA,
		Signature:       sig,
	}

	_, err = srv.RegisterValidatorPubkey(ctx, msg)
	require.Error(t, err)
	require.True(t, errors.Is(err, types.ErrInvalidProofOfPossession))
}

func TestRegisterValidatorPubkey_OverwritesExistingRegistration(t *testing.T) {
	k, ctx, _ := setupKeeper(t)
	srv := pow.NewMsgServerImpl(k)

	minerAddr, addrStr := validMinerAddr(t)

	firstPub, firstPriv, err := ed25519.GenerateKey(nil)
	require.NoError(t, err)
	firstSig := ed25519.Sign(firstPriv, []byte(addrStr))
	_, err = srv.RegisterValidatorPubkey(ctx, &pow.MsgRegisterValidatorPubkey{
		Miner: addrStr, ConsensusPubkey: firstPub, Signature: firstSig,
	})
	require.NoError(t, err)

	// Re-registering with a different, properly-proven key should replace
	// the prior mapping.
	secondPub, secondPriv, err := ed25519.GenerateKey(nil)
	require.NoError(t, err)
	secondSig := ed25519.Sign(secondPriv, []byte(addrStr))
	_, err = srv.RegisterValidatorPubkey(ctx, &pow.MsgRegisterValidatorPubkey{
		Miner: addrStr, ConsensusPubkey: secondPub, Signature: secondSig,
	})
	require.NoError(t, err)

	stored, ok := k.GetValidatorPubkey(ctx, minerAddr)
	require.True(t, ok)
	require.Equal(t, []byte(secondPub), stored)
}

func TestRegisterValidatorPubkey_PopulatesConsensusToMinerIndex(t *testing.T) {
	k, ctx, _ := setupKeeper(t)
	srv := pow.NewMsgServerImpl(k)

	minerAddr, addrStr := validMinerAddr(t)
	consensusPub, consensusPriv, err := ed25519.GenerateKey(nil)
	require.NoError(t, err)
	sig := ed25519.Sign(consensusPriv, []byte(addrStr))

	_, err = srv.RegisterValidatorPubkey(ctx, &pow.MsgRegisterValidatorPubkey{
		Miner: addrStr, ConsensusPubkey: consensusPub, Signature: sig,
	})
	require.NoError(t, err)

	consensusAddr := cometed25519.PubKey(consensusPub).Address()
	foundMiner, ok := k.GetMinerByConsensusAddr(ctx, consensusAddr)
	require.True(t, ok)
	require.Equal(t, minerAddr, foundMiner)
}