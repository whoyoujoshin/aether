package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"math/big"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/scrypt"
)

func dsha(b []byte) []byte {
	a := sha256.Sum256(b)
	c := sha256.Sum256(a[:])
	return c[:]
}

func rev(b []byte) []byte {
	out := make([]byte, len(b))
	for i := range b {
		out[len(b)-1-i] = b[i]
	}
	return out
}

// display is how block explorers and libdohj print a hash: reversed.
func display(b []byte) string { return hex.EncodeToString(rev(b)) }

func merkleRoot(leaf []byte, branch [][]byte, index uint32) []byte {
	h := leaf
	for _, s := range branch {
		if index&1 == 1 {
			h = dsha(append(append([]byte{}, s...), h...))
		} else {
			h = dsha(append(append([]byte{}, h...), s...))
		}
		index >>= 1
	}
	return h
}

// compactTarget expands a header's nBits.
func compactTarget(bits uint32) *big.Int {
	exp := bits >> 24
	mant := big.NewInt(int64(bits & 0x007fffff))
	if exp <= 3 {
		return mant.Rsh(mant, uint(8*(3-exp)))
	}
	return mant.Lsh(mant, uint(8*(exp-3)))
}

func loadBlock(t *testing.T, height string) []byte {
	t.Helper()
	h, err := os.ReadFile("testdata/dogecoin_block" + height + ".hex")
	require.NoError(t, err)
	b, err := hex.DecodeString(strings.TrimSpace(string(h)))
	require.NoError(t, err)
	return b
}

