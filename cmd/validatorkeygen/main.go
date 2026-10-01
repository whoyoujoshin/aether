// cmd/validatorkeygen/main.go
//
// Produces the proof-of-possession signature (over the miner's own bech32
// address string) that MsgRegisterValidatorPubkey requires, and prints
// the register-validator-pubkey command to run.
//
// By default it signs with the node's own consensus key, read from
// <home>/config/priv_validator_key.json: the key CometBFT signs blocks
// with, so the registered key is one the node actually holds. Registering
// any other key makes the miner a validator nothing signs for. --new-key
// generates a fresh keypair instead and prints the priv_validator_key.json
// to install on the node before registering it.
//
// This is separate from the transaction's own signing key (ML-DSA, via
// the keyring) -- the consensus key and the mining/account key are
// intentionally different keypairs; see aether-randomness-beacon-design.md.
//
// Usage:
//
//	go run ./cmd/validatorkeygen --miner aether1... [--home ~/.aether]
//	go run ./cmd/validatorkeygen --miner aether1... --new-key
package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	cometed25519 "github.com/cometbft/cometbft/crypto/ed25519"

	"github.com/whoyoujoshin/aether/app"
)

func init() {
	app.SetAddressPrefixes()
}

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

// privValidatorKey is the part of CometBFT's priv_validator_key.json this
// tool reads and writes.
type privValidatorKey struct {
	Address string   `json:"address"`
	PubKey  typedKey `json:"pub_key"`
	PrivKey typedKey `json:"priv_key"`
}

type typedKey struct {
	Type  string `json:"type"`
	Value string `json:"value"`
}

const (
	pubKeyType  = "tendermint/PubKeyEd25519"
	privKeyType = "tendermint/PrivKeyEd25519"
)

func run(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("validatorkeygen", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	minerAddr := fs.String("miner", "", "bech32 miner address to bind this consensus key to (required)")
	home := fs.String("home", defaultHome(), "the node's home directory; its config/priv_validator_key.json is the key registered")
	keyFile := fs.String("key-file", "", "priv_validator_key.json to read instead of <home>/config/priv_validator_key.json")
	newKey := fs.Bool("new-key", false, "generate a fresh consensus key instead of using the node's; it must be installed on the node before registering")
	existingPrivKeyB64 := fs.String("existing-priv-key-b64", "", "base64-encoded ed25519 private key (64 bytes) to use instead of a key file -- e.g. priv_validator_key.json's priv_key.value")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if *minerAddr == "" {
		return errors.New("--miner is required")
	}
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	if *newKey && set["existing-priv-key-b64"] {
		return errors.New("--new-key and --existing-priv-key-b64 can't be used together")
	}
	if (*newKey || set["existing-priv-key-b64"]) && (set["key-file"] || set["home"]) {
		return errors.New("--home and --key-file only choose the key file to read; drop them with --new-key or --existing-priv-key-b64")
	}

	var priv ed25519.PrivateKey
	switch {
	case *newKey:
		_, generated, err := ed25519.GenerateKey(nil)
		if err != nil {
			return fmt.Errorf("generating key: %w", err)
		}
		priv = generated
	case set["existing-priv-key-b64"]:
		decoded, err := decodePrivKey(*existingPrivKeyB64)
		if err != nil {
			return fmt.Errorf("--existing-priv-key-b64: %w", err)
		}
		priv = decoded
		fmt.Fprintln(out, "Using the provided consensus private key.")
	default:
		path := *keyFile
		if path == "" {
			path = filepath.Join(*home, "config", "priv_validator_key.json")
		}
		read, err := readKeyFile(path)
		if err != nil {
			return fmt.Errorf("%w\n(run this on the validator node, or point --home / --key-file at its priv_validator_key.json; use --new-key only for a key you'll install on the node)", err)
		}
		priv = read
		fmt.Fprintf(out, "Using this node's consensus key from %s.\n", path)
	}

	pub := priv.Public().(ed25519.PublicKey)
	signature := ed25519.Sign(priv, []byte(*minerAddr))

	fmt.Fprintf(out, "Consensus pubkey (hex):  %s\n", hex.EncodeToString(pub))
	fmt.Fprintf(out, "Consensus address:       %s\n", cometed25519.PubKey(pub).Address())

	if *newKey {
		bz, err := json.MarshalIndent(keyFileFor(priv), "", "  ")
		if err != nil {
			return err
		}
		fmt.Fprintln(out, "\nThis is a NEW key: the node doesn't have it. Before registering, install it")
		fmt.Fprintln(out, "as the node's config/priv_validator_key.json (keep the node's")
		fmt.Fprintln(out, "priv_validator_state.json) and restart the node. Registered without that,")
		fmt.Fprintln(out, "the miner becomes a validator nothing signs for. Keep this secret:")
		fmt.Fprintln(out, string(bz))
	}

	homeFlag := ""
	if !*newKey && !set["existing-priv-key-b64"] && !set["key-file"] {
		homeFlag = " --home " + *home
	}
	fmt.Fprintln(out, "\nRegister with:")
	fmt.Fprintf(out,
		"aetherd tx pow register-validator-pubkey %s %s --from %s --chain-id aether-testnet-1 --keyring-backend test --fees 0aeth -y%s\n",
		hex.EncodeToString(pub), hex.EncodeToString(signature), *minerAddr, homeFlag,
	)
	return nil
}

func defaultHome() string {
	dir, err := os.UserHomeDir()
	if err != nil {
		return ".aether"
	}
	return filepath.Join(dir, ".aether")
}

func decodePrivKey(b64 string) (ed25519.PrivateKey, error) {
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(b64))
	if err != nil {
		return nil, fmt.Errorf("decoding base64: %w", err)
	}
	if len(decoded) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("decoded key is %d bytes, expected %d (ed25519 private key)", len(decoded), ed25519.PrivateKeySize)
	}
	return ed25519.PrivateKey(decoded), nil
}

// readKeyFile reads an ed25519 private key from a priv_validator_key.json
// and checks it against the file's own pub_key and address, so a
// hand-edited file that doesn't hang together is refused rather than
// registered.
func readKeyFile(path string) (ed25519.PrivateKey, error) {
	bz, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading the node's consensus key: %w", err)
	}
	var f privValidatorKey
	if err := json.Unmarshal(bz, &f); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	if f.PrivKey.Type != privKeyType {
		return nil, fmt.Errorf("%s holds a %q key; consensus keys here are %s", path, f.PrivKey.Type, privKeyType)
	}
	priv, err := decodePrivKey(f.PrivKey.Value)
	if err != nil {
		return nil, fmt.Errorf("%s priv_key: %w", path, err)
	}
	want := keyFileFor(priv)
	if f.PubKey.Value != want.PubKey.Value || !strings.EqualFold(f.Address, want.Address) {
		return nil, fmt.Errorf("%s is inconsistent: its pub_key or address doesn't match its priv_key", path)
	}
	return priv, nil
}

func keyFileFor(priv ed25519.PrivateKey) privValidatorKey {
	pub := priv.Public().(ed25519.PublicKey)
	return privValidatorKey{
		Address: cometed25519.PubKey(pub).Address().String(),
		PubKey:  typedKey{Type: pubKeyType, Value: base64.StdEncoding.EncodeToString(pub)},
		PrivKey: typedKey{Type: privKeyType, Value: base64.StdEncoding.EncodeToString(priv)},
	}
}
