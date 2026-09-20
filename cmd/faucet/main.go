// cmd/faucet/main.go
//
// A minimal, standalone HTTP faucet for the Aether testnet. Dispenses
// a small, fixed amount of testnet aeth to any valid address, once
// per address per cooldown period. Deliberately shells out to the
// real, already-tested `aetherd` CLI to construct/sign/broadcast the
// actual bank.MsgSend transaction, rather than reimplementing tx
// construction and keyring/broadcast logic from scratch in this
// standalone program.
//
// Usage:
//   go run ./cmd/faucet --from faucet --chain-id aether-testnet-1 --amount 1000000
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"regexp"
	"sync"
	"time"

	"cosmossdk.io/math"
	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/whoyoujoshin/aether/app"
	"github.com/whoyoujoshin/aether/crypto/mldsa"
	"github.com/whoyoujoshin/aether/wallet"
)

func init() {
	app.SetAddressPrefixes()
}

var bech32Pattern = regexp.MustCompile(`^aether1[a-z0-9]{38,90}$`)

type faucetServer struct {
	mu          sync.Mutex // serializes real sends AND protects the in-memory sequence counter
	lastRequest map[string]time.Time
	cooldownMu  sync.Mutex
	wal         *wallet.Wallet
	client      *wallet.Client
	fromKey     string
	fromAddr    string
	chainID     string
	amountUaeth int64
	cooldown    time.Duration

	accountNumber uint64
	sequence      uint64 // tracked in-memory; incremented locally after each successful broadcast, never re-queried from the chain per-request -- avoids the exact stale-sequence race a real, independent stress test found: re-querying the chain for sequence on every send fails whenever two sends happen within the same ~60s block window, since sequence only updates once a tx is actually included in a block, not merely broadcast.

	confirmTimeout      time.Duration
	confirmPollInterval time.Duration
}

type requestBody struct {
	Address string `json:"address"`
}

type responseBody struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	TxHash  string `json:"tx_hash,omitempty"`
}

func (f *faucetServer) checkAndUpdateCooldown(address string) (bool, time.Duration, time.Time) {
	f.cooldownMu.Lock()
	defer f.cooldownMu.Unlock()

	last, seen := f.lastRequest[address]
	now := time.Now()
	if seen {
		elapsed := now.Sub(last)
		if elapsed < f.cooldown {
			return false, f.cooldown - elapsed, time.Time{}
		}
	}
	f.lastRequest[address] = now
	return true, 0, now
}

// releaseCooldownIfUnchanged undoes checkAndUpdateCooldown's recording
// when the send it was guarding turns out to have failed -- a real,
// live-flagged gap (Section 3 item 9): recording the cooldown before
// confirming the send succeeds means any failure (network error,
// on-chain rejection, insufficient faucet balance) burns the address's
// full cooldown for nothing. The early recording itself is deliberate
// and stays -- it closes a real race where two concurrent requests for
// the same address could both pass the check while the first send is
// still in flight -- so this only rolls it back, and only if nothing
// else has touched that address's record since (recordedAt still
// matches exactly), so a genuinely newer request's own record is never
// clobbered.
func (f *faucetServer) releaseCooldownIfUnchanged(address string, recordedAt time.Time) {
	f.cooldownMu.Lock()
	defer f.cooldownMu.Unlock()

	if last, seen := f.lastRequest[address]; seen && last.Equal(recordedAt) {
		delete(f.lastRequest, address)
	}
}

