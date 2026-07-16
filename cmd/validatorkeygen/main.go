// cmd/validatorkeygen/main.go
//
// Generates a fresh ed25519 keypair to act as a consensus key for testing
// the epoch-based Top-K validator selection design, and produces the
// proof-of-possession signature (over the miner's own bech32 address
// string) required by MsgRegisterValidatorPubkey.
//
// This is separate from the transaction's own signing key (secp256k1, via
// the keyring) -- the consensus key and the mining/account key are
// intentionally different keypairs; see aether-randomness-beacon-design.md.
//
// Usage:
//   go run ./cmd/validatorkeygen --miner cosmos14ky92qc9vgdlgjm2m870802t8l88vmh8fw3gmq
package main

import (
	"crypto/ed25519"
	"encoding/hex"
	"flag"
	"fmt"
	"os"
)

func main() {
	minerAddr := flag.String("miner", "", "bech32 miner address to bind this consensus key to (required)")
	flag.Parse()

	if *minerAddr == "" {
		fmt.Fprintln(os.Stderr, "error: --miner is required")
		os.Exit(1)
	}

	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error generating key: %v\n", err)
		os.Exit(1)
	}

	// Proof of possession: sign the miner's own address string with the
	// consensus private key. This matches exactly what msg_server.go's
	// RegisterValidatorPubkey handler verifies via ed25519.Verify.
	signature := ed25519.Sign(priv, []byte(*minerAddr))

	fmt.Printf("Generated consensus keypair for miner %s\n\n", *minerAddr)
	fmt.Printf("Consensus pubkey (hex):  %s\n", hex.EncodeToString(pub))
	fmt.Printf("Consensus privkey (hex): %s   (SAVE THIS -- needed to sign future blocks with this validator)\n\n", hex.EncodeToString(priv))
	fmt.Println("Register with:")
	fmt.Printf(
		"aetherd tx pow register-validator-pubkey %s %s --from %s --chain-id aether-testnet-1 --keyring-backend test --fees 0aeth -y\n",
		hex.EncodeToString(pub), hex.EncodeToString(signature), *minerAddr,
	)
}