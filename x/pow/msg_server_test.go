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
	cometencoding "github.com/cometbft/cometbft/crypto/encoding"
	abci "github.com/cometbft/cometbft/abci/types"
)

// validMinerAddr is a real bech32-encoded address derived from arbitrary
// bytes, used wherever a syntactically valid miner address is needed.
func validMinerAddr(t *testing.T) (sdk.AccAddress, string) {
	t.Helper()
	addr := sdk.AccAddress("valid_miner_address_")
	return addr, addr.String()
}

func newNativeSubmitMsg(miner string, height uint64, timestamp int64, prevHash, merkleRoot []byte, nonce, difficulty uint64) *pow.MsgSubmitPoW {
	return &pow.MsgSubmitPoW{
		Miner: miner,
		Submission: &pow.MsgSubmitPoW_Native{
			Native: &pow.NativeSubmission{
				Height:     height,
				Timestamp:  timestamp,
				PrevHash:   prevHash,
				MerkleRoot: merkleRoot,
				Nonce:      nonce,
				Difficulty: difficulty,
			},
		},
	}
}

func TestSubmitPoW_RejectsInvalidMinerAddress(t *testing.T) {
	k, ctx, _ := setupKeeper(t)
	srv := pow.NewMsgServerImpl(k)

	msg := &pow.MsgSubmitPoW{
Miner: "not-a-valid-bech32-address",
Submission: &pow.MsgSubmitPoW_Native{
Native: &pow.NativeSubmission{
Height:     1,
Timestamp:  time.Now().Unix(),
PrevHash:   []byte("prev"),
MerkleRoot: []byte("merkle"),
Nonce:      1,
Difficulty: 1,
},
},
}

	_, err := srv.SubmitPoW(ctx, msg)
	require.Error(t, err)
	require.True(t, errors.Is(err, types.ErrInvalidCreator), "expected ErrInvalidCreator, got: %v", err)
}

func TestSubmitPoW_RejectsDifficultyBelowRequired(t *testing.T) {
	k, ctx, _ := setupKeeper(t)
	srv := pow.NewMsgServerImpl(k)

	realHash := []byte("real-hash-for-difficulty-test")
	ctx = setupRecentBlock(k, ctx, 1, realHash, 1_000_000)
	ctx = ctx.WithBlockHeight(2)

	_, addrStr := validMinerAddr(t)
	msg := &pow.MsgSubmitPoW{
	Miner: addrStr,
	Submission: &pow.MsgSubmitPoW_Native{
		Native: &pow.NativeSubmission{
			Height: 1, Timestamp: time.Now().Unix(),
			PrevHash: realHash, MerkleRoot: []byte("merkle"),
			Nonce: 1, Difficulty: 1, // far below the required 1,000,000
		},
	},
}

	_, err := srv.SubmitPoW(ctx, msg)
	require.Error(t, err)
	require.True(t, errors.Is(err, types.ErrInvalidPoW))
}

func TestSubmitPoW_RejectsFailedVerification(t *testing.T) {
	k, ctx, _ := setupKeeper(t)
	srv := pow.NewMsgServerImpl(k)

	// Set required difficulty very high so the threshold check passes,
	// but an arbitrary nonce is astronomically unlikely to satisfy the
	// actual hash target — so VerifyMiningHeader should fail here.
	highDifficulty := uint64(1) << 40
	realHash := []byte("real-hash-for-failed-verification-test")
	ctx = setupRecentBlock(k, ctx, 1, realHash, int64(highDifficulty))
	ctx = ctx.WithBlockHeight(2)

	_, addrStr := validMinerAddr(t)
	msg := &pow.MsgSubmitPoW{
	Miner: addrStr,
	Submission: &pow.MsgSubmitPoW_Native{
		Native: &pow.NativeSubmission{
			Height:     1,
			Timestamp:  time.Now().Unix(),
			PrevHash:   realHash,
			MerkleRoot: []byte("merkle"),
			Nonce:      42, // essentially never satisfies a target this small
			Difficulty: highDifficulty,
		},
	},
}
	_, err := srv.SubmitPoW(ctx, msg)
	require.Error(t, err)
	require.True(t, errors.Is(err, types.ErrInvalidPoW), "expected ErrInvalidPoW, got: %v", err)
}