// Real Dogecoin blocks, each merged-mined with a Litecoin parent: the
// parser reads the proof exactly as Dogecoin Core does, so every check a
// chain makes on it passes with the expected values from libdohj's tests.
func TestParseAuxPow_RealDogecoinBlocks(t *testing.T) {
	const dogecoinChainID = 98
	for _, c := range []struct {
		height, blockHash, coinbaseTxid, parentHash string
		chainBranch, coinbaseBranch                 []string
		txs                                         int
	}{
		{
			height:       "371337",
			blockHash:    "60323982f9c5ff1b5a954eac9dc1269352835f47c2c5222691d80f0d50dcf053",
			coinbaseTxid: "e5422732b20e9e7ecc243427abbe296e9528d308bb111aae8d30c3465e442de8",
			parentHash:   "45df41e40aba5b2a03d08bd1202a1c02ef3954d8aa22ea6c5ae62fd00f290ea9",
			chainBranch: []string{
				"b541c848bc001d07d2bdf8643abab61d2c6ae50d5b2495815339a4b30703a46f",
				"78d6abe48cee514cf3496f4042039acb7e27616dcfc5de926ff0d6c7e5987be7",
				"a0469413ce64d67c43902d54ee3a380eff12ded22ca11cbd3842e15d48298103",
			},
			coinbaseBranch: []string{
				"cd3947cd5a0c26fde01b05a3aa3d7a38717be6ae11d27239365024db36a679a9",
				"48f9e8fef3411944e27f49ec804462c9e124dca0954c71c8560e8a9dd218a452",
				"d11293660392e7c51f69477a6130237c72ecee2d0c1d3dc815841734c370331a",
			},
			txs: 6,
		},
		{height: "748634", blockHash: "bd98a06391115285265c04984e8505229739f6ffa5d498929a91fbe7c281ea7b"},
		{height: "894863", blockHash: "93a207e6d227f4d60ee64fad584b47255f654b0b6378d78e774123dd66f4fef9", coinbaseTxid: "c84431cf41f592373cc70db07f6804f945202f5f7baad31a8bbab89aaecb7b8b"},
	} {
		t.Run(c.height, func(t *testing.T) {
			block := loadBlock(t, c.height)
			header := block[:80]
			require.Equal(t, c.blockHash, display(dsha(header)))

			r := &reader{b: block, off: 80}
			a, err := r.auxPow()
			require.NoError(t, err)

			// The proof's bytes are followed by exactly the block's transactions.
			n, err := r.varint(60)
			require.NoError(t, err)
			for i := 0; i < n; i++ {
				_, err := r.tx()
				require.NoError(t, err, "tx %d", i)
			}
			require.Equal(t, len(block), r.off)
			if c.txs != 0 {
				require.Equal(t, c.txs, n)
			}

			if c.coinbaseTxid != "" {
				require.Equal(t, c.coinbaseTxid, display(dsha(a.CoinbaseTx)))
			}
			if c.parentHash != "" {
				require.Equal(t, c.parentHash, display(dsha(a.ParentHeader)))
			}
			for i, want := range c.chainBranch {
				require.Equal(t, want, display(a.ChainBranch[i]))
			}
			for i, want := range c.coinbaseBranch {
				require.Equal(t, want, display(a.CoinbaseBranch[i]))
			}

			// The coinbase is the parent's first transaction and is in its
			// merkle root.
			require.Zero(t, a.CoinbaseIndex)
			require.Equal(t, a.ParentHeader[36:68], merkleRoot(dsha(a.CoinbaseTx), a.CoinbaseBranch, a.CoinbaseIndex))

			// The coinbase commits to the Dogecoin block through the chain
			// branch, at the slot the commitment's nonce picks.
			script := coinbaseScript(t, a.CoinbaseTx)
			at := bytes.Index(script, []byte{0xfa, 0xbe, 0x6d, 0x6d})
			require.GreaterOrEqual(t, at, 0)
			committed := rev(script[at+4 : at+36])
			require.Equal(t, committed, merkleRoot(dsha(header), a.ChainBranch, a.ChainIndex))
			size := binary.LittleEndian.Uint32(script[at+36:])
			nonce := binary.LittleEndian.Uint32(script[at+40:])
			require.Equal(t, uint32(1)<<len(a.ChainBranch), size)
			require.Equal(t, expectedIndex(nonce, dogecoinChainID, uint32(len(a.ChainBranch))), a.ChainIndex)

			// The Litecoin parent's scrypt hash, read little-endian as
			// Litecoin reads it, meets the Dogecoin block's target.
			pow, err := scrypt.Key(a.ParentHeader, a.ParentHeader, 1024, 1, 1, 32)
			require.NoError(t, err)
			target := compactTarget(binary.LittleEndian.Uint32(header[72:76]))
			require.Negative(t, new(big.Int).SetBytes(rev(pow)).Cmp(target))

			// And the bridge's conversion carries the same nonce x/pow checks.
			d, err := a.auxPowData(&template{Hash: dsha(header), Height: 1, Reward: make([]byte, 20)})
			require.NoError(t, err)
			require.Equal(t, nonce, d.ChainNonce)

			// A proof re-serialized the way pools send it parses back the same.
			again, err := parseAuxPow(serializeAuxPow(a))
			require.NoError(t, err)
			require.Equal(t, a, again)
		})
	}
}

func expectedIndex(nonce, chainID, h uint32) uint32 {
	r := nonce*1103515245 + 12345
	r += chainID
	r = r*1103515245 + 12345
	return r % (1 << h)
}

func coinbaseScript(t *testing.T, tx []byte) []byte {
	t.Helper()
	r := &reader{b: tx, off: 4}
	_, err := r.varint(41)
	require.NoError(t, err)
	_, err = r.bytes(36)
	require.NoError(t, err)
	n, err := r.varint(1)
	require.NoError(t, err)
	s, err := r.bytes(n)
	require.NoError(t, err)
	return s
}

// A segwit coinbase is read back in its legacy serialization, the bytes
// its txid (and so the parent's merkle root) commits to.
func TestParseAuxPow_SegwitCoinbaseDropsTheWitness(t *testing.T) {
	legacy := testCoinbase([]byte("prefix"), bytes.Repeat([]byte{0x42}, 32), 7)
	witness := append([]byte{}, legacy[:4]...)
	witness = append(witness, 0x00, 0x01)
	witness = append(witness, legacy[4:len(legacy)-4]...)
	witness = append(witness, 0x01, 0x20) // one witness item, 32 bytes: the coinbase's reserved value
	witness = append(witness, make([]byte, 32)...)
	witness = append(witness, legacy[len(legacy)-4:]...)

	a := &cAuxPow{CoinbaseTx: witness, ParentHeader: make([]byte, 80)}
	parsed, err := parseAuxPow(serializeAuxPow(a))
	require.NoError(t, err)
	require.Equal(t, legacy, parsed.CoinbaseTx)
}