func (f *faucetServer) handleRequest(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		json.NewEncoder(w).Encode(responseBody{Success: false, Message: "use POST"})
		return
	}

	var req requestBody
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(responseBody{Success: false, Message: "invalid request body"})
		return
	}

	if !bech32Pattern.MatchString(req.Address) {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(responseBody{Success: false, Message: "invalid address format"})
		return
	}

	ok, wait, recordedAt := f.checkAndUpdateCooldown(req.Address)
	if !ok {
		w.WriteHeader(http.StatusTooManyRequests)
		json.NewEncoder(w).Encode(responseBody{
			Success: false,
			Message: fmt.Sprintf("please wait %s before requesting again", wait.Round(time.Second)),
		})
		return
	}

	// Serialize the actual send -- avoids two concurrent requests
	// racing on the faucet account's real sequence number.
	f.mu.Lock()
	txHash, err := f.sendCoins(req.Address)
	f.mu.Unlock()

	if err != nil {
		f.releaseCooldownIfUnchanged(req.Address, recordedAt)
		log.Printf("faucet send failed for %s: %v", req.Address, err)
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(responseBody{Success: false, Message: "send failed, please try again later"})
		return
	}

	// BroadcastTx's SYNC mode (the only real option left -- COMMIT/BLOCK
	// mode was removed in SDK v0.47+) only confirms the tx passed
	// CheckTx and entered the mempool, not that it actually succeeded.
	// bank's own Send handler, which enforces the real balance check,
	// only runs at DeliverTx, never at CheckTx -- so a tx that passes
	// CheckTx here can still genuinely fail once included in a block
	// (e.g. the faucet account's real balance running low). Per this
	// project's own standing rule ("never trust broadcast acceptance as
	// proof of anything"), confirm the real on-chain result before ever
	// telling the caller "sent".
	detail, confirmErr := f.confirmTxOnChain(txHash)
	switch {
	case confirmErr != nil:
		// Genuinely inconclusive within the timeout -- deliberately
		// does NOT release the cooldown: the tx may still land later,
		// and releasing here risks letting the same address claim
		// twice for what might turn out to be one real send.
		log.Printf("faucet could not confirm tx %s for %s within %s: %v", txHash, req.Address, f.confirmTimeout, confirmErr)
		w.WriteHeader(http.StatusAccepted)
		json.NewEncoder(w).Encode(responseBody{
			Success: false,
			Message: fmt.Sprintf("broadcast accepted but not yet confirmed on-chain; check tx %s later", txHash),
			TxHash:  txHash,
		})
		return
	case detail.Code != 0:
		// Passed CheckTx, failed for real at DeliverTx -- a genuine
		// send failure, so the cooldown is released the same as any
		// other failure.
		f.releaseCooldownIfUnchanged(req.Address, recordedAt)
		log.Printf("faucet tx %s for %s failed on-chain: %s", txHash, req.Address, detail.RawLog)
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(responseBody{Success: false, Message: "send failed on-chain, please try again later", TxHash: txHash})
		return
	}

	log.Printf("faucet sent %d uaeth to %s, tx %s (confirmed at height %d)", f.amountUaeth, req.Address, txHash, detail.Height)
	json.NewEncoder(w).Encode(responseBody{Success: true, Message: "sent", TxHash: txHash})
}

// confirmTxOnChain polls GetTransactionByHash until the transaction is
// actually found (meaning it was included in a block and its real
// DeliverTx result is known) or confirmTimeout elapses. See
// handleRequest's comment for why this exists: SYNC-mode broadcast
// acceptance alone is not proof the send genuinely succeeded.
func (f *faucetServer) confirmTxOnChain(txHash string) (*wallet.TransactionDetail, error) {
	deadline := time.Now().Add(f.confirmTimeout)
	var lastErr error
	for {
		detail, err := f.client.GetTransactionByHash(txHash)
		if err == nil {
			return detail, nil
		}
		lastErr = err
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("not found after %s: %w", f.confirmTimeout, lastErr)
		}
		time.Sleep(f.confirmPollInterval)
	}
}

func (f *faucetServer) sendCoins(address string) (string, error) {
	amount := sdk.NewCoins(sdk.NewCoin("uaeth", math.NewInt(f.amountUaeth)))

	signed, err := f.wal.BuildAndSignSendTx(f.fromKey, f.fromAddr, address, amount, wallet.TxParams{
		ChainID:       f.chainID,
		AccountNumber: f.accountNumber,
		Sequence:      f.sequence,
		GasLimit:      400_000,
		Fees:          sdk.NewCoins(sdk.NewCoin("uaeth", math.NewInt(0))),
	})
	if err != nil {
		return "", fmt.Errorf("failed to build/sign tx: %w", err)
	}

	result, err := f.client.BroadcastTx(signed)
	if err != nil {
		return "", fmt.Errorf("failed to broadcast: %w", err)
	}
	if result.Code != 0 {
		return "", fmt.Errorf("transaction rejected: %s", result.RawLog)
	}

	f.sequence++ // only advance our local counter after a genuinely successful broadcast
	return result.TxHash, nil
}

