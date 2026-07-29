package pow

import (
	"crypto/sha256"
	"testing"
	"bytes"
	"encoding/binary"

	"github.com/stretchr/testify/require"
)

func TestCheckMerkleBranch_SingleLevel_SiblingOnRight(t *testing.T) {
	leaf := []byte("leaf-data")
	sibling := []byte("sibling-data")

	// index=0 means sibling is on the RIGHT: hash(leaf || sibling)
	expected := doubleSHA256(append(append([]byte{}, leaf...), sibling...))

	result := checkMerkleBranch(leaf, [][]byte{sibling}, 0)
	require.Equal(t, expected, result)
}

func TestCheckMerkleBranch_SingleLevel_SiblingOnLeft(t *testing.T) {
	leaf := []byte("leaf-data")
	sibling := []byte("sibling-data")

	// index=1 means sibling is on the LEFT: hash(sibling || leaf)
	expected := doubleSHA256(append(append([]byte{}, sibling...), leaf...))

	result := checkMerkleBranch(leaf, [][]byte{sibling}, 1)
	require.Equal(t, expected, result)
}

func TestCheckMerkleBranch_TwoLevels_IndexBitsShiftCorrectly(t *testing.T) {
	leaf := []byte("leaf-data")
	siblingA := []byte("sibling-a")
	siblingB := []byte("sibling-b")

	// index=2 (binary 10): level 0 uses bit 0 (0, sibling on right),
	// level 1 uses bit 1 after shifting (1, sibling on left).
	level0 := doubleSHA256(append(append([]byte{}, leaf...), siblingA...))
	expected := doubleSHA256(append(append([]byte{}, siblingB...), level0...))

	result := checkMerkleBranch(leaf, [][]byte{siblingA, siblingB}, 2)
	require.Equal(t, expected, result)
}

func TestCheckMerkleBranch_EmptyBranchReturnsLeafUnchanged(t *testing.T) {
	leaf := []byte("leaf-data")
	result := checkMerkleBranch(leaf, [][]byte{}, 0)
	require.Equal(t, leaf, result)
}

func TestDoubleSHA256_IsGenuinelyDoubled(t *testing.T) {
	data := []byte("test-data")
	single := sha256.Sum256(data)
	expected := sha256.Sum256(single[:])

	result := doubleSHA256(data)
	require.Equal(t, expected[:], result)
}

func buildTestScriptSig(prefix []byte, rootHash []byte, size, nonce uint32) []byte {
	buf := make([]byte, 0)
	buf = append(buf, prefix...)
	buf = append(buf, mergeMiningMagic...)

	// Reverse the root hash before embedding, since extractCommitment
	// reverses it back out -- matches the real endian-conversion step.
	reversed := make([]byte, len(rootHash))
	copy(reversed, rootHash)
	for i, j := 0, len(reversed)-1; i < j; i, j = i+1, j-1 {
		reversed[i], reversed[j] = reversed[j], reversed[i]
	}
	buf = append(buf, reversed...)

	sizeBytes := make([]byte, 4)
	binary.LittleEndian.PutUint32(sizeBytes, size)
	buf = append(buf, sizeBytes...)

	nonceBytes := make([]byte, 4)
	binary.LittleEndian.PutUint32(nonceBytes, nonce)
	buf = append(buf, nonceBytes...)

	return buf
}

func TestExtractCommitment_ValidScriptSig(t *testing.T) {
	rootHash := bytes.Repeat([]byte{0xAB}, 32)
	scriptSig := buildTestScriptSig([]byte("some-prefix-bytes"), rootHash, 1024, 999999)

	result, err := extractCommitment(scriptSig)
	require.NoError(t, err)
	require.Equal(t, rootHash, result.RootHash)
	require.Equal(t, uint32(1024), result.Size)
	require.Equal(t, uint32(999999), result.Nonce)
}

func TestExtractCommitment_MissingMagicHeaderFails(t *testing.T) {
	scriptSig := []byte("no magic header anywhere in this script at all")
	_, err := extractCommitment(scriptSig)
	require.Error(t, err)
}

