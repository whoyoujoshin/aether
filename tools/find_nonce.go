// find_nonce.go — standalone helper, NOT part of the aetherd binary.
//
// Brute-forces a nonce that satisfies the same check as
// x/pow/keeper.go's VerifyMiningHeader:
//
//   sha256(header_bytes) < 2^(256 - difficulty)
//
// This mirrors headerToBytes()'s exact field order/encoding, so a nonce
// found here will actually pass verification on-chain.
//
// Usage:
//   go run find_nonce.go <height> <timestamp> <prev-hash-hex> <merkle-root-hex> <difficulty> <miner-bech32>
//
// Example (genesis difficulty is 1<<20 = 1048576):
//   go run find_nonce.go 1 1735689600 00 00 20 cosmos1ensperc7e66q3mkkzul20nth7dq2y53uuns3pq
//
// Prints the winning nonce plus the exact `aetherd tx pow submit` command
// to run with it.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math/big"
	"os"
	"strconv"
	"time"
)

// mirrors sdk.AccAddress.Bytes() for a bech32 "cosmos1..." address: the
// underlying 20-byte payload. We decode it ourselves here so this file has
// no dependency on the cosmos-sdk module (keeps it a simple standalone script).
func mustBech32Payload(bech32Addr string) []byte {
	hrp, data, err := decodeBech32(bech32Addr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "invalid bech32 address %q: %v\n", bech32Addr, err)
		os.Exit(1)
	}
	if hrp != "cosmos" {
		fmt.Fprintf(os.Stderr, "warning: expected hrp 'cosmos', got %q\n", hrp)
	}
	return data
}

// headerToBytes MUST exactly match x/pow/keeper.go's headerToBytes.
// If you change one, change the other, or verification will silently
// disagree between this tool and the chain.
func headerToBytes(height uint64, timestamp int64, prevHash, merkleRoot []byte, nonce, difficulty uint64, minerBytes []byte) []byte {
	buf := make([]byte, 0, 64)
	var tmp [8]byte
	putU64 := func(v uint64) {
		for i := 0; i < 8; i++ {
			tmp[i] = byte(v >> (8 * i))
		}
		buf = append(buf, tmp[:]...)
	}
	putU64(height)
	putU64(uint64(timestamp))
	buf = append(buf, prevHash...)
	buf = append(buf, merkleRoot...)
	putU64(nonce)
	putU64(difficulty)
	buf = append(buf, minerBytes...)
	return buf
}

func verifies(hash []byte, difficulty uint64) bool {
	if difficulty == 0 || difficulty >= 256 {
		return false
	}
	target := new(big.Int).Lsh(big.NewInt(1), uint(256-difficulty))
	return new(big.Int).SetBytes(hash).Cmp(target) < 0
}

