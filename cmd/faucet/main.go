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
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os/exec"
	"regexp"
	"sync"
	"time"

	"github.com/whoyoujoshin/aether/app"
)

func init() {
	app.SetAddressPrefixes()
}

var bech32Pattern = regexp.MustCompile(`^aether1[a-z0-9]{38,90}$`)

type faucetServer struct {
	mu          sync.Mutex // serializes real sends to avoid account-sequence races
	lastRequest map[string]time.Time
	cooldownMu  sync.Mutex
	fromKey     string
	chainID     string
	amountUaeth int64
	keyringFlag string
	cooldown    time.Duration
}

type requestBody struct {
	Address string `json:"address"`
}

type responseBody struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	TxHash  string `json:"tx_hash,omitempty"`
}

func (f *faucetServer) checkAndUpdateCooldown(address string) (bool, time.Duration) {
	f.cooldownMu.Lock()
	defer f.cooldownMu.Unlock()

	last, seen := f.lastRequest[address]
	now := time.Now()
	if seen {
		elapsed := now.Sub(last)
		if elapsed < f.cooldown {
			return false, f.cooldown - elapsed
		}
	}
	f.lastRequest[address] = now
	return true, 0
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

	ok, wait := f.checkAndUpdateCooldown(req.Address)
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
		log.Printf("faucet send failed for %s: %v", req.Address, err)
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(responseBody{Success: false, Message: "send failed, please try again later"})
		return
	}

	log.Printf("faucet sent %d uaeth to %s, tx %s", f.amountUaeth, req.Address, txHash)
	json.NewEncoder(w).Encode(responseBody{Success: true, Message: "sent", TxHash: txHash})
}

func (f *faucetServer) sendCoins(address string) (string, error) {
	amountStr := fmt.Sprintf("%duaeth", f.amountUaeth)

	cmd := exec.Command("aetherd", "tx", "bank", "send",
	f.fromKey, address, amountStr,
	"--chain-id", f.chainID,
	"--keyring-backend", f.keyringFlag,
	"--fees", "0uaeth",
	"--gas", "400000", // ML-DSA-44 signatures need more than the SDK's default 200,000
	"--broadcast-mode", "sync",
	"--output", "json",
	"-y",
)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("aetherd tx failed: %w (stderr: %s)", err, stderr.String())
	}

	var result struct {
		TxHash string `json:"txhash"`
		Code   int    `json:"code"`
		RawLog string `json:"raw_log"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		return "", fmt.Errorf("failed to parse aetherd output: %w (output: %s)", err, stdout.String())
	}
	if result.Code != 0 {
		return "", fmt.Errorf("transaction rejected: %s", result.RawLog)
	}

	return result.TxHash, nil
}

func main() {
	fromKey := flag.String("from", "faucet", "keyring name of the funded faucet account")
	chainID := flag.String("chain-id", "aether-testnet-1", "chain ID to broadcast against")
	amount := flag.Int64("amount", 1_000_000, "amount to dispense per request, in uaeth")
	keyringBackend := flag.String("keyring-backend", "test", "keyring backend the faucet account is stored in")
	cooldownMinutes := flag.Int("cooldown-minutes", 60, "minutes an address must wait between requests")
	port := flag.String("port", "8080", "HTTP port to listen on")
	flag.Parse()

	server := &faucetServer{
		lastRequest: make(map[string]time.Time),
		fromKey:     *fromKey,
		chainID:     *chainID,
		amountUaeth: *amount,
		keyringFlag: *keyringBackend,
		cooldown:    time.Duration(*cooldownMinutes) * time.Minute,
	}

	http.HandleFunc("/request", server.handleRequest)

	addr := ":" + *port
	log.Printf("Aether faucet listening on %s (dispensing %d uaeth per request, %d-minute cooldown, from key %q on chain %q)",
		addr, *amount, *cooldownMinutes, *fromKey, *chainID)
	log.Fatal(http.ListenAndServe(addr, nil))
}