func main() {
	fromKey := flag.String("from", "faucet", "keyring name of the funded faucet account")
	chainID := flag.String("chain-id", "aether-testnet-1", "chain ID to broadcast against")
	amount := flag.Int64("amount", 1_000_000, "amount to dispense per request, in uaeth")
	keyringBackend := flag.String("keyring-backend", "test", "keyring backend the faucet account is stored in")
	keyringDir := flag.String("keyring-dir", "", "keyring storage directory (defaults to the aetherd default home)")
	grpcEndpoint := flag.String("grpc", "localhost:9090", "node gRPC endpoint")
	cooldownMinutes := flag.Int("cooldown-minutes", 60, "minutes an address must wait between requests")
	port := flag.String("port", "8080", "HTTP port to listen on")
	confirmTimeoutSeconds := flag.Int("confirm-timeout-seconds", 90, "how long to wait for a broadcast tx to actually land on-chain before giving up (comfortably more than one block interval)")
	confirmPollSeconds := flag.Int("confirm-poll-seconds", 3, "how often to poll for on-chain confirmation while waiting")
	flag.Parse()

	dir := *keyringDir
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			log.Fatalf("failed to determine home directory: %v", err)
		}
		dir = home + "/.aether"
	}

	registry := codectypes.NewInterfaceRegistry()
	mldsa.RegisterInterfaces(registry)
	cdc := codec.NewProtoCodec(registry)

	wal, err := wallet.NewWallet("aetherd", *keyringBackend, dir, cdc)
	if err != nil {
		log.Fatalf("failed to open wallet: %v", err)
	}

	account, err := wal.GetAccount(*fromKey)
	if err != nil {
		log.Fatalf("failed to find faucet account %q in keyring: %v", *fromKey, err)
	}

	client, err := wallet.NewClient(*grpcEndpoint)
	if err != nil {
		log.Fatalf("failed to connect to gRPC endpoint: %v", err)
	}

	accountNumber, sequence, err := client.GetAccountInfo(account.Address)
	if err != nil {
		log.Fatalf("failed to fetch initial account info for %s: %v", account.Address, err)
	}

	server := &faucetServer{
		lastRequest:         make(map[string]time.Time),
		wal:                 wal,
		client:              client,
		fromKey:             *fromKey,
		fromAddr:            account.Address,
		chainID:             *chainID,
		amountUaeth:         *amount,
		cooldown:            time.Duration(*cooldownMinutes) * time.Minute,
		accountNumber:       accountNumber,
		sequence:            sequence,
		confirmTimeout:      time.Duration(*confirmTimeoutSeconds) * time.Second,
		confirmPollInterval: time.Duration(*confirmPollSeconds) * time.Second,
	}

		// Deliberately use our own dedicated mux, never the shared global
	// http.DefaultServeMux -- a real, severe issue found live: some
	// transitive dependency in the Cosmos SDK/gRPC stack silently
	// registers Go's pprof debug handlers on the global default mux
	// as an import side effect, with no explicit pprof import
	// anywhere in this project's own code. Passing nil to
	// ListenAndServe (which falls back to that shared global mux)
	// meant this internet-facing faucet -- actively holding a real
	// private key in memory to sign transactions -- was unknowingly
	// serving a full heap-dump endpoint to the entire public
	// internet. A dedicated mux only ever serves handlers this file
	// explicitly registers.
	mux := http.NewServeMux()
	mux.HandleFunc("/request", server.handleRequest)

	addr := ":" + *port
	log.Printf("Aether faucet listening on %s (dispensing %d uaeth per request, %d-minute cooldown, from %s on chain %q, starting sequence %d)",
		addr, *amount, *cooldownMinutes, account.Address, *chainID, sequence)
	log.Fatal(http.ListenAndServe(addr, mux))
}