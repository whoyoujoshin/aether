package pow

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"math/big"

	"golang.org/x/crypto/scrypt"
)

// checkMerkleBranch reconstructs a Merkle root from a leaf hash and a
// branch of sibling hashes, using the exact algorithm real Litecoin/
// Dogecoin AuxPoW verification uses -- confirmed against Dogecoin
// Core's primary source (auxpow.cpp), not assumed. Critically, this
// always uses double-SHA256, regardless of Aether's own native Scrypt
// hash function -- Merkle trees in this format are a fixed property
// of the system being interoperated with, not a free choice.
//
// The index's bits directly encode left/right at each level: if the
// current bit is set, the sibling is on the LEFT (hash goes second in
// the concatenation); if unset, the sibling is on the RIGHT (hash goes
// first). The index is shifted right after each level.
func checkMerkleBranch(leaf []byte, branch [][]byte, index uint32) []byte {
	hash := leaf
	for _, sibling := range branch {
		var combined []byte
		if index&1 == 1 {
			combined = append(append([]byte{}, sibling...), hash...)
		} else {
			combined = append(append([]byte{}, hash...), sibling...)
		}
		hash = doubleSHA256(combined)
		index >>= 1
	}
	return hash
}

func doubleSHA256(data []byte) []byte {
	first := sha256.Sum256(data)
	second := sha256.Sum256(first[:])
	return second[:]
}

var mergeMiningMagic = []byte{0xFA, 0xBE, 0x6D, 0x6D}

// parsedCommitment holds the merge-mining data extracted from a
// coinbase transaction's scriptSig.
type parsedCommitment struct {
	RootHash []byte // the claimed chain merkle root, byte-order already reversed
	Size     uint32
	Nonce    uint32
}

// extractCommitment locates the merge-mining magic header inside a
// coinbase scriptSig and parses the root hash + size/nonce trailer
// that follow it, per the exact structural rules in
// auxpow-binary-format-reference.md section 4. Does not handle the
// legacy no-magic-header fallback -- Aether requires the magic header
// to be present, a stricter but simpler and equally valid rule (real
// Dogecoin/Litecoin miners producing genuine AuxPoW submissions always
// include it; the fallback exists in the reference implementation only
// for backward compatibility with pre-magic-header-era blocks, which
// is not a concern for a chain launching today).
func extractCommitment(scriptSig []byte) (*parsedCommitment, error) {
	idx := bytes.Index(scriptSig, mergeMiningMagic)
	if idx == -1 {
		return nil, errors.New("merge-mining magic header not found in coinbase scriptSig")
	}

	if bytes.Index(scriptSig[idx+1:], mergeMiningMagic) != -1 {
		return nil, errors.New("merge-mining magic header appears more than once")
	}

	rootStart := idx + len(mergeMiningMagic)
	rootEnd := rootStart + 32
	trailerEnd := rootEnd + 8
	if trailerEnd > len(scriptSig) {
		return nil, errors.New("coinbase scriptSig too short for root hash + size/nonce trailer")
	}

	rootHash := make([]byte, 32)
	copy(rootHash, scriptSig[rootStart:rootEnd])
	for i, j := 0, len(rootHash)-1; i < j; i, j = i+1, j-1 {
		rootHash[i], rootHash[j] = rootHash[j], rootHash[i]
	}

	size := binary.LittleEndian.Uint32(scriptSig[rootEnd : rootEnd+4])
	nonce := binary.LittleEndian.Uint32(scriptSig[rootEnd+4 : rootEnd+8])

	return &parsedCommitment{
		RootHash: rootHash,
		Size:     size,
		Nonce:    nonce,
	}, nil
}

// getExpectedIndex reproduces the exact deterministic formula real
// Litecoin/Dogecoin AuxPoW requires nChainIndex to satisfy -- a
// standard glibc-style LCG, confirmed against Dogecoin Core's primary
// source (auxpow.cpp). This is NOT a free value a miner can choose;
// a submission whose chain_branch.index doesn't match this exact
// computation must be rejected, or a forged proof with a convenient
// but invalid index could otherwise slip through.
func getExpectedIndex(nonce uint32, chainID uint32, h uint32) uint32 {
	rand := nonce
	rand = rand*1103515245 + 12345
	rand += chainID
	rand = rand*1103515245 + 12345
	return rand % (1 << h)
}

const parentHeaderSize = 80

// parentHeader holds the parsed fields of a Litecoin/Dogecoin block
// header, per auxpow-binary-format-reference.md section 1.
type parentHeader struct {
	Version       uint32
	HashPrevBlock []byte
	HashMerkleRoot []byte
	Time          uint32
	Bits          uint32
	Nonce         uint32
	raw           []byte // the original 80 bytes, needed to compute the header's own hash
}