func TestSubmitPoW_SucceedsAndDistributesReward(t *testing.T) {
	k, ctx, mockBank := setupKeeper(t)
	srv := pow.NewMsgServerImpl(k)

	realHash := []byte("real-hash-for-success-test")
	ctx = setupRecentBlock(k, ctx, 1, realHash, 1)
	ctx = ctx.WithBlockHeight(2)
	k.SetBlockReward(ctx, math.NewInt(5_000_000))

	minerAddr, addrStr := validMinerAddr(t)
	msg := &pow.MsgSubmitPoW{
Miner: addrStr,
Submission: &pow.MsgSubmitPoW_Native{
Native: &pow.NativeSubmission{
Height:     1,
Timestamp:  time.Now().Unix(),
PrevHash:   realHash,
MerkleRoot: []byte("merkle"),
Nonce:      1,
Difficulty: 1,
},
},
}

	resp, err := srv.SubmitPoW(ctx, msg)
	require.NoError(t, err)
	require.NotNil(t, resp)

	require.Len(t, mockBank.MintCalls, 1)
	require.Equal(t, "5000000uaeth", mockBank.MintCalls[0].Coins.String())
	require.Len(t, mockBank.SendCalls, 2)
	require.Equal(t, minerAddr, mockBank.SendCalls[0].RecipientAddr)

	lastTime, ok := k.GetLastBlockTime(ctx)
	require.True(t, ok)
	require.Equal(t, ctx.BlockTime().Unix(), lastTime)
}

// TestSubmitPoW_BannedMiner_StillMintsFullBlockReward is a real,
// live-flagged concern (Gitty, Section 3 item 3): does a permanently
// banned (post-equivocation) miner actually get rejected at the
// message-handling level, or only excluded from Top-K/rewards?
//
// Confirmed: neither submitNativePoW nor submitAuxPoW calls IsBanned
// anywhere. IsBanned is checked in exactly one place in the entire
// module -- ComputeValidatorUpdates's Top-K qualification filter,
// which only decides who's eligible to be an ACTIVE VALIDATOR. It has
// no bearing on SubmitPoW at all. A banned miner can keep submitting
// indefinitely (subject only to the one-per-block-height cap and
// duplicate-work rejection, neither of which are ban-specific) and
// receives the FULL real block reward every time, exactly as if they
// were never banned. This is worse than "excluded from Top-K/rewards"
// -- it is not excluded from rewards at all, only from becoming a
// validator again. A banned miner keeps farming real minted AETH
// forever.
func TestSubmitPoW_BannedMiner_StillMintsFullBlockReward(t *testing.T) {
	k, ctx, mockBank := setupKeeper(t)
	srv := pow.NewMsgServerImpl(k)

	realHash := []byte("real-hash-for-banned-miner-test")
	ctx = setupRecentBlock(k, ctx, 1, realHash, 1)
	ctx = ctx.WithBlockHeight(2)
	k.SetBlockReward(ctx, math.NewInt(5_000_000))

	minerAddr, addrStr := validMinerAddr(t)
	k.SetBanned(ctx, minerAddr)
	require.True(t, k.IsBanned(ctx, minerAddr), "precondition: miner must actually be banned")

	msg := &pow.MsgSubmitPoW{
		Miner: addrStr,
		Submission: &pow.MsgSubmitPoW_Native{
			Native: &pow.NativeSubmission{
				Height: 1, Timestamp: time.Now().Unix(), PrevHash: realHash,
				MerkleRoot: []byte("merkle"), Nonce: 1, Difficulty: 1,
			},
		},
	}

	resp, err := srv.SubmitPoW(ctx, msg)
	require.NoError(t, err, "BUG: a banned miner's submission currently succeeds -- SubmitPoW never checks IsBanned")
	require.NotNil(t, resp)

	require.Len(t, mockBank.MintCalls, 1, "BUG: a banned miner still triggers a real MintCoins call for the full block reward")
	require.Equal(t, "5000000uaeth", mockBank.MintCalls[0].Coins.String())
}

func TestSubmitPoW_PropagatesRewardDistributionError(t *testing.T) {
	k, ctx, mockBank := setupKeeper(t)
	srv := pow.NewMsgServerImpl(k)

	realHash := []byte("real-hash-for-reward-error-test")
	ctx = setupRecentBlock(k, ctx, 1, realHash, 1)
	ctx = ctx.WithBlockHeight(2)
	k.SetBlockReward(ctx, math.NewInt(5_000_000))
	mockBank.MintErr = errors.New("bank layer failure")

	_, addrStr := validMinerAddr(t)
	msg := &pow.MsgSubmitPoW{
Miner: addrStr,
Submission: &pow.MsgSubmitPoW_Native{
Native: &pow.NativeSubmission{
Height:     1,
Timestamp:  time.Now().Unix(),
PrevHash:   realHash,
MerkleRoot: []byte("merkle"),
Nonce:      1,
Difficulty: 1,
},
},
}

	_, err := srv.SubmitPoW(ctx, msg)
	require.Error(t, err)
	require.Contains(t, err.Error(), "bank layer failure")
}

