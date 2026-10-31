package miner

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func testHeader() Header {
	prev := make([]byte, 32)
	for i := range prev {
		prev[i] = byte(i)
	}
	miner := make([]byte, 32)
	for i := range miner {
		miner[i] = byte(0xa0 + i)
	}
	return Header{Height: 534_771, Timestamp: 1_793_000_000, PrevHash: prev, MerkleRoot: MerkleRoot, Nonce: 7, Difficulty: 355_000, Miner: miner}
}

func TestHeaderBytesLayout(t *testing.T) {
	h := testHeader()
	b := h.Bytes()
	require.Len(t, b, 8+8+32+32+8+8+32)
	require.Equal(t, []byte{0xf3, 0x28, 0x08, 0, 0, 0, 0, 0}, b[:8], "height, little-endian")
	require.Equal(t, h.PrevHash, b[16:48])
	require.Equal(t, MerkleRoot, b[48:80])
	require.Equal(t, byte(7), b[80], "nonce")
	require.Equal(t, h.Miner, b[96:])
}

func TestMeetsAndTarget(t *testing.T) {
	require.False(t, Meets(make([]byte, 32), 0))
	require.True(t, Meets(make([]byte, 32), 1<<40), "a zero hash meets any difficulty")
	all := make([]byte, 32)
	for i := range all {
		all[i] = 0xff
	}
	require.False(t, Meets(all, 1), "2^256-1 never meets: the target is below it")
	require.Equal(t, byte(0x7f), Target(2)[0])
	require.Equal(t, byte(0x00), Target(256)[0])
	require.Equal(t, byte(0xff), Target(256)[1])
}

// Search finds a nonce whose hash meets the difficulty, and searchers with
// different starts on one step cover disjoint nonces.
func TestSearch(t *testing.T) {
	h := testHeader()
	h.Difficulty = 16
	var hashes atomic.Uint64
	found, ok := Search(context.Background(), h, 1, 2, 10_000, &hashes)
	require.True(t, ok)
	require.Equal(t, uint64(1), found.Nonce%2, "odd nonces only")
	require.True(t, Meets(Hash(found.Bytes()), 16))
	require.Equal(t, hashes.Load(), (found.Nonce-1)/2+1)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	h.Difficulty = 1 << 60
	_, ok = Search(ctx, h, 0, 1, 1<<30, &hashes)
	require.False(t, ok, "a cancelled search stops")
}

func TestWorkJSON(t *testing.T) {
	w := Work{ChainID: "aether-testnet-1", Height: 534_771, Hash: testHeader().PrevHash, Difficulty: 1<<60 + 1, RecencyWindow: 60}
	b, err := json.Marshal(w)
	require.NoError(t, err)
	require.Contains(t, string(b), `"difficulty":"1152921504606846977"`, "as a string: JavaScript loses it as a number")
	require.Contains(t, string(b), `"hash":"000102`)
	var back Work
	require.NoError(t, json.Unmarshal(b, &back))
	require.Equal(t, w, back)
	require.Error(t, json.Unmarshal([]byte(`{"height":5,"hash":"00","difficulty":"9","recencyWindow":60}`), &back))
	require.Error(t, json.Unmarshal([]byte(`{"height":5,"hash":"`+hexOf(w.Hash)+`","difficulty":"0","recencyWindow":60}`), &back))
}

func hexOf(b []byte) string {
	const digits = "0123456789abcdef"
	out := make([]byte, 0, 2*len(b))
	for _, c := range b {
		out = append(out, digits[c>>4], digits[c&15])
	}
	return string(out)
}

func TestStale(t *testing.T) {
	w := Work{Height: 100, Difficulty: 50, RecencyWindow: 60}
	require.False(t, w.Stale(Work{Height: 129, Difficulty: 50, RecencyWindow: 60}))
	require.True(t, w.Stale(Work{Height: 130, Difficulty: 50, RecencyWindow: 60}), "half the window")
	require.True(t, w.Stale(Work{Height: 101, Difficulty: 49, RecencyWindow: 60}), "easier")
	require.False(t, w.Stale(Work{Height: 101, Difficulty: 51, RecencyWindow: 60}))
}

// However many ask within TTL, the node is read once; a short outage
// keeps the last good work; a long one reports the error.
func TestCache(t *testing.T) {
	var reads atomic.Int32
	fail := false
	c := &Cache{TTL: 20 * time.Millisecond, Read: func(context.Context) (Work, error) {
		reads.Add(1)
		if fail {
			return Work{}, errors.New("node down")
		}
		return Work{Height: int64(reads.Load())}, nil
	}}
	for i := 0; i < 50; i++ {
		w, err := c.Get(context.Background())
		require.NoError(t, err)
		require.Equal(t, int64(1), w.Height)
	}
	require.Equal(t, int32(1), reads.Load())

	time.Sleep(25 * time.Millisecond)
	fail = true
	w, err := c.Get(context.Background())
	require.NoError(t, err, "a short outage serves the last good work")
	require.Equal(t, int64(1), w.Height)

	time.Sleep(11 * c.TTL)
	_, err = c.Get(context.Background())
	require.ErrorContains(t, err, "node down")
	_, err = c.Get(context.Background())
	require.ErrorContains(t, err, "node down", "the error is cached too")
	require.Equal(t, int32(3), reads.Load())
}
