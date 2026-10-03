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
	"errors"
	"fmt"
	"math/big"
	"os"
	"time"

	"flag"

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

// tip is the chain state a header is mined against.
type tip struct {
	height     int64
	hash       []byte
	difficulty uint64
	recency    int64 // RecencyWindowK: how far behind the tip a header may be
}

func readTip(rpcClient *cometrpchttp.HTTP, queryClient pow.QueryClient) (tip, error) {
	status, err := rpcClient.Status(context.Background())
	if err != nil {
		return tip{}, fmt.Errorf("querying chain status: %w", err)
	}
	height := status.SyncInfo.LatestBlockHeight
	block, err := rpcClient.Block(context.Background(), &height)
	if err != nil {
		return tip{}, fmt.Errorf("querying block at height %d: %w", height, err)
	}
	diffResp, err := queryClient.Difficulty(context.Background(), &pow.QueryDifficultyRequest{})
	if err != nil {
		return tip{}, fmt.Errorf("querying current difficulty: %w", err)
	}
	var difficulty uint64
	fmt.Sscanf(diffResp.Difficulty, "%d", &difficulty)
	recency := int64(60)
	if params, err := queryClient.Params(context.Background(), &pow.QueryParamsRequest{}); err == nil && params.RecencyWindowK > 0 {
		recency = params.RecencyWindowK
	}
	return tip{height: height, hash: block.BlockID.Hash.Bytes(), difficulty: difficulty, recency: recency}, nil
}

// refreshEvery is how many nonces mineOne tries between looks at the chain
// tip: about 20 seconds at the ~4,800 hashes/s x/pow is calibrated on.
const refreshEvery = 100_000

// mineOne mines a valid nonce against real chain state and returns the
// header and the height it was mined at.
//
// The chain refuses a header more than RecencyWindowK blocks behind its
// tip (about 7 minutes at 60 blocks), so mineOne checks the tip every
// refreshEvery nonces and starts a fresh header once the current one is
// half that window old, or when difficulty has dropped below the
// header's. Mining is memoryless, so a fresh header loses nothing; at a
// high difficulty, mining one header for the whole round would mostly
// produce shares the chain refuses as stale.
func mineOne(rpcAddr, grpcAddr, minerStr string, minerAddr sdk.AccAddress, maxAttempts uint64) (header, int64, error) {
	rpcClient, err := cometrpchttp.New(rpcAddr, "/websocket")
	if err != nil {
		return header{}, 0, fmt.Errorf("creating RPC client: %w", err)
	}
	conn, err := grpc.NewClient(grpcAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return header{}, 0, fmt.Errorf("connecting to gRPC: %w", err)
	}
	defer conn.Close()
	queryClient := pow.NewQueryClient(conn)

	t, err := readTip(rpcClient, queryClient)
	if err != nil {
		return header{}, 0, err
	}
	newHeader := func(t tip) header {
		return header{
			Height:       uint64(t.height),
			Timestamp:    time.Now().Unix(),
			PrevHash:     t.hash,
			MerkleRoot:   []byte("merkleplaceholder000000000000000000000000000000"[:32]),
			Difficulty:   t.difficulty,
			MinerAddress: minerAddr,
		}
	}
	h := newHeader(t)
	fmt.Printf("Real chain state: height=%d difficulty=%d (expected ~%d hashes on average)\n\n", t.height, t.difficulty, t.difficulty)
	start := time.Now()

	for attempts := uint64(1); attempts <= maxAttempts; attempts++ {
		if verify(h) {
			fmt.Printf("\nFound valid nonce: %d for height %d (%d attempts this round, %s)\n\n", h.Nonce, h.Height, attempts, time.Since(start).Round(time.Millisecond))
			return h, int64(h.Height), nil
		}
		h.Nonce++
		if attempts%refreshEvery != 0 {
			continue
		}
		next, err := readTip(rpcClient, queryClient)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  ...%d attempts; can't read the chain (%v), still mining height %d\n", attempts, err, h.Height)
			continue
		}
		switch {
		case next.height-int64(h.Height) >= next.recency/2:
			fmt.Printf("  ...%d attempts; height %d is %d blocks old, fresh header at %d (difficulty %d)\n", attempts, h.Height, next.height-int64(h.Height), next.height, next.difficulty)
			h = newHeader(next)
		case next.difficulty < h.Difficulty:
			fmt.Printf("  ...%d attempts; difficulty fell to %d, fresh header at %d\n", attempts, next.difficulty, next.height)
			h = newHeader(next)
		default:
			fmt.Printf("  ...%d attempts (%s elapsed)\n", attempts, time.Since(start).Round(time.Millisecond))
		}
	}
	return header{}, 0, fmt.Errorf("%w within %d attempts", errNoNonce, maxAttempts)
}

var errNoNonce = errors.New("no valid nonce found")

// ML-DSA-44 signatures need more than the SDK's 200,000 default.
const submitGas = 400_000

// feesFlag renders fees for aetherd's --fees, which needs a value even
// when they're zero (sdk.Coins drops zero amounts).
func feesFlag(fees sdk.Coins) string {
	if fees.IsZero() {
		return "0uaeth"
	}
	return fees.String()
}

// submitAndConfirm builds, signs, and broadcasts a real MsgSubmitPoW via
// the wallet library, then polls until it's genuinely included in a
// block (not just accepted at broadcast) -- the same discipline this
// project has used throughout for every real transaction.
func submitAndConfirm(wal *wallet.Wallet, client *wallet.Client, fromKey, minerStr, chainID string, fees sdk.Coins, h header) error {
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
		GasLimit:      submitGas,
		Fees:          fees,
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
	feesStr := flag.String("fees", "0uaeth", "fee per submission; a node with --minimum-gas-prices needs at least gas x that price (400000 gas at 0.0001uaeth is 40uaeth)")
	flag.Parse()

	fees, err := sdk.ParseCoinsNormalized(*feesStr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: invalid --fees %q: %v\n", *feesStr, err)
		os.Exit(1)
	}

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
			if !*loop {
				os.Exit(1)
			}
			// A miner that stops costs its validator the seat at the next
			// epoch boundary, so in --loop nothing here is fatal: an
			// unreachable node or an unlucky round just starts another.
			if !errors.Is(err, errNoNonce) {
				time.Sleep(10 * time.Second)
			}
			round++
			continue
		}

		if !*autoSubmit {
			fmt.Println("Submit with (note: this submission must be broadcast within the chain's")
			fmt.Println("RecencyWindowK blocks of the height below, or it will be rejected as stale):")
			fmt.Printf(
				"aetherd tx pow submit %d %d %s %s %d %d --from %s --chain-id %s --keyring-backend test --fees %s --gas %d -y\n",
				h.Height, h.Timestamp, hex.EncodeToString(h.PrevHash), hex.EncodeToString(h.MerkleRoot), h.Nonce, h.Difficulty, *minerStr, *chainID,
				feesFlag(fees), submitGas,
			)
			break
		}

		if err := submitAndConfirm(wal, client, *fromKey, *minerStr, *chainID, fees, h); err != nil {
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