// parseParentHeader deserializes a raw 80-byte Litecoin/Dogecoin
// block header. Field order, sizes, and endianness confirmed against
// Dogecoin Core's primary source (primitives/pureheader.h), not
// assumed.
func parseParentHeader(data []byte) (*parentHeader, error) {
	if len(data) != parentHeaderSize {
		return nil, fmt.Errorf("parent header must be exactly %d bytes, got %d", parentHeaderSize, len(data))
	}

	h := &parentHeader{raw: data}
	h.Version = binary.LittleEndian.Uint32(data[0:4])
	h.HashPrevBlock = append([]byte{}, data[4:36]...)
	h.HashMerkleRoot = append([]byte{}, data[36:68]...)
	h.Time = binary.LittleEndian.Uint32(data[68:72])
	h.Bits = binary.LittleEndian.Uint32(data[72:76])
	h.Nonce = binary.LittleEndian.Uint32(data[76:80])

	return h, nil
}

// chainID extracts the parent block's own declared chain ID from its
// version field -- GetChainId() is nVersion >> 16 in the real
// reference implementation, confirmed directly from Dogecoin Core's
// chain.h (16 bits reserved for chain ID, above the base version and
// the auxpow flag bit).
func (h *parentHeader) chainID() uint32 {
	return h.Version >> 16
}

// scryptHash computes the parent header's proof-of-work hash using
// the exact same Scrypt parameters Aether's own native mining uses
// (Litecoin's real N=1024, r=1, p=1) -- both chains share the Scrypt
// algorithm by design, which is precisely what makes merged mining
// possible: the same real computed hash can satisfy both chains'
// difficulty targets simultaneously.
func (h *parentHeader) scryptHash() ([]byte, error) {
	return scrypt.Key(h.raw, h.raw, ScryptN, ScryptR, ScryptP, 32)
}

const AuxPoWChainID uint32 = 17776
const maxChainMerkleBranchLength = 30

// CheckAuxPow performs the complete AuxPoW validation sequence,
// matching the real reference implementation's order exactly (see
// auxpow-binary-format-reference.md section 5), plus the actual
// proof-of-work check the reference implementation explicitly does
// NOT perform itself (confirmed directly from its own source comment)
// -- checking the parent header's real Scrypt hash against Aether's
// own current difficulty target, not the parent chain's own nBits.
//
// auxBlockHash is Aether's own block hash being committed via merged
// mining. currentDifficulty is Aether's live difficulty target (from
// GetDifficulty), the same value native submissions are checked
// against.
func CheckAuxPow(data *AuxPowData, currentDifficulty uint64) error {
	auxBlockHash := data.AuxBlockHash
	if len(data.ChainBranch.Hashes) > maxChainMerkleBranchLength {
		return fmt.Errorf("chain merkle branch too long: %d exceeds maximum of %d", len(data.ChainBranch.Hashes), maxChainMerkleBranchLength)
	}

	parent, err := parseParentHeader(data.ParentHeader)
	if err != nil {
		return fmt.Errorf("invalid parent header: %w", err)
	}

	// 1. Coinbase must genuinely be part of the parent block's own
	// merkle tree.
	coinbaseTxHash := doubleSHA256(data.CoinbaseTx)
	reconstructedBlockRoot := checkMerkleBranch(coinbaseTxHash, data.CoinbaseBranch.Hashes, data.CoinbaseBranch.Index)
	if !bytes.Equal(reconstructedBlockRoot, parent.HashMerkleRoot) {
		return errors.New("coinbase merkle branch does not reconstruct the parent block's merkle root")
	}

	// 2. Extract and validate the merge-mining commitment embedded in
	// the coinbase.
	coinbaseTxParsed, err := extractCoinbaseScriptSig(data.CoinbaseTx)
	if err != nil {
		return fmt.Errorf("failed to locate coinbase scriptSig: %w", err)
	}
	commitment, err := extractCommitment(coinbaseTxParsed)
	if err != nil {
		return fmt.Errorf("invalid merge-mining commitment: %w", err)
	}

	// 3. Aether's own block hash must genuinely be committed via the
	// chain merkle branch.
	reconstructedChainRoot := checkMerkleBranch(auxBlockHash, data.ChainBranch.Hashes, data.ChainBranch.Index)
	reversedChainRoot := make([]byte, len(reconstructedChainRoot))
	copy(reversedChainRoot, reconstructedChainRoot)
	for i, j := 0, len(reversedChainRoot)-1; i < j; i, j = i+1, j-1 {
		reversedChainRoot[i], reversedChainRoot[j] = reversedChainRoot[j], reversedChainRoot[i]
	}
	if !bytes.Equal(reversedChainRoot, commitment.RootHash) {
		return errors.New("chain merkle branch does not reconstruct the committed aux block hash")
	}

	// 4. The commitment's declared size must match the actual branch
	// depth.
	expectedSize := uint32(1) << uint32(len(data.ChainBranch.Hashes))
	if commitment.Size != expectedSize {
		return fmt.Errorf("commitment size %d does not match expected %d for branch depth %d", commitment.Size, expectedSize, len(data.ChainBranch.Hashes))
	}

	// 5. The chain branch's index is not a free choice -- it must
	// match the required deterministic formula exactly.
	expectedIndex := getExpectedIndex(data.ChainNonce, AuxPoWChainID, uint32(len(data.ChainBranch.Hashes)))
	if data.ChainBranch.Index != expectedIndex {
		return fmt.Errorf("chain branch index %d does not match required expected index %d", data.ChainBranch.Index, expectedIndex)
	}

	// 6. The actual proof-of-work check -- explicitly NOT covered by
	// the merkle/structural checks above (confirmed from the reference
	// implementation's own source comment). The parent header's real
	// Scrypt hash must meet AETHER's difficulty, not the parent's own.
	scryptHash, err := parent.scryptHash()
	if err != nil {
		return fmt.Errorf("failed to compute parent header scrypt hash: %w", err)
	}
	if !meetsdifficulty(scryptHash, currentDifficulty) {
		return errors.New("parent header's proof of work does not meet Aether's current difficulty")
	}

	return nil
}