func TestSubmitPoW_Success_UpdatesDifficultyAndLastBlockTime(t *testing.T) {
	k, ctx, _ := setupKeeper(t)
	srv := pow.NewMsgServerImpl(k)

	realHash := []byte("real-hash-for-difficulty-update-test")
	ctx = setupRecentBlock(k, ctx, 1, realHash, 1)
	ctx = ctx.WithBlockHeight(2)
	k.SetBlockReward(ctx, math.NewInt(5_000_000))

	_, hadLastTime := k.GetLastBlockTime(ctx)
	require.False(t, hadLastTime)

	_, addrStr := validMinerAddr(t)
	msg := &pow.MsgSubmitPoW{
Miner: addrStr,
Submission: &pow.MsgSubmitPoW_Native{
Native: &pow.NativeSubmission{
Height:     1,
Timestamp:  time.Now().Unix(),
PrevHash:   realHash,
MerkleRoot: []byte("merkle"),
Nonce:      1,
Difficulty: 1,
},
},
}

	_, err := srv.SubmitPoW(ctx, msg)
	require.NoError(t, err)

	lastTime, ok := k.GetLastBlockTime(ctx)
	require.True(t, ok, "LastBlockTime should be set after a successful submission")
	require.Equal(t, ctx.BlockTime().Unix(), lastTime)
}

func TestSubmitPoW_Success_DifficultyRetargetsBasedOnSubmissionTiming(t *testing.T) {
	k, ctx, _ := setupKeeper(t)
	srv := pow.NewMsgServerImpl(k)

	k.SetMinDifficulty(ctx, 1)
	k.SetTargetBlockTime(ctx, 60)

	realHash := []byte("real-hash-for-retarget-test")
	ctx = setupRecentBlock(k, ctx, 1, realHash, 1)
	ctx = ctx.WithBlockHeight(2)

	priorTime := time.Now().Unix()
	k.SetLastBlockTime(ctx, priorTime)

	submissionTime := priorTime + 5
	ctx = ctx.WithBlockTime(time.Unix(submissionTime, 0))

	_, addrStr := validMinerAddr(t)
	msg := &pow.MsgSubmitPoW{
Miner: addrStr,
Submission: &pow.MsgSubmitPoW_Native{
Native: &pow.NativeSubmission{
Height:     1,
Timestamp:  submissionTime,
PrevHash:   realHash,
MerkleRoot: []byte("merkle"),
Nonce:      1,
Difficulty: 1,
},
},
}

	_, err := srv.SubmitPoW(ctx, msg)
	require.NoError(t, err)

	newDifficulty := k.GetDifficulty(ctx)
	require.True(t, newDifficulty.Equal(math.NewInt(12)),
		"expected difficulty to retarget to 12 (1*60/5), got %s", newDifficulty.String())
}

func TestSubmitPoW_FailedVerification_DoesNotAdjustDifficulty(t *testing.T) {
	k, ctx, _ := setupKeeper(t)
	srv := pow.NewMsgServerImpl(k)

	highDifficulty := uint64(1) << 40
	realHash := []byte("real-hash-failed-verification-no-adjust")
	ctx = setupRecentBlock(k, ctx, 1, realHash, int64(highDifficulty))
	ctx = ctx.WithBlockHeight(2)

	_, addrStr := validMinerAddr(t)
	msg := &pow.MsgSubmitPoW{
Miner: addrStr,
Submission: &pow.MsgSubmitPoW_Native{
Native: &pow.NativeSubmission{
Height:     1,
Timestamp:  time.Now().Unix(),
PrevHash:   realHash,
MerkleRoot: []byte("merkle"),
Nonce:      42,
Difficulty: highDifficulty,
},
},
}

	_, err := srv.SubmitPoW(ctx, msg)
	require.Error(t, err)

	_, hadLastTime := k.GetLastBlockTime(ctx)
	require.False(t, hadLastTime, "LastBlockTime must not be set on a failed submission")
}