func TestExtractCommitment_DuplicateMagicHeaderFails(t *testing.T) {
	rootHash := bytes.Repeat([]byte{0xCD}, 32)
	scriptSig := buildTestScriptSig([]byte("prefix"), rootHash, 1, 1)
	// Append a second occurrence of the magic header.
	scriptSig = append(scriptSig, mergeMiningMagic...)

	_, err := extractCommitment(scriptSig)
	require.Error(t, err)
}

func TestExtractCommitment_TooShortForTrailerFails(t *testing.T) {
	scriptSig := append([]byte("prefix"), mergeMiningMagic...)
	scriptSig = append(scriptSig, bytes.Repeat([]byte{0x00}, 10)...) // way short of 32+8

	_, err := extractCommitment(scriptSig)
	require.Error(t, err)
}

func TestExtractCommitment_ZeroPrefixLength(t *testing.T) {
	rootHash := bytes.Repeat([]byte{0xEF}, 32)
	scriptSig := buildTestScriptSig([]byte{}, rootHash, 42, 12345)

	result, err := extractCommitment(scriptSig)
	require.NoError(t, err)
	require.Equal(t, rootHash, result.RootHash)
}

func TestGetExpectedIndex_MatchesHandComputedValue(t *testing.T) {
	// Manually compute using the exact same formula, independently, to
	// catch any transcription error in the implementation (e.g. wrong
	// operator precedence, wrong operand order) rather than just
	// re-deriving the same function twice.
	nonce := uint32(42)
	chainID := uint32(17776)
	h := uint32(3)

	rand := nonce
	rand = rand*1103515245 + 12345
	rand = rand + chainID
	rand = rand*1103515245 + 12345
	expected := rand % (1 << h)

	result := getExpectedIndex(nonce, chainID, h)
	require.Equal(t, expected, result)
}

func TestGetExpectedIndex_DifferentNoncesProduceDifferentIndices(t *testing.T) {
	// Not a formal proof of good distribution, just a basic sanity
	// check that the function isn't accidentally constant.
	h := uint32(8) // 256 possible slots, low collision chance for 2 samples
	a := getExpectedIndex(1, 17776, h)
	b := getExpectedIndex(2, 17776, h)
	require.NotEqual(t, a, b)
}

func TestGetExpectedIndex_ResultAlwaysWithinRange(t *testing.T) {
	h := uint32(4) // 16 possible slots
	for nonce := uint32(0); nonce < 1000; nonce++ {
		result := getExpectedIndex(nonce, 17776, h)
		require.Less(t, result, uint32(16))
	}
}

func TestGetExpectedIndex_DifferentChainIDsProduceDifferentIndicesForSameNonce(t *testing.T) {
	h := uint32(8)
	a := getExpectedIndex(42, 1, h)      // Namecoin's real chain ID
	b := getExpectedIndex(42, 17776, h) // Aether's chain ID
	require.NotEqual(t, a, b, "different chain IDs should (typically) diverge for the same nonce, confirming chainID actually factors into the computation")
}

func buildTestParentHeader(version, time, bits, nonce uint32, prevBlock, merkleRoot []byte) []byte {
	buf := make([]byte, 0, 80)

	versionBytes := make([]byte, 4)
	binary.LittleEndian.PutUint32(versionBytes, version)
	buf = append(buf, versionBytes...)

	buf = append(buf, prevBlock...)
	buf = append(buf, merkleRoot...)

	timeBytes := make([]byte, 4)
	binary.LittleEndian.PutUint32(timeBytes, time)
	buf = append(buf, timeBytes...)

	bitsBytes := make([]byte, 4)
	binary.LittleEndian.PutUint32(bitsBytes, bits)
	buf = append(buf, bitsBytes...)

	nonceBytes := make([]byte, 4)
	binary.LittleEndian.PutUint32(nonceBytes, nonce)
	buf = append(buf, nonceBytes...)

	return buf
}

