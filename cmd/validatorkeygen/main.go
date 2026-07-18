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
	"encoding/base64"
	"encoding/hex"
	"flag"
	"fmt"
	"os"
)

func main() {
	minerAddr := flag.String("miner", "", "bech32 miner address to bind this consensus key to (required)")
	existingPrivKeyB64 := flag.String("existing-priv-key-b64", "", "base64-encoded existing ed25519 private key (64 bytes) to use instead of generating a fresh one -- e.g. from priv_validator_key.json's priv_key.value")
	flag.Parse()

	if *minerAddr == "" {
		fmt.Fprintln(os.Stderr, "error: --miner is required")
		os.Exit(1)
	}

	var pub ed25519.PublicKey
	var priv ed25519.PrivateKey

	if *existingPrivKeyB64 != "" {
		decoded, err := base64.StdEncoding.DecodeString(*existingPrivKeyB64)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error decoding --existing-priv-key-b64: %v\n", err)
			os.Exit(1)
		}
		if len(decoded) != ed25519.PrivateKeySize {
			fmt.Fprintf(os.Stderr, "error: decoded key is %d bytes, expected %d (ed25519 private key)\n", len(decoded), ed25519.PrivateKeySize)
			os.Exit(1)
		}
		priv = ed25519.PrivateKey(decoded)
		pub = priv.Public().(ed25519.PublicKey)
		fmt.Println("Using provided existing consensus private key (not generating a new one).")
	} else {
		var err error
		pub, priv, err = ed25519.GenerateKey(nil)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error generating key: %v\n", err)
			os.Exit(1)
		}
	}

	signature := ed25519.Sign(priv, []byte(*minerAddr))

	fmt.Printf("Consensus pubkey (hex):  %s\n", hex.EncodeToString(pub))
	if *existingPrivKeyB64 == "" {
		fmt.Printf("Consensus privkey (hex): %s   (SAVE THIS)\n", hex.EncodeToString(priv))
	}
	fmt.Println("\nRegister with:")
	fmt.Printf(
		"aetherd tx pow register-validator-pubkey %s %s --from %s --chain-id aether-testnet-1 --keyring-backend test --fees 0aeth -y\n",
		hex.EncodeToString(pub), hex.EncodeToString(signature), *minerAddr,
	)
}