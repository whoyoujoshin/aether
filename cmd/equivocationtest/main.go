// cmd/equivocationtest/main.go
package main

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"time"

	cometed25519 "github.com/cometbft/cometbft/crypto/ed25519"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	"github.com/cometbft/cometbft/privval"
	cometrpchttp "github.com/cometbft/cometbft/rpc/client/http"
	"github.com/cometbft/cometbft/types"
)

func main() {
	keyFile := flag.String("key-file", "", "path to the validator's real priv_validator_key.json (required)")
	rpcAddr := flag.String("rpc", "http://127.0.0.1:26657", "CometBFT RPC address to broadcast evidence to")
	chainID := flag.String("chain-id", "aether-testnet-1", "chain ID (must match the running chain)")
	height := flag.Int64("height", 0, "height to construct conflicting votes at (required)")
	votingPower := flag.Int64("voting-power", 1000000, "voting power to assign this validator in the constructed evidence")
	blockTimeStr := flag.String("block-time", "", "the REAL timestamp of the block at --height, RFC3339Nano format (required)")
	node1PubKeyB64 := flag.String("node1-pubkey-b64", "", "base64 consensus pubkey of the OTHER validator on chain (required)")
	node1Power := flag.Int64("node1-power", 1000000000000, "voting power of the other validator")
	flag.Parse()

	if *keyFile == "" || *height == 0 {
		fmt.Fprintln(os.Stderr, "error: --key-file and --height are required")
		os.Exit(1)
	}
	if *blockTimeStr == "" {
		fmt.Fprintln(os.Stderr, "error: --block-time is required")
		os.Exit(1)
	}
	if *node1PubKeyB64 == "" {
		fmt.Fprintln(os.Stderr, "error: --node1-pubkey-b64 is required")
		os.Exit(1)
	}
	now, err := time.Parse(time.RFC3339Nano, *blockTimeStr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error parsing --block-time: %v\n", err)
		os.Exit(1)
	}

	stateFileA := os.TempDir() + "/equivocation-test-state-a.json"
	stateFileB := os.TempDir() + "/equivocation-test-state-b.json"
	os.Remove(stateFileA)
	os.Remove(stateFileB)
	pvA := privval.LoadFilePVEmptyState(*keyFile, stateFileA)
	pvB := privval.LoadFilePVEmptyState(*keyFile, stateFileB)

	pubKey, err := pvA.GetPubKey()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error getting pubkey: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Loaded real validator key. Consensus address: %s\n", pubKey.Address().String())

	blockIDA := types.BlockID{
		Hash:          make([]byte, 32),
		PartSetHeader: types.PartSetHeader{Total: 1, Hash: make([]byte, 32)},
	}
	blockIDA.Hash[0] = 0xAA

	blockIDB := types.BlockID{
		Hash:          make([]byte, 32),
		PartSetHeader: types.PartSetHeader{Total: 1, Hash: make([]byte, 32)},
	}
	blockIDB.Hash[0] = 0xBB

	voteA, err := types.MakeVote(pvA, *chainID, 0, *height, 0, cmtproto.PrecommitType, blockIDA, now)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error signing vote A: %v\n", err)
		os.Exit(1)
	}
	voteB, err := types.MakeVote(pvB, *chainID, 0, *height, 0, cmtproto.PrecommitType, blockIDB, now)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error signing vote B: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Constructed two independently-signed, genuinely conflicting votes at height %d, round 0:\n", *height)
	fmt.Printf("  Vote A block hash: %s\n", hex.EncodeToString(blockIDA.Hash))
	fmt.Printf("  Vote B block hash: %s\n", hex.EncodeToString(blockIDB.Hash))

	node1PubKeyBytes, err := base64.StdEncoding.DecodeString(*node1PubKeyB64)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error decoding --node1-pubkey-b64: %v\n", err)
		os.Exit(1)
	}
	node1PubKey := cometed25519.PubKey(node1PubKeyBytes)

	valSet := types.NewValidatorSet([]*types.Validator{
		types.NewValidator(pubKey, *votingPower),
		types.NewValidator(node1PubKey, *node1Power),
	})

	evidence, err := types.NewDuplicateVoteEvidence(voteA, voteB, now, valSet)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error constructing duplicate vote evidence: %v\n", err)
		os.Exit(1)
	}

	if err := evidence.ValidateBasic(); err != nil {
		fmt.Fprintf(os.Stderr, "error: constructed evidence fails ValidateBasic: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("Evidence passes ValidateBasic() -- confirmed structurally valid, real equivocation evidence.")

	client, err := cometrpchttp.New(*rpcAddr, "/websocket")
	if err != nil {
		fmt.Fprintf(os.Stderr, "error creating RPC client: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("\nBroadcasting evidence to %s ...\n", *rpcAddr)
	result, err := client.BroadcastEvidence(context.Background(), evidence)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error broadcasting evidence: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Broadcast result: hash=%s\n", hex.EncodeToString(result.Hash))
	fmt.Println("\nWatch both nodes' logs for the pow module's equivocation ban/burn log line.")
}