func TestParseParentHeader_ValidHeader(t *testing.T) {
	prevBlock := bytes.Repeat([]byte{0x11}, 32)
	merkleRoot := bytes.Repeat([]byte{0x22}, 32)
	raw := buildTestParentHeader(0x00020062, 1700000000, 0x1e0ffff0, 12345, prevBlock, merkleRoot)

	h, err := parseParentHeader(raw)
	require.NoError(t, err)
	require.Equal(t, uint32(0x00020062), h.Version)
	require.Equal(t, prevBlock, h.HashPrevBlock)
	require.Equal(t, merkleRoot, h.HashMerkleRoot)
	require.Equal(t, uint32(1700000000), h.Time)
	require.Equal(t, uint32(0x1e0ffff0), h.Bits)
	require.Equal(t, uint32(12345), h.Nonce)
}

func TestParseParentHeader_WrongLengthFails(t *testing.T) {
	_, err := parseParentHeader(make([]byte, 79))
	require.Error(t, err)

	_, err = parseParentHeader(make([]byte, 81))
	require.Error(t, err)
}

func TestParentHeader_ChainIDExtraction(t *testing.T) {
	// version = 0x00620062 -> chain ID = version >> 16 = 0x0062 = 98,
	// Dogecoin's own real, documented chain ID -- a meaningful,
	// real-world value to test against, not an arbitrary one.
	raw := buildTestParentHeader(0x00620062, 0, 0, 0, make([]byte, 32), make([]byte, 32))
	h, err := parseParentHeader(raw)
	require.NoError(t, err)
	require.Equal(t, uint32(98), h.chainID())
}

func TestParentHeader_ScryptHashIsDeterministic(t *testing.T) {
	raw := buildTestParentHeader(1, 100, 200, 300, bytes.Repeat([]byte{0xAA}, 32), bytes.Repeat([]byte{0xBB}, 32))
	h, err := parseParentHeader(raw)
	require.NoError(t, err)

	hash1, err := h.scryptHash()
	require.NoError(t, err)
	hash2, err := h.scryptHash()
	require.NoError(t, err)

	require.Equal(t, hash1, hash2, "identical header must always produce identical scrypt hash")
	require.Len(t, hash1, 32)
}

func TestParentHeader_DifferentHeadersProduceDifferentHashes(t *testing.T) {
	rawA := buildTestParentHeader(1, 100, 200, 300, bytes.Repeat([]byte{0xAA}, 32), bytes.Repeat([]byte{0xBB}, 32))
	rawB := buildTestParentHeader(1, 100, 200, 301, bytes.Repeat([]byte{0xAA}, 32), bytes.Repeat([]byte{0xBB}, 32)) // nonce differs

	hA, _ := parseParentHeader(rawA)
	hB, _ := parseParentHeader(rawB)

	hashA, err := hA.scryptHash()
	require.NoError(t, err)
	hashB, err := hB.scryptHash()
	require.NoError(t, err)

	require.NotEqual(t, hashA, hashB)
}

func TestReadVarInt_SingleByteValue(t *testing.T) {
	data := []byte{0x05, 0xAA, 0xBB}
	val, n, err := readVarInt(data, 0)
	require.NoError(t, err)
	require.Equal(t, uint64(5), val)
	require.Equal(t, 1, n)
}

func TestReadVarInt_TwoByteEncoding(t *testing.T) {
	data := []byte{0xfd, 0x00, 0x01} // 0xfd prefix, then 0x0100 LE = 256
	val, n, err := readVarInt(data, 0)
	require.NoError(t, err)
	require.Equal(t, uint64(256), val)
	require.Equal(t, 3, n)
}

func TestReadVarInt_FourByteEncoding(t *testing.T) {
	data := []byte{0xfe, 0x00, 0x00, 0x01, 0x00} // 0xfe prefix, then 0x00010000 LE = 65536
	val, n, err := readVarInt(data, 0)
	require.NoError(t, err)
	require.Equal(t, uint64(65536), val)
	require.Equal(t, 5, n)
}