// readVarInt parses a Bitcoin-family compactSize varint starting at
// offset, per auxpow-binary-format-reference.md section 7. Returns
// the parsed value and the number of bytes consumed.
func readVarInt(data []byte, offset int) (uint64, int, error) {
	if offset >= len(data) {
		return 0, 0, errors.New("unexpected end of data reading varint")
	}
	first := data[offset]
	switch {
	case first < 0xfd:
		return uint64(first), 1, nil
	case first == 0xfd:
		if offset+3 > len(data) {
			return 0, 0, errors.New("unexpected end of data reading 2-byte varint")
		}
		return uint64(binary.LittleEndian.Uint16(data[offset+1 : offset+3])), 3, nil
	case first == 0xfe:
		if offset+5 > len(data) {
			return 0, 0, errors.New("unexpected end of data reading 4-byte varint")
		}
		return uint64(binary.LittleEndian.Uint32(data[offset+1 : offset+5])), 5, nil
	default: // 0xff
		if offset+9 > len(data) {
			return 0, 0, errors.New("unexpected end of data reading 8-byte varint")
		}
		return binary.LittleEndian.Uint64(data[offset+1 : offset+9]), 9, nil
	}
}

// extractCoinbaseScriptSig locates and extracts just the scriptSig
// bytes from a full serialized coinbase transaction, per the legacy
// (non-SegWit) Bitcoin-family transaction layout confirmed in
// auxpow-binary-format-reference.md section 7. Only parses far enough
// to reach the scriptSig -- everything after it (sequence, outputs,
// locktime) is irrelevant to merge-mining commitment extraction and
// is deliberately not parsed.
func extractCoinbaseScriptSig(tx []byte) ([]byte, error) {
	offset := 4 // skip 4-byte version

	inputCount, n, err := readVarInt(tx, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to read input count: %w", err)
	}
	offset += n

	if inputCount < 1 {
		return nil, errors.New("coinbase transaction must have at least one input")
	}

	offset += 32 // skip prev_txid
	offset += 4  // skip prev_vout

	scriptSigLen, n, err := readVarInt(tx, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to read scriptSig length: %w", err)
	}
	offset += n

	if offset+int(scriptSigLen) > len(tx) {
		return nil, errors.New("scriptSig length extends beyond end of transaction data")
	}

	return tx[offset : offset+int(scriptSigLen)], nil
}

// meetsdifficulty checks whether hash, interpreted as a big-endian
// 256-bit number, is below the target implied by difficulty. Shared
// by both native (VerifyMiningHeader) and AuxPoW (CheckAuxPow)
// verification paths -- identical math, one implementation.
func meetsdifficulty(hash []byte, difficulty uint64) bool {
	if difficulty == 0 {
		return false
	}
	maxTarget := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 256), big.NewInt(1))
	diff := new(big.Int).SetUint64(difficulty)
	target := new(big.Int).Div(maxTarget, diff)

	return new(big.Int).SetBytes(hash).Cmp(target) < 0
}
