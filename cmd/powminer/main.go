// cmd/powminer/main.go
//
// Brute-force PoW miner that queries REAL chain state before mining, since
// Phase 3's ancestor validation (see aether-randomness-beacon-design.md and
// the SubmitPoW hardening pass) rejects any submission whose Height/PrevHash
// don't match a real, recent block the chain actually produced. The old
// version of this tool used placeholder zero-hashes and an arbitrary
// height -- every submission it produces now would be rejected with
// ErrUnknownAncestor.
//
// Usage:
//   go run ./cmd/powminer \
//     --miner cosmos14ky92qc9vgdlgjm2m870802t8l88vmh8fw3gmq \
//     --rpc http://127.0.0.1:26657 \
//     --grpc localhost:9090
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math/big"
	"os"
	"time"

	"flag"

	cometrpchttp "github.com/cometbft/cometbft/rpc/client/http"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/whoyoujoshin/aether/x/pow"
)

// header mirrors pow.MiningHeader's field layout exactly, matching
// x/pow/keeper.go's headerToBytes byte-for-byte.
type header struct {
	Height       uint64
	Timestamp    int64
	PrevHash     []byte
	MerkleRoot   []byte
	Nonce        uint64
	Difficulty   uint64
	MinerAddress sdk.AccAddress
}

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
	minerStr := flag.String("miner", "", "bech32 miner address (required)")
	rpcAddr := flag.String("rpc", "http://127.0.0.1:26657", "CometBFT RPC address, for querying real chain height/hash")
	grpcAddr := flag.String("grpc", "localhost:9090", "gRPC address, for querying real current difficulty via x/pow's query service")
	chainID := flag.String("chain-id", "aether-testnet-1", "chain ID, printed in the submit command")
	maxAttempts := flag.Uint64("max-attempts", 10_000_000, "give up after this many nonce attempts")
	flag.Parse()

	if *minerStr == "" {
		fmt.Fprintln(os.Stderr, "error: --miner is required")
		os.Exit(1)
	}

	minerAddr, err := sdk.AccAddressFromBech32(*minerStr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: invalid --miner address: %v\n", err)
		os.Exit(1)
	}

	// 1. Query the real, current chain height and that block's real hash.
	rpcClient, err := cometrpchttp.New(*rpcAddr, "/websocket")
	if err != nil {
		fmt.Fprintf(os.Stderr, "error creating RPC client: %v\n", err)
		os.Exit(1)
	}

	status, err := rpcClient.Status(context.Background())
	if err != nil {
		fmt.Fprintf(os.Stderr, "error querying chain status: %v\n", err)
		os.Exit(1)
	}
	currentHeight := status.SyncInfo.LatestBlockHeight

	block, err := rpcClient.Block(context.Background(), &currentHeight)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error querying block at height %d: %v\n", currentHeight, err)
		os.Exit(1)
	}
	realBlockHash := block.BlockID.Hash

	fmt.Printf("Real chain state: height=%d block_hash=%s\n", currentHeight, realBlockHash.String())

	// 2. Query the real current difficulty via x/pow's gRPC query service.
	conn, err := grpc.NewClient(*grpcAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		fmt.Fprintf(os.Stderr, "error connecting to gRPC: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close()

	queryClient := pow.NewQueryClient(conn)
	diffResp, err := queryClient.Difficulty(context.Background(), &pow.QueryDifficultyRequest{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "error querying current difficulty: %v\n", err)
		os.Exit(1)
	}

	var difficulty uint64
	fmt.Sscanf(diffResp.Difficulty, "%d", &difficulty)
	fmt.Printf("Real current difficulty: %d\n\n", difficulty)

	// 3. Mine against the REAL height/hash/difficulty.
	timestamp := time.Now().Unix()
	h := header{
		Height:       uint64(currentHeight),
		Timestamp:    timestamp,
		PrevHash:     realBlockHash.Bytes(),
		MerkleRoot:   []byte("merkleplaceholder000000000000000000000000000000"[:32]),
		Difficulty:   difficulty,
		MinerAddress: minerAddr,
	}

	fmt.Printf("Mining at difficulty %d (expected ~%d hashes on average)...\n", difficulty, difficulty)
	start := time.Now()

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
		fmt.Fprintf(os.Stderr, "no valid nonce found within %d attempts\n", *maxAttempts)
		os.Exit(1)
	}

	elapsed := time.Since(start)
	fmt.Printf("\nFound valid nonce: %d (in %d attempts, %s)\n\n", nonce, nonce+1, elapsed.Round(time.Millisecond))

	fmt.Println("Submit with (note: this submission must be broadcast within the chain's")
	fmt.Println("RecencyWindowK blocks of the height below, or it will be rejected as stale):")
	fmt.Printf(
		"aetherd tx pow submit %d %d %s %s %d %d --from %s --chain-id %s --keyring-backend test --fees 0aeth -y\n",
		currentHeight, timestamp, hex.EncodeToString(realBlockHash.Bytes()), hex.EncodeToString(h.MerkleRoot), nonce, difficulty, *minerStr, *chainID,
	)
}