func TestReadVarInt_TruncatedDataFails(t *testing.T) {
	data := []byte{0xfd, 0x01} // claims 2-byte value but only 1 byte follows
	_, _, err := readVarInt(data, 0)
	require.Error(t, err)
}

func buildTestCoinbaseTx(scriptSig []byte) []byte {
	buf := make([]byte, 0)
	buf = append(buf, 0x01, 0x00, 0x00, 0x00) // version = 1, LE
	buf = append(buf, 0x01)                   // input count = 1 (single-byte varint)
	buf = append(buf, bytes.Repeat([]byte{0x00}, 32)...) // prev_txid, all zero
	buf = append(buf, 0xFF, 0xFF, 0xFF, 0xFF) // prev_vout = 0xFFFFFFFF

	// scriptSig length as single-byte varint (test scripts kept short)
	buf = append(buf, byte(len(scriptSig)))
	buf = append(buf, scriptSig...)

	buf = append(buf, 0xFF, 0xFF, 0xFF, 0xFF) // sequence
	buf = append(buf, 0x00)                   // output count = 0 (irrelevant for this test)
	buf = append(buf, 0x00, 0x00, 0x00, 0x00)  // locktime

	return buf
}

func TestExtractCoinbaseScriptSig_ValidTransaction(t *testing.T) {
	scriptSig := []byte("test-scriptsig-content")
	tx := buildTestCoinbaseTx(scriptSig)

	result, err := extractCoinbaseScriptSig(tx)
	require.NoError(t, err)
	require.Equal(t, scriptSig, result)
}

func TestExtractCoinbaseScriptSig_TruncatedTransactionFails(t *testing.T) {
	scriptSig := []byte("test-scriptsig-content")
	tx := buildTestCoinbaseTx(scriptSig)
	truncated := tx[:len(tx)-30] // cut off partway through the scriptSig

	_, err := extractCoinbaseScriptSig(truncated)
	require.Error(t, err)
}

func TestExtractCoinbaseScriptSig_EmptyScriptSig(t *testing.T) {
	tx := buildTestCoinbaseTx([]byte{})
	result, err := extractCoinbaseScriptSig(tx)
	require.NoError(t, err)
	require.Empty(t, result)
}

// buildValidAuxPow constructs a complete, internally self-consistent
// AuxPoW submission from scratch -- a fake but genuinely valid parent
// header, a fake but genuinely valid coinbase committing to
// auxBlockHash, and correct merkle branches -- for integration testing
// CheckAuxPow's full validation sequence.
func buildValidAuxPow(t *testing.T, auxBlockHash []byte, difficulty uint64) *AuxPowData {
	t.Helper()

	// Chain branch: zero-depth (h=0), meaning the coinbase's committed
	// root IS the aux block hash directly, with no intermediate
	// siblings -- the simplest possible valid case.
	chainBranchHashes := [][]byte{}
	nonce := uint32(0)

	// index must satisfy getExpectedIndex exactly, per the real,
	// required formula.
	expectedIndex := getExpectedIndex(nonce, AuxPoWChainID, 0)

	// At h=0, checkMerkleBranch with an empty branch just returns the
	// leaf unchanged -- so the committed root equals auxBlockHash
	// itself. Reverse it, since extractCommitment reverses it back.
	// buildTestScriptSig reverses whatever we give it exactly once,
// matching the real spec's single endian-conversion step -- pass
// auxBlockHash directly, unreversed. (Corrected: a prior version
// pre-reversed it here too, a double-reversal that only appeared
// correct because CheckAuxPow had its own separate, compensating
// extra-reversal bug -- see the fix in CheckAuxPow itself.)
scriptSig := buildTestScriptSig([]byte("miner-arbitrary-data"), auxBlockHash, 1, nonce) // size = 1<<0 = 1

	coinbaseTx := buildTestCoinbaseTx(scriptSig)

	// Coinbase branch: zero-depth too, meaning the coinbase tx hash
	// IS the parent block's merkle root directly.
	coinbaseTxHash := doubleSHA256(coinbaseTx)

	// Construct a parent header whose merkle root is this coinbase's
	// hash, and whose scrypt hash meets the given difficulty -- brute
	// force a nonce, same as real mining, since we need a genuinely
	// valid proof for the integration test to be meaningful.
	prevBlock := bytes.Repeat([]byte{0x11}, 32)
	var parentRaw []byte
	for n := uint32(0); ; n++ {
		candidate := buildTestParentHeader(1, 1700000000, 0x1e0ffff0, n, prevBlock, coinbaseTxHash)
		h, err := parseParentHeader(candidate)
		require.NoError(t, err)
		hash, err := h.scryptHash()
		require.NoError(t, err)
		if meetsdifficulty(hash, difficulty) {
			parentRaw = candidate
			break
		}
		if n > 200000 {
			t.Fatal("could not find a valid parent nonce within a reasonable attempt count -- difficulty may be set too high for a test")
		}
	}

	return &AuxPowData{
		ParentHeader:   parentRaw,
		CoinbaseTx:     coinbaseTx,
		CoinbaseBranch: &MerkleBranch{Hashes: [][]byte{}, Index: 0},
		ChainBranch:    &MerkleBranch{Hashes: chainBranchHashes, Index: expectedIndex},
		ChainNonce:     nonce,
		AuxBlockHash:   auxBlockHash,
	}
}