func main() {
	if len(os.Args) != 7 {
		fmt.Println("usage: go run find_nonce.go <height> <timestamp> <prev-hash-hex> <merkle-root-hex> <difficulty> <miner-bech32>")
		fmt.Println("example: go run find_nonce.go 1 1735689600 00 00 20 cosmos1ensperc7e66q3mkkzul20nth7dq2y53uuns3pq")
		os.Exit(1)
	}

	height, err := strconv.ParseUint(os.Args[1], 10, 64)
	if err != nil {
		fmt.Fprintf(os.Stderr, "invalid height: %v\n", err)
		os.Exit(1)
	}
	timestamp, err := strconv.ParseInt(os.Args[2], 10, 64)
	if err != nil {
		fmt.Fprintf(os.Stderr, "invalid timestamp: %v\n", err)
		os.Exit(1)
	}
	prevHash, err := hex.DecodeString(os.Args[3])
	if err != nil {
		fmt.Fprintf(os.Stderr, "invalid prev-hash hex: %v\n", err)
		os.Exit(1)
	}
	merkleRoot, err := hex.DecodeString(os.Args[4])
	if err != nil {
		fmt.Fprintf(os.Stderr, "invalid merkle-root hex: %v\n", err)
		os.Exit(1)
	}
	difficulty, err := strconv.ParseUint(os.Args[5], 10, 64)
	if err != nil {
		fmt.Fprintf(os.Stderr, "invalid difficulty: %v\n", err)
		os.Exit(1)
	}
	minerAddr := os.Args[6]
	minerBytes := mustBech32Payload(minerAddr)

	fmt.Printf("Searching for a nonce at difficulty %d (target ~1 in 2^%d hashes)...\n", difficulty, difficulty)
	start := time.Now()

	var nonce uint64
	var found bool
	for nonce = 0; nonce < 1<<32; nonce++ {
		data := headerToBytes(height, timestamp, prevHash, merkleRoot, nonce, difficulty, minerBytes)
		hash := sha256.Sum256(data)
		if verifies(hash[:], difficulty) {
			found = true
			break
		}
		if nonce%5_000_000 == 0 && nonce > 0 {
			fmt.Printf("  ...tried %d nonces so far (%.1fs elapsed)\n", nonce, time.Since(start).Seconds())
		}
	}

	if !found {
		fmt.Println("No nonce found in range — try a lower difficulty for testing.")
		os.Exit(1)
	}

	elapsed := time.Since(start)
	fmt.Printf("\nFound nonce %d in %.2fs\n\n", nonce, elapsed.Seconds())

	prevHashHex := hex.EncodeToString(prevHash)
	if prevHashHex == "" {
		prevHashHex = "00"
	}
	merkleRootHex := hex.EncodeToString(merkleRoot)
	if merkleRootHex == "" {
		merkleRootHex = "00"
	}

	fmt.Println("Run this to submit it:")
	fmt.Printf(
		"go run ./cmd/aetherd tx pow submit %d %d %s %s %d %d --from myval --chain-id aether-testnet-1 --fees 0aeth --keyring-backend test -y\n",
		height, timestamp, prevHashHex, merkleRootHex, nonce, difficulty,
	)
}

// --- minimal bech32 decoder (no external deps, so this file stays standalone) ---

const bech32Charset = "qpzry9x8gf2tvdw0s3jn54khce6mua7l"

func decodeBech32(s string) (hrp string, data []byte, err error) {
	if len(s) < 8 || len(s) > 90 {
		return "", nil, fmt.Errorf("invalid length")
	}
	pos := -1
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == '1' {
			pos = i
			break
		}
	}
	if pos < 1 || pos+7 > len(s) {
		return "", nil, fmt.Errorf("invalid separator position")
	}
	hrp = s[:pos]
	dataPart := s[pos+1:]

	values := make([]byte, len(dataPart))
	for i, c := range dataPart {
		idx := -1
		for j, cc := range bech32Charset {
			if cc == c {
				idx = j
				break
			}
		}
		if idx == -1 {
			return "", nil, fmt.Errorf("invalid character %q", c)
		}
		values[i] = byte(idx)
	}

	// drop the 6-character checksum, convert 5-bit groups back to bytes
	values = values[:len(values)-6]
	converted, err := convertBits(values, 5, 8, false)
	if err != nil {
		return "", nil, err
	}
	return hrp, converted, nil
}

func convertBits(data []byte, fromBits, toBits uint, pad bool) ([]byte, error) {
	acc := 0
	bits := uint(0)
	var ret []byte
	maxv := (1 << toBits) - 1
	for _, value := range data {
		if int(value) < 0 || int(value)>>fromBits != 0 {
			return nil, fmt.Errorf("invalid data value")
		}
		acc = (acc << fromBits) | int(value)
		bits += fromBits
		for bits >= toBits {
			bits -= toBits
			ret = append(ret, byte((acc>>bits)&maxv))
		}
	}
	if pad {
		if bits > 0 {
			ret = append(ret, byte((acc<<(toBits-bits))&maxv))
		}
	} else if bits >= fromBits || ((acc<<(toBits-bits))&maxv) != 0 {
		return nil, fmt.Errorf("invalid padding")
	}
	return ret, nil
}