func TestSubmitPoW_FailedDifficultyThreshold_DoesNotAdjustDifficulty(t *testing.T) {
	k, ctx, _ := setupKeeper(t)
	srv := pow.NewMsgServerImpl(k)

	realHash := []byte("real-hash-failed-difficulty-threshold")
	ctx = setupRecentBlock(k, ctx, 1, realHash, 1_000_000)
	ctx = ctx.WithBlockHeight(2)

	_, addrStr := validMinerAddr(t)
	msg := &pow.MsgSubmitPoW{
	Miner: addrStr,
	Submission: &pow.MsgSubmitPoW_Native{
		Native: &pow.NativeSubmission{
			Height: 1, Timestamp: time.Now().Unix(),
			PrevHash: realHash, MerkleRoot: []byte("merkle"),
			Nonce: 1, Difficulty: 1, // far below the required 1,000,000
		},
	},
}

	_, err := srv.SubmitPoW(ctx, msg)
	require.Error(t, err)

	_, hadLastTime := k.GetLastBlockTime(ctx)
	require.False(t, hadLastTime, "LastBlockTime must not be set when difficulty check fails")
}

func TestSubmitPoW_FailedRewardDistribution_DoesNotAdjustDifficulty(t *testing.T) {
	k, ctx, mockBank := setupKeeper(t)
	srv := pow.NewMsgServerImpl(k)

	realHash := []byte("real-hash-failed-reward-distribution")
	ctx = setupRecentBlock(k, ctx, 1, realHash, 1)
	ctx = ctx.WithBlockHeight(2)
	k.SetBlockReward(ctx, math.NewInt(5_000_000))
	mockBank.MintErr = errors.New("bank layer failure")

	_, addrStr := validMinerAddr(t)
	msg := &pow.MsgSubmitPoW{
Miner: addrStr,
Submission: &pow.MsgSubmitPoW_Native{
Native: &pow.NativeSubmission{
Height:     1,
Timestamp:  time.Now().Unix(),
PrevHash:   realHash,
MerkleRoot: []byte("merkle"),
Nonce:      1,
Difficulty: 1,
},
},
}

	_, err := srv.SubmitPoW(ctx, msg)
	require.Error(t, err)

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

// TestRegisterValidatorPubkey_RotationOrphansOldKeysCometBFTPower is a
// real, live-flagged concern (Gitty, Section 3 item 1): does
// re-registering a new consensus pubkey for an already-active miner
// retire the old one? It does not, and this is a genuine consensus
// vulnerability, not just stale bookkeeping.
//
// RegisterValidatorPubkey itself never emits an abci.ValidatorUpdate --
// it only rewrites keeper state. The ONLY code path that can ever
// revoke a validator's real CometBFT voting power is the epoch-boundary
// removal loop in ComputeValidatorUpdates, and that loop builds its
// revocation from whatever GetValidatorPubkey CURRENTLY returns for the
// miner -- never whatever pubkey actually held power at the time. So a
// miner who is active (real power under key A), then registers a new
// key B, then later gets dropped from Top-K, has their removal update
// issued for B -- a key that never held any power to begin with. Key
// A's real, live CometBFT voting power is never targeted by any
// revocation anywhere in this module and stays live in the validator
// set forever (until/unless some unrelated event, like a future
// equivocation catch against A specifically, removes it). A miner can
// use this to accumulate multiple simultaneously-powered validator
// identities under one economic actor by simply rotating keys while
// active, undetectable from active-validator-count alone.
func TestRegisterValidatorPubkey_RotationOrphansOldKeysCometBFTPower(t *testing.T) {
	k, ctx, _ := setupKeeper(t)
	srv := pow.NewMsgServerImpl(k)

	minerAddr, addrStr := validMinerAddr(t)

	// Miner registers key A and (in the real world) is selected into
	// Top-K under it, so CometBFT grants key A real voting power.
	// Simulating that directly here, the same way the rest of this
	// suite treats "already active" as a precondition.
	oldPub, oldPriv, err := ed25519.GenerateKey(nil)
	require.NoError(t, err)
	oldSig := ed25519.Sign(oldPriv, []byte(addrStr))
	_, err = srv.RegisterValidatorPubkey(ctx, &pow.MsgRegisterValidatorPubkey{
		Miner: addrStr, ConsensusPubkey: oldPub, Signature: oldSig,
	})
	require.NoError(t, err)
	k.SetActiveValidator(ctx, minerAddr)

	oldConsensusAddr := cometed25519.PubKey(oldPub).Address()

	// Miner rotates to a new key B -- e.g. routine key hygiene, nothing
	// malicious required to trigger this.
	newPub, newPriv, err := ed25519.GenerateKey(nil)
	require.NoError(t, err)
	newSig := ed25519.Sign(newPriv, []byte(addrStr))
	_, err = srv.RegisterValidatorPubkey(ctx, &pow.MsgRegisterValidatorPubkey{
		Miner: addrStr, ConsensusPubkey: newPub, Signature: newSig,
	})
	require.NoError(t, err)

	// The old consensus address is still live in the reverse index --
	// it is never cleared on rotation.
	_, stillIndexed := k.GetMinerByConsensusAddr(ctx, oldConsensusAddr)
	require.True(t, stillIndexed, "the OLD consensus address must still resolve after rotation -- SetConsensusToMiner is additive-only, nothing ever deletes the prior entry")

	// Now the miner fails to qualify for the next epoch (no mining
	// work) and must be removed. ComputeValidatorUpdates short-circuits
	// to a no-op whenever NOBODY mined this epoch (the empty-qualified-
	// pool safety guard), so a second, unrelated miner needs real
	// qualifying work this epoch for the removal loop to run at all.
	epoch := k.CurrentEpoch(ctx)
	otherMinerAddr := sdk.AccAddress("rotation_test_other_miner")
	k.SetValidatorPubkey(ctx, otherMinerAddr, make([]byte, 32))
	k.AddMiningWork(ctx, epoch, otherMinerAddr, 1)

	updates := k.ComputeValidatorUpdates(ctx, epoch)

	newKeyProto, err := cometencoding.PubKeyToProto(cometed25519.PubKey(newPub))
	require.NoError(t, err)
	oldKeyProto, err := cometencoding.PubKeyToProto(cometed25519.PubKey(oldPub))
	require.NoError(t, err)

	var removalUpdate *abci.ValidatorUpdate
	for i := range updates {
		if updates[i].Power == 0 {
			removalUpdate = &updates[i]
		}
	}
	require.NotNil(t, removalUpdate, "the no-longer-qualified miner must produce a removal update")
	require.Equal(t, newKeyProto, removalUpdate.PubKey, "the removal update is issued for the NEW key, which never held any power")
	require.NotEqual(t, oldKeyProto, removalUpdate.PubKey, "the OLD key -- the one that actually held real CometBFT voting power -- is never targeted by any revocation, and stays live in the validator set indefinitely")
}

// Helper to set up a valid recent-block context for ancestor validation
// tests, so each test doesn't need to repeat this boilerplate.
func setupRecentBlock(k pow.Keeper, ctx sdk.Context, height int64, hash []byte, difficulty int64) sdk.Context {
	ctx = ctx.WithBlockHeight(height).WithHeaderHash(hash)
	k.SetDifficulty(ctx, math.NewInt(difficulty))
	k.RecordRecentBlock(ctx)
	return ctx
}

func TestSubmitPoW_RejectsUnknownAncestorHeight(t *testing.T) {
	k, ctx, _ := setupKeeper(t)
	srv := pow.NewMsgServerImpl(k)

	_, addrStr := validMinerAddr(t)
	msg := &pow.MsgSubmitPoW{
Miner: addrStr,
Submission: &pow.MsgSubmitPoW_Native{
Native: &pow.NativeSubmission{
Height:     999,
Timestamp:  time.Now().Unix(),
PrevHash:   []byte("some-hash"),
MerkleRoot: []byte("merkle"),
Nonce:      1,
Difficulty: 1,
},
},
}

	_, err := srv.SubmitPoW(ctx, msg)
	require.Error(t, err)
	require.True(t, errors.Is(err, types.ErrUnknownAncestor))
}

func TestSubmitPoW_RejectsMismatchedPrevHash(t *testing.T) {
	k, ctx, _ := setupKeeper(t)
	srv := pow.NewMsgServerImpl(k)

	realHash := []byte("the-real-block-hash-at-height-5")
	ctx = setupRecentBlock(k, ctx, 5, realHash, 1)
	ctx = ctx.WithBlockHeight(6) // simulate: we're now processing the NEXT block

	_, addrStr := validMinerAddr(t)
	msg := &pow.MsgSubmitPoW{
Miner: addrStr,
Submission: &pow.MsgSubmitPoW_Native{
Native: &pow.NativeSubmission{
Height:     5,
Timestamp:  time.Now().Unix(),
PrevHash:   []byte("a-completely-different-fake-hash"),
MerkleRoot: []byte("merkle"),
Nonce:      1,
Difficulty: 1,
},
},
}

	_, err := srv.SubmitPoW(ctx, msg)
	require.Error(t, err)
	require.True(t, errors.Is(err, types.ErrUnknownAncestor))
}

func TestSubmitPoW_RejectsStaleAncestorBeyondRecencyWindow(t *testing.T) {
	k, ctx, _ := setupKeeper(t)
	srv := pow.NewMsgServerImpl(k)
	k.SetRecencyWindowK(ctx, 10)

	realHash := []byte("real-hash-at-height-1")
	ctx = setupRecentBlock(k, ctx, 1, realHash, 1)
	ctx = ctx.WithBlockHeight(50) // 49 blocks later -- well outside K=10

	_, addrStr := validMinerAddr(t)
	msg := &pow.MsgSubmitPoW{
Miner: addrStr,
Submission: &pow.MsgSubmitPoW_Native{
Native: &pow.NativeSubmission{
Height:     1,
Timestamp:  time.Now().Unix(),
PrevHash:   realHash,
MerkleRoot: []byte("merkle"),
Nonce:      1,
Difficulty: 1,
},
},
}

	_, err := srv.SubmitPoW(ctx, msg)
	require.Error(t, err)
	require.True(t, errors.Is(err, types.ErrStaleAncestor))
}

func TestSubmitPoW_AcceptsValidAncestorWithinRecencyWindow(t *testing.T) {
	k, ctx, _ := setupKeeper(t)
	srv := pow.NewMsgServerImpl(k)
	k.SetRecencyWindowK(ctx, 10)

	realHash := []byte("real-hash-at-height-40")
	ctx = setupRecentBlock(k, ctx, 40, realHash, 1)
	ctx = ctx.WithBlockHeight(45) // 5 blocks later -- within K=10

	_, addrStr := validMinerAddr(t)
	msg := &pow.MsgSubmitPoW{
Miner: addrStr,
Submission: &pow.MsgSubmitPoW_Native{
Native: &pow.NativeSubmission{
Height:     40,
Timestamp:  time.Now().Unix(),
PrevHash:   realHash,
MerkleRoot: []byte("merkle"),
Nonce:      1,
Difficulty: 1,
},
},
}

	_, err := srv.SubmitPoW(ctx, msg)
	require.NoError(t, err)
}

func TestSubmitPoW_UsesHistoricalDifficultyNotCurrentDifficulty(t *testing.T) {
	k, ctx, _ := setupKeeper(t)
	srv := pow.NewMsgServerImpl(k)
	k.SetRecencyWindowK(ctx, 10)

	realHash := []byte("real-hash-historical-diff-test")
	// Historical difficulty at height 10 was low (1) -- miner solved against this.
	ctx = setupRecentBlock(k, ctx, 10, realHash, 1)

	// Difficulty has since risen sharply, but that must NOT retroactively
	// invalidate work solved against the older, correctly-recorded target.
	ctx = ctx.WithBlockHeight(15)
	k.SetDifficulty(ctx, math.NewInt(999_999_999))

	_, addrStr := validMinerAddr(t)
	msg := &pow.MsgSubmitPoW{
	Miner: addrStr,
	Submission: &pow.MsgSubmitPoW_Native{
		Native: &pow.NativeSubmission{
			Height: 10, Timestamp: time.Now().Unix(),
			PrevHash: realHash, MerkleRoot: []byte("merkle"),
			Nonce: 1, Difficulty: 1, // matches the HISTORICAL difficulty of 1, not current
		},
	},
}

	_, err := srv.SubmitPoW(ctx, msg)
	require.NoError(t, err, "should validate against historical difficulty at the claimed height, not current live difficulty")
}

func TestSubmitPoW_RejectsDuplicateWork(t *testing.T) {
	k, ctx, _ := setupKeeper(t)
	srv := pow.NewMsgServerImpl(k)
	k.SetRecencyWindowK(ctx, 10)

	realHash := []byte("real-hash-duplicate-test")
	ctx = setupRecentBlock(k, ctx, 20, realHash, 1)
	ctx = ctx.WithBlockHeight(22)

	_, addrStr := validMinerAddr(t)
	msg := &pow.MsgSubmitPoW{
	Miner: addrStr,
	Submission: &pow.MsgSubmitPoW_Native{
		Native: &pow.NativeSubmission{
			Height: 20, Timestamp: 12345, // fixed timestamp so header hash is deterministic
			PrevHash: realHash, MerkleRoot: []byte("merkle"),
			Nonce: 1, Difficulty: 1,
		},
	},
}

		_, err := srv.SubmitPoW(ctx, msg)
	require.NoError(t, err, "first submission of this exact header should succeed")

	// Advance to a genuinely new real block height before resubmitting,
	// so this test isolates duplicate-work detection specifically,
	// independent of the separate, later-added per-block-height
	// submission limit -- otherwise both protections would trigger on
	// the same resubmission and this test couldn't tell them apart.
	ctx = setupRecentBlock(k, ctx, 21, []byte("real-hash-duplicate-test-2"), 1)
	ctx = ctx.WithBlockHeight(23)

	// Re-submit the EXACT same header (same miner, claimed height,
	// prevHash, nonce, timestamp, difficulty) -- this must still be
	// rejected as duplicate work, even from a genuinely new real block,
	// since nothing about the header itself changed.
	_, err = srv.SubmitPoW(ctx, msg)
	require.Error(t, err)
	require.True(t, errors.Is(err, types.ErrDuplicateWork))
}

// The submission-cap tests below run at heights past
// pow.SubmissionCapActivationHeight -- the cap is gated on that real
// height (see its doc comment), so exercising it below that height
// would silently no-op the very behavior these tests check.
func TestSubmitPoW_RejectsSecondSubmissionAtSameHeight(t *testing.T) {
	k, ctx, _ := setupKeeper(t)

	ancestorHeight := pow.SubmissionCapActivationHeight + 1
	submitHeight := pow.SubmissionCapActivationHeight + 2

	realHash := []byte("real-hash-for-same-height-test")
	ctx = setupRecentBlock(k, ctx, ancestorHeight, realHash, 1)
	ctx = ctx.WithBlockHeight(submitHeight)
	k.SetBlockReward(ctx, math.NewInt(5_000_000))

	_, addrStr := validMinerAddr(t)
	srv := pow.NewMsgServerImpl(k)

	firstMsg := &pow.MsgSubmitPoW{
		Miner: addrStr,
		Submission: &pow.MsgSubmitPoW_Native{
			Native: &pow.NativeSubmission{
				Height: uint64(ancestorHeight), Timestamp: time.Now().Unix(), PrevHash: realHash,
				MerkleRoot: []byte("merkle"), Nonce: 1, Difficulty: 1,
			},
		},
	}
	_, err := srv.SubmitPoW(ctx, firstMsg)
	require.NoError(t, err, "the first submission at this height must succeed")

	secondMsg := &pow.MsgSubmitPoW{
		Miner: addrStr,
		Submission: &pow.MsgSubmitPoW_Native{
			Native: &pow.NativeSubmission{
				Height: uint64(ancestorHeight), Timestamp: time.Now().Unix(), PrevHash: realHash,
				MerkleRoot: []byte("merkle"), Nonce: 2, Difficulty: 1, // a different nonce -- a genuinely distinct submission, not a duplicate-work rejection
			},
		},
	}
	_, err = srv.SubmitPoW(ctx, secondMsg)
	require.Error(t, err, "a second submission at the same height must be rejected")
	require.Contains(t, err.Error(), "already been accepted at height")
}

func TestSubmitPoW_AllowsSubmissionAtNextHeight(t *testing.T) {
	k, ctx, _ := setupKeeper(t)

	ancestorHeight := pow.SubmissionCapActivationHeight + 1
	submitHeight := pow.SubmissionCapActivationHeight + 2

	realHash := []byte("real-hash-for-next-height-test")
	ctx = setupRecentBlock(k, ctx, ancestorHeight, realHash, 1)
	ctx = ctx.WithBlockHeight(submitHeight)
	k.SetBlockReward(ctx, math.NewInt(5_000_000))

	_, addrStr := validMinerAddr(t)
	srv := pow.NewMsgServerImpl(k)

	firstMsg := &pow.MsgSubmitPoW{
		Miner: addrStr,
		Submission: &pow.MsgSubmitPoW_Native{
			Native: &pow.NativeSubmission{
				Height: uint64(ancestorHeight), Timestamp: time.Now().Unix(), PrevHash: realHash,
				MerkleRoot: []byte("merkle"), Nonce: 1, Difficulty: 1,
			},
		},
	}
	_, err := srv.SubmitPoW(ctx, firstMsg)
	require.NoError(t, err)

	// Move to the next real block height and its own real ancestor.
	nextAncestorHeight := ancestorHeight + 1
	nextSubmitHeight := submitHeight + 1
	nextHash := []byte("real-hash-for-next-height-test-2")
	ctx = setupRecentBlock(k, ctx, nextAncestorHeight, nextHash, 1)
	ctx = ctx.WithBlockHeight(nextSubmitHeight)

	secondMsg := &pow.MsgSubmitPoW{
		Miner: addrStr,
		Submission: &pow.MsgSubmitPoW_Native{
			Native: &pow.NativeSubmission{
				Height: uint64(nextAncestorHeight), Timestamp: time.Now().Unix(), PrevHash: nextHash,
				MerkleRoot: []byte("merkle"), Nonce: 1, Difficulty: 1,
			},
		},
	}
	_, err = srv.SubmitPoW(ctx, secondMsg)
	require.NoError(t, err, "a submission at a genuinely new block height must succeed")
}

func TestSubmitPoW_FailedSubmission_DoesNotConsumeThisHeightsSlot(t *testing.T) {
	k, ctx, _ := setupKeeper(t)

	ancestorHeight := pow.SubmissionCapActivationHeight + 1
	submitHeight := pow.SubmissionCapActivationHeight + 2

	realHash := []byte("real-hash-for-failed-then-valid-test")
	ctx = setupRecentBlock(k, ctx, ancestorHeight, realHash, 1)
	ctx = ctx.WithBlockHeight(submitHeight)
	k.SetBlockReward(ctx, math.NewInt(5_000_000))

	_, addrStr := validMinerAddr(t)
	srv := pow.NewMsgServerImpl(k)

	// A genuinely invalid submission (wrong PrevHash) -- must fail
	// verification, not consume the height's slot.
	badMsg := &pow.MsgSubmitPoW{
		Miner: addrStr,
		Submission: &pow.MsgSubmitPoW_Native{
			Native: &pow.NativeSubmission{
				Height: uint64(ancestorHeight), Timestamp: time.Now().Unix(), PrevHash: []byte("wrong-hash-entirely"),
				MerkleRoot: []byte("merkle"), Nonce: 1, Difficulty: 1,
			},
		},
	}
	_, err := srv.SubmitPoW(ctx, badMsg)
	require.Error(t, err, "a genuinely invalid submission must fail")

	goodMsg := &pow.MsgSubmitPoW{
		Miner: addrStr,
		Submission: &pow.MsgSubmitPoW_Native{
			Native: &pow.NativeSubmission{
				Height: uint64(ancestorHeight), Timestamp: time.Now().Unix(), PrevHash: realHash,
				MerkleRoot: []byte("merkle"), Nonce: 1, Difficulty: 1,
			},
		},
	}
	_, err = srv.SubmitPoW(ctx, goodMsg)
	require.NoError(t, err, "a genuinely valid submission at the same height must still succeed, since the earlier failed attempt shouldn't have consumed the slot")
}

// TestSubmitPoW_SubmissionCap_NoOpBeforeActivationHeight is the
// regression test for the real bug this activation gate fixes: before
// SubmissionCapActivationHeight, a second submission at the same
// height must still succeed (matching the seed's real pre-deploy
// history), and the tracking write must never happen -- otherwise a
// fresh node pays gas for a Get/Set the seed's original execution
// never performed, which is exactly what caused the real height-40914
// LastResultsHash divergence.
func TestSubmitPoW_SubmissionCap_NoOpBeforeActivationHeight(t *testing.T) {
	k, ctx, _ := setupKeeper(t)

	preActivationHeight := pow.SubmissionCapActivationHeight - 100
	ancestorHeight := preActivationHeight - 1

	realHash := []byte("real-hash-pre-activation-test")
	ctx = setupRecentBlock(k, ctx, ancestorHeight, realHash, 1)
	ctx = ctx.WithBlockHeight(preActivationHeight)
	k.SetBlockReward(ctx, math.NewInt(5_000_000))

	_, addrStr := validMinerAddr(t)
	srv := pow.NewMsgServerImpl(k)

	firstMsg := &pow.MsgSubmitPoW{
		Miner: addrStr,
		Submission: &pow.MsgSubmitPoW_Native{
			Native: &pow.NativeSubmission{
				Height: uint64(ancestorHeight), Timestamp: time.Now().Unix(), PrevHash: realHash,
				MerkleRoot: []byte("merkle"), Nonce: 1, Difficulty: 1,
			},
		},
	}
	_, err := srv.SubmitPoW(ctx, firstMsg)
	require.NoError(t, err)

	secondMsg := &pow.MsgSubmitPoW{
		Miner: addrStr,
		Submission: &pow.MsgSubmitPoW_Native{
			Native: &pow.NativeSubmission{
				Height: uint64(ancestorHeight), Timestamp: time.Now().Unix(), PrevHash: realHash,
				MerkleRoot: []byte("merkle"), Nonce: 2, Difficulty: 1,
			},
		},
	}
	_, err = srv.SubmitPoW(ctx, secondMsg)
	require.NoError(t, err, "before the real activation height, a second submission at the same height must still succeed -- matching the seed's actual pre-deploy history")

	_, ok := k.GetLastAcceptedSubmissionHeight(ctx)
	require.False(t, ok, "before the real activation height, the tracking key must never be written -- a fresh replay must not pay gas the seed's original execution never paid")
}