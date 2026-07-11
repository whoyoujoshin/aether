// cmd/powminer/main.go
//
// Standalone helper that brute-forces a nonce satisfying the pow module's
// difficulty target, then prints the exact `aetherd tx pow submit` command
// to run. This mirrors VerifyMiningHeader's hashing logic exactly, so a
// nonce found here is guaranteed to also pass on-chain verification
// (assuming the difficulty you pass matches the chain's current difficulty).
//
// Usage:
//   go run ./cmd/powminer \
//     --miner aether1yourvalidatoraddresshere \
//     --height 1 \
//     --difficulty 1048576 \
//     --max-attempts 10000000
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"math/big"
	"os"
	"time"

	sdk "github.com/cosmos/cosmos-sdk/types"
)

// header mirrors pow.MiningHeader's field layout exactly. Keeping this as
// a local struct (rather than importing the pow package) keeps this helper
// dependency-light; if pow.MiningHeader's layout changes, this must be
// updated to match or the found nonce won't validate on-chain.
type header struct {
	Height       uint64
	Timestamp    int64
	PrevHash     []byte
	MerkleRoot   []byte
	Nonce        uint64
	Difficulty   uint64
	MinerAddress sdk.AccAddress
}

// headerToBytes must match x/pow/keeper.go's headerToBytes byte-for-byte,
// or nonces found here will not satisfy on-chain verification.
func headerToBytes(h header) []byte {
	buf := make([]byte, 0, 64)
	var tmp [8]byte
	putU64 := func(v uint64) {
		for i := 0; i < 8; i++ {
			tmp[i] = byte(v >> (8 * i))
		}
		buf = append(buf, tmp[:]...)
	}
	putU64(h.Height)
	putU64(uint64(h.Timestamp))
	buf = append(buf, h.PrevHash...)
	buf = append(buf, h.MerkleRoot...)
	putU64(h.Nonce)
	putU64(h.Difficulty)
	buf = append(buf, h.MinerAddress.Bytes()...)
	return buf
}

// verify matches x/pow/keeper.go's VerifyMiningHeader exactly (post-fix
// big-integer target division, not the old bit-shift version).
func verify(h header) bool {
	if h.Difficulty == 0 {
		return false
	}
	data := headerToBytes(h)
	hash := sha256.Sum256(data)

	maxTarget := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 256), big.NewInt(1))
	difficulty := new(big.Int).SetUint64(h.Difficulty)
	target := new(big.Int).Div(maxTarget, difficulty)

	return new(big.Int).SetBytes(hash[:]).Cmp(target) < 0
}

func main() {
	minerStr := flag.String("miner", "", "bech32 miner address (required, must match --from key used to submit)")
	height := flag.Uint64("height", 1, "block height to submit for")
	difficulty := flag.Uint64("difficulty", 1048576, "target difficulty (must match chain's current difficulty; default matches InitialDifficulty = 1<<20)")
	prevHashHex := flag.String("prev-hash", "00000000000000000000000000000000000000000000000000000000000000", "prev block hash as hex (arbitrary is fine for this test — msg_server does not yet validate it against real chain state)")
	merkleRootHex := flag.String("merkle-root", "11111111111111111111111111111111111111111111111111111111111111", "merkle root as hex (arbitrary is fine for this test, same caveat as prev-hash)")
	maxAttempts := flag.Uint64("max-attempts", 10_000_000, "give up after this many nonce attempts")
	flag.Parse()

	if *minerStr == "" {
		fmt.Fprintln(os.Stderr, "error: --miner is required (bech32 address of the key you'll sign with)")
		os.Exit(1)
	}

	minerAddr, err := sdk.AccAddressFromBech32(*minerStr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: invalid --miner address: %v\n", err)
		os.Exit(1)
	}

	prevHash, err := hex.DecodeString(*prevHashHex)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: invalid --prev-hash hex: %v\n", err)
		os.Exit(1)
	}
	merkleRoot, err := hex.DecodeString(*merkleRootHex)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: invalid --merkle-root hex: %v\n", err)
		os.Exit(1)
	}

	timestamp := time.Now().Unix()

	fmt.Printf("Mining at difficulty %d (expected ~%d hashes on average)...\n", *difficulty, *difficulty)
	start := time.Now()

	h := header{
		Height:       *height,
		Timestamp:    timestamp,
		PrevHash:     prevHash,
		MerkleRoot:   merkleRoot,
		Difficulty:   *difficulty,
		MinerAddress: minerAddr,
	}

	var found bool
	var nonce uint64
	for nonce = 0; nonce < *maxAttempts; nonce++ {
		h.Nonce = nonce
		if verify(h) {
			found = true
			break
		}
		if nonce%500_000 == 0 && nonce > 0 {
			fmt.Printf("  ...%d attempts so far (%s elapsed)\n", nonce, time.Since(start).Round(time.Millisecond))
		}
	}

	if !found {
		fmt.Fprintf(os.Stderr, "no valid nonce found within %d attempts — try a lower --difficulty for testing\n", *maxAttempts)
		os.Exit(1)
	}

	elapsed := time.Since(start)
	fmt.Printf("\nFound valid nonce: %d (in %d attempts, %s)\n\n", nonce, nonce+1, elapsed.Round(time.Millisecond))

	fmt.Println("Submit with:")
	fmt.Printf(
		"aetherd tx pow submit %d %d %s %s %d %d --from %s --chain-id aether-dev-1 --keyring-backend test --fees 1000aeth -y\n",
		*height, timestamp, hex.EncodeToString(prevHash), hex.EncodeToString(merkleRoot), nonce, *difficulty, *minerStr,
	)
}
