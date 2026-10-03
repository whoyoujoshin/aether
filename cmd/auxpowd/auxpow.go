package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"

	sdk "github.com/cosmos/cosmos-sdk/types"

	pow "github.com/whoyoujoshin/aether/x/pow"
)

// cAuxPow is the standard merged-mining proof as pools submit it
// (submitauxblock's second argument): Namecoin's and Dogecoin's CAuxPow
// serialization.
//
//	coinbase tx           the parent block's coinbase transaction
//	uint256 parent hash   unused (the parent header below is what counts)
//	vector<uint256>       coinbase merkle branch
//	int32                 coinbase index (0: the coinbase is the first tx)
//	vector<uint256>       chain merkle branch
//	int32                 chain index
//	80 bytes              parent block header
//
// Hashes are in the order they're serialized (internal byte order), the
// order x/pow's merkle checks use.
type cAuxPow struct {
	CoinbaseTx     []byte // legacy (no-witness) serialization: the bytes whose double SHA-256 is the txid
	CoinbaseBranch [][]byte
	CoinbaseIndex  uint32
	ChainBranch    [][]byte
	ChainIndex     uint32
	ParentHeader   []byte
}

// maxBranchLength bounds both merkle branches. x/pow refuses chain
// branches over 30; a coinbase branch of 32 covers any parent block.
const maxBranchLength = 32

// parseAuxPow decodes a serialized CAuxPow. It refuses trailing bytes:
// submitauxblock carries exactly one proof.
func parseAuxPow(b []byte) (*cAuxPow, error) {
	r := &reader{b: b}
	a, err := r.auxPow()
	if err != nil {
		return nil, err
	}
	if r.off != len(b) {
		return nil, fmt.Errorf("%d bytes after the parent header", len(b)-r.off)
	}
	return a, nil
}

func (r *reader) auxPow() (*cAuxPow, error) {
	tx, err := r.tx()
	if err != nil {
		return nil, fmt.Errorf("coinbase transaction: %w", err)
	}
	a := &cAuxPow{CoinbaseTx: tx}
	if _, err := r.bytes(32); err != nil {
		return nil, fmt.Errorf("parent hash: %w", err)
	}
	if a.CoinbaseBranch, err = r.branch(); err != nil {
		return nil, fmt.Errorf("coinbase branch: %w", err)
	}
	if a.CoinbaseIndex, err = r.uint32(); err != nil {
		return nil, fmt.Errorf("coinbase index: %w", err)
	}
	if a.ChainBranch, err = r.branch(); err != nil {
		return nil, fmt.Errorf("chain branch: %w", err)
	}
	if a.ChainIndex, err = r.uint32(); err != nil {
		return nil, fmt.Errorf("chain index: %w", err)
	}
	if a.ParentHeader, err = r.bytes(80); err != nil {
		return nil, fmt.Errorf("parent header: %w", err)
	}
	return a, nil
}

// auxPowData is the submission x/pow checks for the template it was mined
// against. chain_nonce comes from the coinbase's own commitment, where
// x/pow requires it to match.
func (a *cAuxPow) auxPowData(t *template) (*pow.AuxPowData, error) {
	_, nonce, err := pow.AuxPowCommitment(a.CoinbaseTx)
	if err != nil {
		return nil, fmt.Errorf("merge-mining commitment: %w", err)
	}
	return &pow.AuxPowData{
		ParentHeader:   a.ParentHeader,
		CoinbaseTx:     a.CoinbaseTx,
		CoinbaseBranch: &pow.MerkleBranch{Hashes: nonNil(a.CoinbaseBranch), Index: a.CoinbaseIndex},
		ChainBranch:    &pow.MerkleBranch{Hashes: nonNil(a.ChainBranch), Index: a.ChainIndex},
		ChainNonce:     nonce,
		AuxBlockHash:   t.Hash,
		TemplateHeight: t.Height,
		RewardAddress:  sdk.AccAddress(t.Reward).String(),
	}, nil
}

// committedTo reports whether the proof's coinbase commits to hash: the
// chain branch from hash reconstructs the coinbase's committed root, as
// x/pow checks.
func (a *cAuxPow) committedTo(hash []byte) bool {
	root, _, err := pow.AuxPowCommitment(a.CoinbaseTx)
	if err != nil {
		return false
	}
	h := hash
	index := a.ChainIndex
	for _, sibling := range a.ChainBranch {
		if index&1 == 1 {
			h = doubleSHA256(append(append([]byte{}, sibling...), h...))
		} else {
			h = doubleSHA256(append(append([]byte{}, h...), sibling...))
		}
		index >>= 1
	}
	return bytes.Equal(h, root)
}

