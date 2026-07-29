// cmd/auxpowtest/main.go
//
// Constructs a REAL, internally self-consistent AuxPoW submission --
// a genuinely valid (brute-forced, real scrypt-hashed) fake parent
// header, a genuinely valid coinbase committing to a chosen aux block
// hash, and correct merkle branches -- for live devnet verification of
// the AuxPoW pipeline. This is the exact same construction technique
// used in x/pow's own TestCheckAuxPow_ValidSubmissionPasses
// integration test, just packaged as a standalone tool that writes
// real JSON output consumable by `aetherd tx pow submit-auxpow`.
//
// Usage:
//   go run ./cmd/auxpowtest --difficulty 4 --output auxpow.json
package main

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"math/big"
	"os"

	"golang.org/x/crypto/scrypt"
)

const (
	scryptN = 1024
	scryptR = 1
	scryptP = 1
)

var mergeMiningMagic = []byte{0xFA, 0xBE, 0x6D, 0x6D}

func doubleSHA256(data []byte) []byte {
	first := sha256.Sum256(data)
	second := sha256.Sum256(first[:])
	return second[:]
}

func meetsDifficulty(hash []byte, difficulty uint64) bool {
	if difficulty == 0 {
		return false
	}
	maxTarget := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 256), big.NewInt(1))
	diff := new(big.Int).SetUint64(difficulty)
	target := new(big.Int).Div(maxTarget, diff)
	return new(big.Int).SetBytes(hash).Cmp(target) < 0
}

// getExpectedIndex mirrors x/pow's own required formula exactly --
// this is not a free choice, so this standalone tool must reproduce
// it precisely for the resulting submission to validate.
func getExpectedIndex(nonce, chainID, h uint32) uint32 {
	rand := nonce
	rand = rand*1103515245 + 12345
	rand += chainID
	rand = rand*1103515245 + 12345
	return rand % (1 << h)
}

func buildParentHeader(version, timestamp, bits, nonce uint32, prevBlock, merkleRoot []byte) []byte {
	buf := make([]byte, 0, 80)
	tmp := make([]byte, 4)

	binary.LittleEndian.PutUint32(tmp, version)
	buf = append(buf, tmp...)
	buf = append(buf, prevBlock...)
	buf = append(buf, merkleRoot...)

	binary.LittleEndian.PutUint32(tmp, timestamp)
	buf = append(buf, tmp...)
	binary.LittleEndian.PutUint32(tmp, bits)
	buf = append(buf, tmp...)
	binary.LittleEndian.PutUint32(tmp, nonce)
	buf = append(buf, tmp...)

	return buf
}

func scryptHash(header []byte) ([]byte, error) {
	return scrypt.Key(header, header, scryptN, scryptR, scryptP, 32)
}

func buildScriptSig(prefix, reversedRootHash []byte, size, nonce uint32) []byte {
	buf := make([]byte, 0)
	buf = append(buf, prefix...)
	buf = append(buf, mergeMiningMagic...)
	buf = append(buf, reversedRootHash...)

	sizeBytes := make([]byte, 4)
	binary.LittleEndian.PutUint32(sizeBytes, size)
	buf = append(buf, sizeBytes...)

	nonceBytes := make([]byte, 4)
	binary.LittleEndian.PutUint32(nonceBytes, nonce)
	buf = append(buf, nonceBytes...)

	return buf
}

func buildCoinbaseTx(scriptSig []byte) []byte {
	buf := make([]byte, 0)
	buf = append(buf, 0x01, 0x00, 0x00, 0x00) // version = 1
	buf = append(buf, 0x01)                   // input count = 1
	buf = append(buf, make([]byte, 32)...)    // prev_txid, all zero
	buf = append(buf, 0xFF, 0xFF, 0xFF, 0xFF) // prev_vout

	buf = append(buf, byte(len(scriptSig))) // scriptSig length (single-byte varint, kept short)
	buf = append(buf, scriptSig...)

	buf = append(buf, 0xFF, 0xFF, 0xFF, 0xFF) // sequence
	buf = append(buf, 0x00)                   // output count = 0
	buf = append(buf, 0x00, 0x00, 0x00, 0x00) // locktime

	return buf
}