func TestCheckAuxPow_ValidSubmissionPasses(t *testing.T) {
	auxBlockHash := bytes.Repeat([]byte{0x42}, 32)
	difficulty := uint64(4) // low, so the test brute-forces quickly

	auxPow := buildValidAuxPow(t, auxBlockHash, difficulty)

	err := CheckAuxPow(auxPow, difficulty)
	require.NoError(t, err)
}

func TestCheckAuxPow_WrongAuxBlockHashFails(t *testing.T) {
	auxBlockHash := bytes.Repeat([]byte{0x42}, 32)
	difficulty := uint64(4)
	auxPow := buildValidAuxPow(t, auxBlockHash, difficulty)
	wrongHash := bytes.Repeat([]byte{0x99}, 32)
	auxPow.AuxBlockHash = wrongHash // tamper with the committed hash after construction
	err := CheckAuxPow(auxPow, difficulty)
	require.Error(t, err)
}

func TestCheckAuxPow_InsufficientParentPoWFails(t *testing.T) {
	auxBlockHash := bytes.Repeat([]byte{0x42}, 32)
	buildDifficulty := uint64(4)

	auxPow := buildValidAuxPow(t, auxBlockHash, buildDifficulty)

	// Check against a much higher difficulty than the parent header
	// actually satisfies.
	err := CheckAuxPow(auxPow, 1_000_000_000)
	require.Error(t, err)
}

func TestCheckAuxPow_TamperedCoinbaseBreaksMerkleProof(t *testing.T) {
	auxBlockHash := bytes.Repeat([]byte{0x42}, 32)
	difficulty := uint64(4)

	auxPow := buildValidAuxPow(t, auxBlockHash, difficulty)
	// Tamper with the coinbase after the parent header was mined
	// against the original -- this must break the merkle-root check,
	// since the parent header's committed root no longer matches.
	tampered := make([]byte, len(auxPow.CoinbaseTx))
	copy(tampered, auxPow.CoinbaseTx)
	tampered[len(tampered)-1] ^= 0xFF
	auxPow.CoinbaseTx = tampered

	err := CheckAuxPow(auxPow, difficulty)
	require.Error(t, err)
}

func TestCheckAuxPow_ExcessiveChainBranchLengthFails(t *testing.T) {
	auxBlockHash := bytes.Repeat([]byte{0x42}, 32)
	difficulty := uint64(4)

	auxPow := buildValidAuxPow(t, auxBlockHash, difficulty)
	tooLong := make([][]byte, maxChainMerkleBranchLength+1)
	for i := range tooLong {
		tooLong[i] = bytes.Repeat([]byte{0x00}, 32)
	}
	auxPow.ChainBranch.Hashes = tooLong

	err := CheckAuxPow(auxPow, difficulty)
	require.Error(t, err)
}