func doubleSHA256(b []byte) []byte {
	first := sha256.Sum256(b)
	second := sha256.Sum256(first[:])
	return second[:]
}

func nonNil(b [][]byte) [][]byte {
	if b == nil {
		return [][]byte{}
	}
	return b
}

type reader struct {
	b   []byte
	off int
}

var errShort = errors.New("unexpected end of data")

func (r *reader) bytes(n int) ([]byte, error) {
	if n < 0 || len(r.b)-r.off < n {
		return nil, errShort
	}
	out := r.b[r.off : r.off+n]
	r.off += n
	return out, nil
}

func (r *reader) uint32() (uint32, error) {
	b, err := r.bytes(4)
	if err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint32(b), nil
}

// varint reads a compactSize, refusing a count larger than the bytes left
// could hold at minSize bytes each, so a hostile length can't force a huge
// allocation.
func (r *reader) varint(minSize int) (int, error) {
	first, err := r.bytes(1)
	if err != nil {
		return 0, err
	}
	var n uint64
	switch first[0] {
	case 0xfd:
		b, err := r.bytes(2)
		if err != nil {
			return 0, err
		}
		n = uint64(binary.LittleEndian.Uint16(b))
	case 0xfe:
		b, err := r.bytes(4)
		if err != nil {
			return 0, err
		}
		n = uint64(binary.LittleEndian.Uint32(b))
	case 0xff:
		b, err := r.bytes(8)
		if err != nil {
			return 0, err
		}
		n = binary.LittleEndian.Uint64(b)
	default:
		n = uint64(first[0])
	}
	if minSize < 1 {
		minSize = 1
	}
	if n > uint64((len(r.b)-r.off)/minSize) {
		return 0, fmt.Errorf("count %d exceeds the data left", n)
	}
	return int(n), nil
}

func (r *reader) branch() ([][]byte, error) {
	n, err := r.varint(32)
	if err != nil {
		return nil, err
	}
	if n > maxBranchLength {
		return nil, fmt.Errorf("%d hashes, more than %d", n, maxBranchLength)
	}
	hashes := make([][]byte, n)
	for i := range hashes {
		h, err := r.bytes(32)
		if err != nil {
			return nil, err
		}
		hashes[i] = append([]byte{}, h...)
	}
	return hashes, nil
}

// tx reads a Bitcoin-family transaction and returns its legacy
// serialization. A segwit coinbase (marker 0x00, flag 0x01) has its
// witness dropped: the legacy bytes are what the txid, and so the parent's
// merkle root, commits to.
func (r *reader) tx() ([]byte, error) {
	start := r.off
	if _, err := r.bytes(4); err != nil { // version
		return nil, err
	}
	segwit := len(r.b)-r.off >= 2 && r.b[r.off] == 0x00 && r.b[r.off+1] == 0x01
	var legacy []byte
	legacy = append(legacy, r.b[start:r.off]...)
	if segwit {
		r.off += 2
	}
	bodyStart := r.off

	inputs, err := r.varint(41)
	if err != nil {
		return nil, fmt.Errorf("input count: %w", err)
	}
	if inputs == 0 {
		return nil, errors.New("no inputs")
	}
	for i := 0; i < inputs; i++ {
		if _, err := r.bytes(36); err != nil { // previous outpoint
			return nil, err
		}
		if err := r.script(); err != nil {
			return nil, fmt.Errorf("input %d script: %w", i, err)
		}
		if _, err := r.bytes(4); err != nil { // sequence
			return nil, err
		}
	}
	outputs, err := r.varint(9)
	if err != nil {
		return nil, fmt.Errorf("output count: %w", err)
	}
	for i := 0; i < outputs; i++ {
		if _, err := r.bytes(8); err != nil { // value
			return nil, err
		}
		if err := r.script(); err != nil {
			return nil, fmt.Errorf("output %d script: %w", i, err)
		}
	}
	legacy = append(legacy, r.b[bodyStart:r.off]...)

	if segwit {
		for i := 0; i < inputs; i++ {
			items, err := r.varint(1)
			if err != nil {
				return nil, fmt.Errorf("input %d witness: %w", i, err)
			}
			for j := 0; j < items; j++ {
				if err := r.script(); err != nil {
					return nil, fmt.Errorf("input %d witness item %d: %w", i, j, err)
				}
			}
		}
	}
	lockTime, err := r.bytes(4)
	if err != nil {
		return nil, err
	}
	return append(legacy, lockTime...), nil
}

func (r *reader) script() error {
	n, err := r.varint(1)
	if err != nil {
		return err
	}
	_, err = r.bytes(n)
	return err
}
