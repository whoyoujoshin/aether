// cmd/powminer/main.go
//
// Brute-force PoW miner that queries REAL chain state before mining, since
// Phase 3's ancestor validation (see aether-randomness-beacon-design.md and
// the SubmitPoW hardening pass) rejects any submission whose Height/PrevHash
// don't match a real, recent block the chain actually produced.
//
// By default, prints a ready-to-run `aetherd tx pow submit` CLI command
// (the original behavior). With --auto-submit, signs and broadcasts the
// submission directly via the wallet library instead -- no CLI subprocess,
// and no risk of forgetting the ML-DSA-44 --gas 400000 requirement, since
// that's hardcoded here. With --loop (requires --auto-submit), mines and
// submits continuously, waiting for each submission to be confirmed before
// starting the next, until interrupted (Ctrl+C).
//
// Usage:
//   go run ./cmd/powminer --miner aether1... --rpc http://host:26657 --grpc host:9090
//   go run ./cmd/powminer --miner aether1... --from realminer --auto-submit --loop \
//     --rpc http://157.245.252.221:26657 --grpc 157.245.252.221:9090
package main

import (
	"context"
	"encoding/hex"
	"fmt"
	"math/big"
	"os"
	"time"

	"flag"

	"cosmossdk.io/math"
	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	cometrpchttp "github.com/cometbft/cometbft/rpc/client/http"
	"golang.org/x/crypto/scrypt"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/whoyoujoshin/aether/app"
	"github.com/whoyoujoshin/aether/crypto/mldsa"
	"github.com/whoyoujoshin/aether/wallet"
	"github.com/whoyoujoshin/aether/x/pow"
)

func init() {
	app.SetAddressPrefixes()
}

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

const (
	scryptN = 1024
	scryptR = 1
	scryptP = 1
)

func verify(h header) bool {
	if h.Difficulty == 0 {
		return false
	}
	data := headerToBytes(h)

	// Matches x/pow's keeper: scrypt(header, header, N, r, p, 32),
	// Litecoin's real parameters, not a lighter devnet-only variant.
	hash, err := scrypt.Key(data, data, scryptN, scryptR, scryptP, 32)
	if err != nil {
		return false
	}

	maxTarget := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 256), big.NewInt(1))
	difficulty := new(big.Int).SetUint64(h.Difficulty)
	target := new(big.Int).Div(maxTarget, difficulty)

	return new(big.Int).SetBytes(hash).Cmp(target) < 0
}

// mineOne queries real chain state, mines a single valid nonce against
// it, and returns the resulting header along with the real height and
// difficulty used.
func mineOne(rpcAddr, grpcAddr, minerStr string, minerAddr sdk.AccAddress, maxAttempts uint64) (header, int64, error) {
	rpcClient, err := cometrpchttp.New(rpcAddr, "/websocket")
	if err != nil {
		return header{}, 0, fmt.Errorf("creating RPC client: %w", err)
	}

	status, err := rpcClient.Status(context.Background())
	if err != nil {
		return header{}, 0, fmt.Errorf("querying chain status: %w", err)
	}
	currentHeight := status.SyncInfo.LatestBlockHeight

	block, err := rpcClient.Block(context.Background(), &currentHeight)
	if err != nil {
		return header{}, 0, fmt.Errorf("querying block at height %d: %w", currentHeight, err)
	}
	realBlockHash := block.BlockID.Hash

	fmt.Printf("Real chain state: height=%d block_hash=%s\n", currentHeight, realBlockHash.String())

	conn, err := grpc.NewClient(grpcAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return header{}, 0, fmt.Errorf("connecting to gRPC: %w", err)
	}
	defer conn.Close()

	queryClient := pow.NewQueryClient(conn)
	diffResp, err := queryClient.Difficulty(context.Background(), &pow.QueryDifficultyRequest{})
	if err != nil {
		return header{}, 0, fmt.Errorf("querying current difficulty: %w", err)
	}

	var difficulty uint64
	fmt.Sscanf(diffResp.Difficulty, "%d", &difficulty)
	fmt.Printf("Real current difficulty: %d\n\n", difficulty)

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
	for nonce = 0; nonce < maxAttempts; nonce++ {
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
		return header{}, 0, fmt.Errorf("no valid nonce found within %d attempts", maxAttempts)
	}

	fmt.Printf("\nFound valid nonce: %d (in %d attempts, %s)\n\n", nonce, nonce+1, time.Since(start).Round(time.Millisecond))
	return h, currentHeight, nil
}

