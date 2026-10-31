package pow

import (
	"bytes"
	"sync/atomic"
	"testing"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	"github.com/whoyoujoshin/aether/miner"
)

// package miner, which the browser, desktop and CLI miners follow, hashes
// and judges headers exactly as this keeper checks them.
func TestMinerPackageMatchesKeeper(t *testing.T) {
	addr := sdk.AccAddress(bytes.Repeat([]byte{0xab}, 32))
	prev := bytes.Repeat([]byte{0x11}, 32)
	for _, d := range []uint64{1, 2, 16, 255, 256, 1_000_003} {
		h := miner.Header{Height: 42, Timestamp: 1_700_000_123, PrevHash: prev, MerkleRoot: miner.MerkleRoot, Nonce: 9, Difficulty: d, Miner: addr}
		mh := MiningHeader{Height: 42, Timestamp: 1_700_000_123, PrevHash: prev, MerkleRoot: miner.MerkleRoot, Nonce: 9, Difficulty: d, MinerAddress: addr}
		require.Equal(t, headerToBytes(mh), h.Bytes())
		hash := miner.Hash(h.Bytes())
		require.Equal(t, meetsdifficulty(hash, d), miner.Meets(hash, d), "difficulty %d", d)
	}

	k, ctx := newBeaconTestKeeper(t)
	h := miner.Header{Height: 42, Timestamp: 1_700_000_123, PrevHash: prev, MerkleRoot: miner.MerkleRoot, Difficulty: 64, Miner: addr}
	var n atomic.Uint64
	found, ok := miner.Search(ctx, h, 0, 1, 100_000, &n)
	require.True(t, ok)
	mh := MiningHeader{Height: 42, Timestamp: 1_700_000_123, PrevHash: prev, MerkleRoot: miner.MerkleRoot, Nonce: found.Nonce, Difficulty: 64, MinerAddress: addr}
	require.True(t, k.VerifyMiningHeader(ctx, mh), "the keeper accepts what Search finds")
	mh.Nonce++
	require.Equal(t, miner.Meets(miner.Hash(headerToBytes(mh)), 64), k.VerifyMiningHeader(ctx, mh))
}
