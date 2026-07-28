// cmd/scryptbench/main.go
//
// Benchmarks real Scrypt throughput (Litecoin's parameters: N=1024, r=1,
// p=1) on this machine. Needed because SHA-256's difficulty constants
// mean nothing under Scrypt -- memory-hardness makes it dramatically
// slower per-hash, and difficulty needs to be retuned against REAL
// measured throughput, not guessed.
package main

import (
	"crypto/rand"
	"fmt"
	"time"

	"golang.org/x/crypto/scrypt"
)

const (
	scryptN = 1024
	scryptR = 1
	scryptP = 1
)

func main() {
	fmt.Println("Benchmarking real Scrypt throughput (Litecoin params: N=1024, r=1, p=1)...")

	// Warm-up, to avoid counting any one-time setup cost.
	data := make([]byte, 64)
	rand.Read(data)
	_, _ = scrypt.Key(data, data, scryptN, scryptR, scryptP, 32)

	const duration = 5 * time.Second
	start := time.Now()
	count := 0

	for time.Since(start) < duration {
		rand.Read(data)
		_, err := scrypt.Key(data, data, scryptN, scryptR, scryptP, 32)
		if err != nil {
			fmt.Printf("scrypt error: %v\n", err)
			return
		}
		count++
	}

	elapsed := time.Since(start)
	hashesPerSecond := float64(count) / elapsed.Seconds()

	fmt.Printf("\nCompleted %d scrypt hashes in %s\n", count, elapsed.Round(time.Millisecond))
	fmt.Printf("Real throughput: %.2f hashes/second (single-threaded)\n", hashesPerSecond)
	fmt.Printf("\nFor reference, at this rate:\n")
	fmt.Printf("  difficulty %d would average ~1 second to find a valid nonce\n", int64(hashesPerSecond))
	fmt.Printf("  difficulty %d would average ~%d seconds\n", int64(hashesPerSecond)*10, 10)
	fmt.Printf("  difficulty %d would average ~%d seconds\n", int64(hashesPerSecond)*60, 60)
}