// submitAndConfirm builds, signs, and broadcasts a real MsgSubmitPoW via
// the wallet library, then polls until it's genuinely included in a
// block (not just accepted at broadcast) -- the same discipline this
// project has used throughout for every real transaction.
func submitAndConfirm(wal *wallet.Wallet, client *wallet.Client, fromKey, minerStr, chainID string, h header) error {
	msg := &pow.MsgSubmitPoW{
		Miner: minerStr,
		Submission: &pow.MsgSubmitPoW_Native{
			Native: &pow.NativeSubmission{
				Height:     h.Height,
				Timestamp:  h.Timestamp,
				PrevHash:   h.PrevHash,
				MerkleRoot: h.MerkleRoot,
				Nonce:      h.Nonce,
				Difficulty: h.Difficulty,
			},
		},
	}

	accountNumber, sequence, err := client.GetAccountInfo(minerStr)
	if err != nil {
		return fmt.Errorf("fetching account info: %w", err)
	}

	signed, err := wal.BuildAndSignMsgTx(fromKey, msg, wallet.TxParams{
		ChainID:       chainID,
		AccountNumber: accountNumber,
		Sequence:      sequence,
		GasLimit:      400_000, // ML-DSA-44 signatures need more than the SDK's 200,000 default
		Fees:          sdk.NewCoins(sdk.NewCoin("uaeth", math.NewInt(0))),
	})
	if err != nil {
		return fmt.Errorf("building/signing tx: %w", err)
	}

	result, err := client.BroadcastTx(signed)
	if err != nil {
		return fmt.Errorf("broadcasting: %w", err)
	}
	if result.Code != 0 {
		return fmt.Errorf("rejected at broadcast: code %d: %s", result.Code, result.RawLog)
	}

	fmt.Printf("Broadcast accepted, tx %s -- waiting for real inclusion...\n", result.TxHash)

	// Poll for genuine on-chain confirmation, matching this project's
	// standing rule: a successful broadcast is not the same as a
	// successful, executed transaction.
	for i := 0; i < 15; i++ {
		time.Sleep(5 * time.Second)
		txs, err := client.GetTransactionHistory(minerStr, 5)
		if err != nil {
			continue
		}
		for _, tx := range txs {
			if tx.Hash == result.TxHash {
				if tx.Code == 0 {
					fmt.Printf("Confirmed at height %d: real reward minted and distributed.\n\n", tx.Height)
					return nil
				}
				return fmt.Errorf("included but failed on-chain: code %d", tx.Code)
			}
		}
	}
	return fmt.Errorf("submitted but not confirmed within the wait window -- check manually")
}

func main() {
	minerStr := flag.String("miner", "", "bech32 miner address (required)")
	rpcAddr := flag.String("rpc", "http://127.0.0.1:26657", "CometBFT RPC address, for querying real chain height/hash")
	grpcAddr := flag.String("grpc", "localhost:9090", "gRPC address, for querying real current difficulty via x/pow's query service")
	chainID := flag.String("chain-id", "aether-testnet-1", "chain ID, printed in the submit command")
	maxAttempts := flag.Uint64("max-attempts", 10_000_000, "give up after this many nonce attempts")
	autoSubmit := flag.Bool("auto-submit", false, "sign and broadcast the submission directly via the wallet library, instead of just printing a CLI command")
	fromKey := flag.String("from", "", "keyring name of the miner account (required with --auto-submit)")
	keyringBackend := flag.String("keyring-backend", "test", "keyring backend the miner account is stored in")
	loop := flag.Bool("loop", false, "keep mining and submitting continuously (requires --auto-submit); runs until interrupted")
	flag.Parse()

	if *minerStr == "" {
		fmt.Fprintln(os.Stderr, "error: --miner is required")
		os.Exit(1)
	}
	if *loop && !*autoSubmit {
		fmt.Fprintln(os.Stderr, "error: --loop requires --auto-submit")
		os.Exit(1)
	}
	if *autoSubmit && *fromKey == "" {
		fmt.Fprintln(os.Stderr, "error: --from is required with --auto-submit")
		os.Exit(1)
	}

	minerAddr, err := sdk.AccAddressFromBech32(*minerStr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: invalid --miner address: %v\n", err)
		os.Exit(1)
	}

	var wal *wallet.Wallet
	var client *wallet.Client
	if *autoSubmit {
		registry := codectypes.NewInterfaceRegistry()
		mldsa.RegisterInterfaces(registry)
		cdc := codec.NewProtoCodec(registry)
		home, err := os.UserHomeDir()
		if err != nil {
			fmt.Fprintf(os.Stderr, "error determining home directory: %v\n", err)
			os.Exit(1)
		}
		wal, err = wallet.NewWallet("aetherd", *keyringBackend, home+"/.aether", cdc)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error opening wallet: %v\n", err)
			os.Exit(1)
		}
		client, err = wallet.NewClient(*grpcAddr)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error connecting to gRPC: %v\n", err)
			os.Exit(1)
		}
		defer client.Close()
	}

	round := 1
	for {
		if *loop {
			fmt.Printf("=== Round %d ===\n", round)
		}

		h, _, err := mineOne(*rpcAddr, *grpcAddr, *minerStr, minerAddr, *maxAttempts)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}

		if !*autoSubmit {
			fmt.Println("Submit with (note: this submission must be broadcast within the chain's")
			fmt.Println("RecencyWindowK blocks of the height below, or it will be rejected as stale):")
			fmt.Printf(
				"aetherd tx pow submit %d %d %s %s %d %d --from %s --chain-id %s --keyring-backend test --fees 0uaeth --gas 400000 -y\n",
				h.Height, h.Timestamp, hex.EncodeToString(h.PrevHash), hex.EncodeToString(h.MerkleRoot), h.Nonce, h.Difficulty, *minerStr, *chainID,
			)
			break
		}

		if err := submitAndConfirm(wal, client, *fromKey, *minerStr, *chainID, h); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			if !*loop {
				os.Exit(1)
			}
			fmt.Println("Retrying next round...")
		}

		if !*loop {
			break
		}
		round++
	}
}