func reverse(b []byte) []byte {
	out := make([]byte, len(b))
	copy(out, b)
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

type merkleBranchJSON struct {
	Hashes []string `json:"hashes"`
	Index  uint32   `json:"index"`
}

type auxPowJSON struct {
	ParentHeader   string           `json:"parent_header"`
	CoinbaseTx     string           `json:"coinbase_tx"`
	CoinbaseBranch merkleBranchJSON `json:"coinbase_branch"`
	ChainBranch    merkleBranchJSON `json:"chain_branch"`
	ChainNonce     uint32           `json:"chain_nonce"`
	AuxBlockHash   string           `json:"aux_block_hash"`
}

func main() {
	difficulty := flag.Uint64("difficulty", 4, "difficulty to mine the fake parent header against (keep low for fast testing)")
	output := flag.String("output", "auxpow.json", "output JSON file path")
	auxChainID := flag.Uint("chain-id", 17776, "Aether's AuxPoW chain ID (must match x/pow.AuxPoWChainID)")
	flag.Parse()

	auxBlockHash := make([]byte, 32)
	for i := range auxBlockHash {
		auxBlockHash[i] = byte(0x42 + i%16) // arbitrary but fixed, real chain would use a real block hash
	}

	chainNonce := uint32(0)
	expectedIndex := getExpectedIndex(chainNonce, uint32(*auxChainID), 0) // h=0, empty chain branch

	// At h=0, the committed root is auxBlockHash itself (no siblings to combine with).
	committedRoot := reverse(auxBlockHash)

	scriptSig := buildScriptSig([]byte("auxpowtest-arbitrary-data"), committedRoot, 1, chainNonce) // size = 1<<0 = 1
	coinbaseTx := buildCoinbaseTx(scriptSig)
	coinbaseTxHash := doubleSHA256(coinbaseTx)

	fmt.Println("Mining a real, valid fake parent header (brute force, same as real mining)...")
	prevBlock := make([]byte, 32)
	for i := range prevBlock {
		prevBlock[i] = 0x11
	}

	var parentHeader []byte
	for nonce := uint32(0); ; nonce++ {
		candidate := buildParentHeader(1, 1700000000, 0x1e0ffff0, nonce, prevBlock, coinbaseTxHash)
		hash, err := scryptHash(candidate)
		if err != nil {
			fmt.Fprintf(os.Stderr, "scrypt error: %v\n", err)
			os.Exit(1)
		}
		if meetsDifficulty(hash, *difficulty) {
			parentHeader = candidate
			fmt.Printf("Found valid parent nonce: %d\n", nonce)
			break
		}
		if nonce > 5_000_000 {
			fmt.Fprintln(os.Stderr, "could not find a valid nonce within a reasonable attempt count -- try a lower --difficulty")
			os.Exit(1)
		}
	}

	result := auxPowJSON{
		ParentHeader: hex.EncodeToString(parentHeader),
		CoinbaseTx:   hex.EncodeToString(coinbaseTx),
		CoinbaseBranch: merkleBranchJSON{
			Hashes: []string{},
			Index:  0,
		},
		ChainBranch: merkleBranchJSON{
			Hashes: []string{},
			Index:  expectedIndex,
		},
		ChainNonce:   chainNonce,
		AuxBlockHash: hex.EncodeToString(auxBlockHash),
	}

	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to marshal json: %v\n", err)
		os.Exit(1)
	}

	if err := os.WriteFile(*output, data, 0644); err != nil {
		fmt.Fprintf(os.Stderr, "failed to write output file: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("\nWrote valid AuxPoW submission to %s\n", *output)
	fmt.Printf("Submit with:\n  aetherd tx pow submit-auxpow %s --from <your-account> --chain-id aether-testnet-1 --keyring-backend test --fees 0aeth -y\n", *output)
}