func TestParseAuxPow_RefusesMalformedInput(t *testing.T) {
	good := serializeAuxPow(&cAuxPow{
		CoinbaseTx:     testCoinbase([]byte("p"), bytes.Repeat([]byte{0x42}, 32), 0),
		CoinbaseBranch: [][]byte{bytes.Repeat([]byte{1}, 32)},
		ParentHeader:   make([]byte, 80),
	})
	_, err := parseAuxPow(good)
	require.NoError(t, err)

	_, err = parseAuxPow(append(append([]byte{}, good...), 0x00))
	require.ErrorContains(t, err, "after the parent header")
	for cut := 1; cut < len(good); cut += 7 {
		_, err = parseAuxPow(good[:cut])
		require.Error(t, err, "truncated at %d", cut)
	}

	// A branch count far beyond the data is refused before allocating.
	huge := serializeAuxPow(&cAuxPow{CoinbaseTx: testCoinbase(nil, make([]byte, 32), 0), ParentHeader: make([]byte, 80)})
	at := len(testCoinbase(nil, make([]byte, 32), 0)) + 32
	huge = append(append(append([]byte{}, huge[:at]...), 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x7f), huge[at+1:]...)
	_, err = parseAuxPow(huge)
	require.ErrorContains(t, err, "exceeds the data left")
}

// testCoinbase is a one-input, no-output coinbase whose scriptSig carries a
// merge-mining commitment to root (internal byte order) with nonce.
func testCoinbase(prefix, root []byte, nonce uint32) []byte {
	script := append(append([]byte{}, prefix...), 0xfa, 0xbe, 0x6d, 0x6d)
	script = append(script, rev(root)...)
	script = binary.LittleEndian.AppendUint32(script, 1)
	script = binary.LittleEndian.AppendUint32(script, nonce)
	tx := []byte{1, 0, 0, 0, 1}
	tx = append(tx, make([]byte, 32)...)
	tx = append(tx, 0xff, 0xff, 0xff, 0xff)
	tx = appendVarint(tx, uint64(len(script)))
	tx = append(tx, script...)
	tx = append(tx, 0xff, 0xff, 0xff, 0xff, 0, 0, 0, 0, 0)
	return tx
}

// serializeAuxPow is parseAuxPow's inverse, for pool-side tooling and
// tests: the bytes a pool sends in submitauxblock.
func serializeAuxPow(a *cAuxPow) []byte {
	var out []byte
	u32 := func(v uint32) {
		var b [4]byte
		binary.LittleEndian.PutUint32(b[:], v)
		out = append(out, b[:]...)
	}
	branch := func(hs [][]byte) {
		out = appendVarint(out, uint64(len(hs)))
		for _, h := range hs {
			out = append(out, h...)
		}
	}
	out = append(out, a.CoinbaseTx...)
	out = append(out, make([]byte, 32)...)
	branch(a.CoinbaseBranch)
	u32(a.CoinbaseIndex)
	branch(a.ChainBranch)
	u32(a.ChainIndex)
	return append(out, a.ParentHeader...)
}

func appendVarint(out []byte, n uint64) []byte {
	switch {
	case n < 0xfd:
		return append(out, byte(n))
	case n <= 0xffff:
		return append(out, 0xfd, byte(n), byte(n>>8))
	case n <= 0xffffffff:
		var b [4]byte
		binary.LittleEndian.PutUint32(b[:], uint32(n))
		return append(append(out, 0xfe), b[:]...)
	default:
		var b [8]byte
		binary.LittleEndian.PutUint64(b[:], n)
		return append(append(out, 0xff), b[:]...)
	}
}
