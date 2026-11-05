package gpu

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/whoyoujoshin/aether/miner"
)

// hashers is a small-batch hasher for every OpenCL device here; the test
// skips where there is none (most CI machines, unless pocl is installed).
func hashers(t *testing.T, batch int) []*Hasher {
	t.Helper()
	devs, err := Devices()
	if err != nil || len(devs) == 0 {
		if os.Getenv("AETHER_GPU_TESTS") == "required" {
			t.Fatalf("AETHER_GPU_TESTS=required but no OpenCL device (%v)", err)
		}
		t.Skipf("no OpenCL device (%v)", err)
	}
	var hs []*Hasher
	for _, d := range devs {
		h, err := NewHasher(d, batch)
		require.NoError(t, err, d.String())
		t.Cleanup(h.Close)
		hs = append(hs, h)
	}
	return hs
}

func withNonce(header []byte, nonce uint64) []byte {
	b := bytes.Clone(header)
	binary.LittleEndian.PutUint64(b[80:], nonce)
	return b
}

// Every hash the kernel computes is the one x/pow computes (package
// miner's Hash, the same scrypt the keeper checks).
func TestHashesMatchGoScrypt(t *testing.T) {
	for _, h := range hashers(t, 64) {
		header := make([]byte, HeaderSize)
		_, _ = rand.Read(header)
		base := uint64(0xfffffffffffffff0) // the nonce's high word changes mid-batch
		got, err := h.Hashes(header, base, 40)
		require.NoError(t, err)
		for i, g := range got {
			want := miner.Hash(withNonce(header, base+uint64(i)))
			require.Equal(t, want, g[:], "%s, nonce %d", h.Device.Name, base+uint64(i))
		}
	}
}

// A real mining header, built as package miner builds it.
func TestHashesMatchAMiningHeader(t *testing.T) {
	hdr := miner.Work{Height: 609151, Hash: bytes.Repeat([]byte{0xab}, 32), Difficulty: 355000, RecencyWindow: 20}.
		Header(bytes.Repeat([]byte{0x33}, 32), 1762300000)
	b := hdr.Bytes()
	require.Len(t, b, HeaderSize)
	for _, h := range hashers(t, 64) {
		got, err := h.Hashes(b, 7, 3)
		require.NoError(t, err)
		for i := range got {
			hdr.Nonce = 7 + uint64(i)
			require.Equal(t, miner.Hash(hdr.Bytes()), got[i][:])
		}
	}
}

// Search finds a nonce that really meets the target, and none when the
// target is impossible.
func TestSearch(t *testing.T) {
	for _, h := range hashers(t, 64) {
		header := make([]byte, HeaderSize)
		_, _ = rand.Read(header)
		const difficulty = 4 // about one hash in four meets it
		nonce, ok, err := h.Search(header, 1000, miner.Target(difficulty))
		require.NoError(t, err)
		require.True(t, ok, "64 tries at difficulty 4")
		require.GreaterOrEqual(t, nonce, uint64(1000))
		require.Less(t, nonce, uint64(1000+h.Batch))
		require.True(t, miner.Meets(miner.Hash(withNonce(header, nonce)), difficulty))

		_, ok, err = h.Search(header, 1000, make([]byte, 32))
		require.NoError(t, err)
		require.False(t, ok, "nothing is below a zero target")
	}
}
