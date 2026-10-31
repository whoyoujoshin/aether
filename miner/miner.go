// Package miner is native Aether mining outside the chain: the header a
// miner hashes, the work it hashes against, and a search loop. x/pow's
// keeper checks submissions with the same rules (headerToBytes,
// meetsdifficulty); a test in x/pow holds the two together.
//
// A header is mined against a recent block: its height, its hash and the
// difficulty recorded at it. The chain accepts the header while that block
// is at most RecencyWindow blocks old, takes one native submission per
// block, and refuses a header it already accepted. It doesn't check the
// timestamp or merkle root.
package miner

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"strconv"
	"sync/atomic"

	"golang.org/x/crypto/scrypt"
)

// Litecoin's scrypt parameters, as x/pow uses them: scrypt(header,
// header, N, r, p, 32).
const (
	ScryptN = 1024
	ScryptR = 1
	ScryptP = 1
)

// MerkleRoot is the 32 bytes every Aether miner puts in a header. The
// chain doesn't read it.
var MerkleRoot = []byte("merkleplaceholder000000000000000")

// Header is what a miner hashes and submits (x/pow's MiningHeader).
type Header struct {
	Height     uint64
	Timestamp  int64
	PrevHash   []byte
	MerkleRoot []byte
	Nonce      uint64
	Difficulty uint64
	Miner      []byte // the miner's account address bytes: whoever submits it
}

// Bytes is the header as x/pow hashes it: height, timestamp, prev hash,
// merkle root, nonce, difficulty and the miner's address, integers as
// 8-byte little-endian.
func (h Header) Bytes() []byte {
	buf := make([]byte, 0, 32+len(h.PrevHash)+len(h.MerkleRoot)+len(h.Miner))
	putU64 := func(v uint64) {
		for i := 0; i < 8; i++ {
			buf = append(buf, byte(v>>(8*i)))
		}
	}
	putU64(h.Height)
	putU64(uint64(h.Timestamp))
	buf = append(buf, h.PrevHash...)
	buf = append(buf, h.MerkleRoot...)
	putU64(h.Nonce)
	putU64(h.Difficulty)
	return append(buf, h.Miner...)
}

// ID is the hash x/pow keeps of each accepted header, to refuse it again.
func (h Header) ID() [32]byte { return sha256.Sum256(h.Bytes()) }

// Hash is the header's scrypt hash.
func Hash(header []byte) []byte {
	out, err := scrypt.Key(header, header, ScryptN, ScryptR, ScryptP, 32)
	if err != nil {
		panic(err) // only for invalid parameters, and these are constants
	}
	return out
}

var maxTarget = new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 256), big.NewInt(1))

// Target is the value a hash must stay below: (2^256-1)/difficulty, as 32
// big-endian bytes. A hash meets it about once in difficulty tries.
func Target(difficulty uint64) []byte {
	if difficulty == 0 {
		return make([]byte, 32) // nothing meets it
	}
	t := new(big.Int).Div(maxTarget, new(big.Int).SetUint64(difficulty))
	return t.FillBytes(make([]byte, 32))
}

// Meets says whether hash, read big-endian, is below difficulty's target.
func Meets(hash []byte, difficulty uint64) bool {
	if difficulty == 0 {
		return false
	}
	return new(big.Int).SetBytes(hash).Cmp(new(big.Int).SetBytes(Target(difficulty))) < 0
}

// Work is the block a header is mined against, and how long the chain
// takes headers on it.
type Work struct {
	ChainID       string
	Height        int64
	Hash          []byte // the block's hash, PrevHash in the header
	Difficulty    uint64 // recorded at Height: the least a header there may claim
	RecencyWindow int64
}

// workJSON is Work on the wire (the explorer's /api/mine/work): the hash
// in hex, and the difficulty as a decimal string, since JavaScript
// numbers lose integers past 2^53.
type workJSON struct {
	ChainID       string `json:"chainId"`
	Height        int64  `json:"height"`
	Hash          string `json:"hash"`
	Difficulty    string `json:"difficulty"`
	RecencyWindow int64  `json:"recencyWindow"`
	Target        string `json:"target"` // hex; what a hash must stay below
}

func (w Work) MarshalJSON() ([]byte, error) {
	return json.Marshal(workJSON{
		ChainID: w.ChainID, Height: w.Height, Hash: hex.EncodeToString(w.Hash),
		Difficulty: strconv.FormatUint(w.Difficulty, 10), RecencyWindow: w.RecencyWindow,
		Target: hex.EncodeToString(Target(w.Difficulty)),
	})
}

func (w *Work) UnmarshalJSON(b []byte) error {
	var j workJSON
	if err := json.Unmarshal(b, &j); err != nil {
		return err
	}
	hash, err := hex.DecodeString(j.Hash)
	if err != nil || len(hash) != 32 {
		return fmt.Errorf("work hash %q isn't 32 bytes of hex", j.Hash)
	}
	d, err := strconv.ParseUint(j.Difficulty, 10, 64)
	if err != nil || d == 0 {
		return fmt.Errorf("work difficulty %q isn't a positive integer", j.Difficulty)
	}
	if j.Height <= 0 || j.RecencyWindow <= 0 {
		return fmt.Errorf("work height %d or recency window %d isn't positive", j.Height, j.RecencyWindow)
	}
	*w = Work{ChainID: j.ChainID, Height: j.Height, Hash: hash, Difficulty: d, RecencyWindow: j.RecencyWindow}
	return nil
}

// Header is a fresh header on w for miner, nonce 0.
func (w Work) Header(miner []byte, timestamp int64) Header {
	return Header{
		Height:     uint64(w.Height),
		Timestamp:  timestamp,
		PrevHash:   w.Hash,
		MerkleRoot: MerkleRoot,
		Difficulty: w.Difficulty,
		Miner:      miner,
	}
}

// Stale says whether a header on w should be abandoned now that the tip
// is next: past half the recency window (so a find still has time to
// land), or the difficulty fell (a fresh header is easier).
func (w Work) Stale(next Work) bool {
	return next.Height-w.Height >= next.RecencyWindow/2 || next.Difficulty < w.Difficulty
}

// Search tries h's nonces start, start+step, start+2*step, ... at most
// tries of them, until one meets h.Difficulty or ctx ends. It counts each
// hash into hashes. With n searchers on one header, give each its own
// start in 0..n-1 and step n.
func Search(ctx context.Context, h Header, start, step, tries uint64, hashes *atomic.Uint64) (Header, bool) {
	if step == 0 {
		step = 1
	}
	b := h.Bytes()
	nonceAt := 16 + len(h.PrevHash) + len(h.MerkleRoot)
	target := new(big.Int).SetBytes(Target(h.Difficulty))
	var v big.Int
	nonce := start
	for i := uint64(0); i < tries; i++ {
		if i%64 == 0 && ctx.Err() != nil {
			return Header{}, false
		}
		for j := 0; j < 8; j++ {
			b[nonceAt+j] = byte(nonce >> (8 * j))
		}
		hash := Hash(b)
		hashes.Add(1)
		if v.SetBytes(hash).Cmp(target) < 0 && h.Difficulty > 0 {
			h.Nonce = nonce
			return h, true
		}
		nonce += step
	}
	return Header{}, false
}

// String is a header's short description, for logs.
func (h Header) String() string {
	return fmt.Sprintf("height %d nonce %d difficulty %d", h.Height, h.Nonce, h.Difficulty)
}
