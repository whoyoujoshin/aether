package main

import (
	"encoding/hex"
	"fmt"
	"math/big"
	"sync"

	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"

	pow "github.com/whoyoujoshin/aether/x/pow"
)

// chainState is what the bridge reads from an Aether node to hand out and
// check work.
type chainState struct {
	ChainID       string
	Height        int64  // the latest committed block
	BlockHash     []byte // its hash, which x/pow records for that height
	AuxDifficulty uint64 // what an AuxPoW submission in the next block must meet
	BlockReward   sdkmath.Int
	ShareBps      uint32 // the merged share of the reward while native mining is active
	RecencyWindow int64  // how many blocks back a template may commit to
}

// template is one unit of work: a recent Aether block and a reward
// address, committed to by Hash (x/pow.AuxPoWTemplateHash, internal byte
// order), which a pool puts in its parent coinbase.
type template struct {
	Hash      []byte
	Height    int64
	BlockHash []byte
	Reward    sdk.AccAddress
}

func newTemplate(st chainState, reward sdk.AccAddress) *template {
	return &template{
		Hash:      pow.AuxPoWTemplateHash(st.ChainID, st.Height, st.BlockHash, reward),
		Height:    st.Height,
		BlockHash: st.BlockHash,
		Reward:    reward,
	}
}

// templates caches the work handed out, by hash, until it's too old for
// the chain to accept: submitauxblock names a template only by its hash.
type templates struct {
	mu     sync.Mutex
	byHash map[string]*template
}

func newTemplates() *templates {
	return &templates{byHash: map[string]*template{}}
}

// get returns the template for the latest block and reward, creating it
// once, and drops templates the chain would now refuse.
func (s *templates) get(st chainState, reward sdk.AccAddress) *template {
	t := newTemplate(st, reward)
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, old := range s.byHash {
		if st.Height+1-old.Height > st.RecencyWindow {
			delete(s.byHash, k)
		}
	}
	if have, ok := s.byHash[string(t.Hash)]; ok {
		return have
	}
	s.byHash[string(t.Hash)] = t
	return t
}

func (s *templates) lookup(hash []byte) *template {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.byHash[string(hash)]
}

// auxBlock is createauxblock's (and getauxblock's) answer, in the fields
// and encodings Namecoin and Dogecoin use, so pool software reads it
// unchanged. Hashes are in display order (reversed), as pools expect. The
// target is sent twice, in the same little-endian bytes: as _target, the
// Namecoin API's name, and as target, the name Dogecoin's legacy API uses
// and pools such as yiimp read.
type auxBlock struct {
	Hash              string `json:"hash"`
	ChainID           uint32 `json:"chainid"`
	PreviousBlockHash string `json:"previousblockhash"`
	CoinbaseValue     int64  `json:"coinbasevalue"`
	Bits              string `json:"bits"`
	Height            int64  `json:"height"`
	Target            string `json:"_target"`
	LegacyTarget      string `json:"target"`
}

func (t *template) auxBlock(st chainState) auxBlock {
	target := auxTarget(st.AuxDifficulty)
	le := hex.EncodeToString(reversed(leftPad32(target.Bytes())))
	return auxBlock{
		Hash:              hex.EncodeToString(reversed(t.Hash)),
		ChainID:           pow.AuxPoWChainID,
		PreviousBlockHash: hex.EncodeToString(reversed(t.BlockHash)),
		CoinbaseValue:     mergedReward(st),
		Bits:              fmt.Sprintf("%08x", compact(target)),
		Height:            t.Height + 1,
		Target:            le,
		LegacyTarget:      le,
	}
}

// mergedReward is what an accepted AuxPoW submission earns while native
// mining is active, in uaeth: the block reward's merged share. A merged
// track mining alone earns the whole reward; pools are told the usual,
// lower figure.
func mergedReward(st chainState) int64 {
	share := st.BlockReward.MulRaw(int64(st.ShareBps)).QuoRaw(10_000)
	if !share.IsInt64() {
		return 0
	}
	return share.Int64()
}

// auxTarget is the target x/pow compares a parent's scrypt hash against,
// read little-endian as Litecoin reads it: (2^256 - 1) / difficulty.
func auxTarget(difficulty uint64) *big.Int {
	max := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 256), big.NewInt(1))
	if difficulty == 0 {
		return max
	}
	return max.Div(max, new(big.Int).SetUint64(difficulty))
}

// compact encodes a target as a header's nBits. It rounds down, so work
// meeting the compact target also meets the exact one.
func compact(target *big.Int) uint32 {
	size := uint32(len(target.Bytes()))
	var mantissa uint32
	if size <= 3 {
		mantissa = uint32(target.Uint64() << (8 * (3 - size)))
	} else {
		mantissa = uint32(new(big.Int).Rsh(target, uint(8*(size-3))).Uint64())
	}
	if mantissa&0x00800000 != 0 {
		mantissa >>= 8
		size++
	}
	return size<<24 | mantissa
}

func leftPad32(b []byte) []byte {
	out := make([]byte, 32)
	copy(out[32-len(b):], b)
	return out
}

func reversed(b []byte) []byte {
	out := make([]byte, len(b))
	for i := range b {
		out[len(b)-1-i] = b[i]
	}
	return out
}

// blockTemplate is getblocktemplate's answer, for pool software that polls
// every daemon it mines with getblocktemplate, merged-mined ones included
// (yiimp does), before asking for aux work. It describes the next Aether
// block in bitcoind's fields and carries no transactions: the work itself
// comes from createauxblock or getauxblock. target is big-endian here, as
// in bitcoind's getblocktemplate.
type blockTemplate struct {
	Version           int32             `json:"version"`
	PreviousBlockHash string            `json:"previousblockhash"`
	Transactions      []any             `json:"transactions"`
	CoinbaseAux       map[string]string `json:"coinbaseaux"`
	CoinbaseValue     int64             `json:"coinbasevalue"`
	Target            string            `json:"target"`
	Mutable           []string          `json:"mutable"`
	NonceRange        string            `json:"noncerange"`
	CurTime           int64             `json:"curtime"`
	Bits              string            `json:"bits"`
	Height            int64             `json:"height"`
}

func newBlockTemplate(st chainState, now int64) blockTemplate {
	target := auxTarget(st.AuxDifficulty)
	return blockTemplate{
		Version:           1,
		PreviousBlockHash: hex.EncodeToString(reversed(st.BlockHash)),
		Transactions:      []any{},
		CoinbaseAux:       map[string]string{"flags": ""},
		CoinbaseValue:     mergedReward(st),
		Target:            hex.EncodeToString(leftPad32(target.Bytes())),
		Mutable:           []string{},
		NonceRange:        "00000000ffffffff",
		CurTime:           now,
		Bits:              fmt.Sprintf("%08x", compact(target)),
		Height:            st.Height + 1,
	}
}
