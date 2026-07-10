package pow_test

import (
	"errors"
	"testing"
	"time"

	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	"github.com/whoyoujoshin/aether/x/pow"
	"github.com/whoyoujoshin/aether/x/pow/types"
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