// cmd/walletapi/main.go
//
// A local-only HTTP JSON API wrapping the wallet library (wallet/),
// for a locally-running desktop wallet frontend ("Aether Pay
// Desktop"). Deliberately NOT meant to be exposed publicly or serve
// multiple untrusted users -- it holds a real local keyring and signs
// on the caller's behalf, which is only appropriate because the
// server and the person using it are the same, trusted machine.
// Defaults to the public testnet for chain queries/broadcasts, while
// signing always happens with keys that never leave this machine.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"

	"cosmossdk.io/math"
	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/whoyoujoshin/aether/crypto/mldsa"
	"github.com/whoyoujoshin/aether/wallet"
	"github.com/whoyoujoshin/aether/app"
)

func init() {
	app.SetAddressPrefixes()
}

var (
	keyringDir     string
	keyringBackend string
	grpcEndpoint   string
	rpcEndpoint    string
	chainID        string
	port           string
)

func defaultHomeDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".aether"
	}
	return filepath.Join(home, ".aether")
}

func newWallet() (*wallet.Wallet, error) {
	registry := codectypes.NewInterfaceRegistry()
	mldsa.RegisterInterfaces(registry)
	cdc := codec.NewProtoCodec(registry)
	return wallet.NewWallet("aetherd", keyringBackend, keyringDir, cdc)
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

func withCORS(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		h(w, r)
	}
}

// GET /api/account?name=mywallet
func handleAccount(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	if name == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("missing 'name' query parameter"))
		return
	}

	wal, err := newWallet()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	account, err := wal.GetAccount(name)
	if err != nil {
		writeError(w, http.StatusNotFound, fmt.Errorf("account %q not found: %w", name, err))
		return
	}

	client, err := wallet.NewClient(grpcEndpoint)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	defer client.Close()

	balance, err := client.GetBalance(account.Address)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("failed to fetch balance: %w", err))
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"name":    account.Name,
		"address": account.Address,
		"balance": balance.AmountOf("uaeth").String(),
	})
}

// GET /api/accounts
func handleAccounts(w http.ResponseWriter, r *http.Request) {
	wal, err := newWallet()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	accounts, err := wal.ListAccounts()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, accounts)
}

type sendRequest struct {
	From   string `json:"from"`
	To     string `json:"to"`
	Amount string `json:"amount"` // uaeth, as a plain integer string
}

// POST /api/send
func handleSend(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, fmt.Errorf("use POST"))
		return
	}

	var req sendRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid request body: %w", err))
		return
	}

	wal, err := newWallet()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	fromAccount, err := wal.GetAccount(req.From)
	if err != nil {
		writeError(w, http.StatusNotFound, fmt.Errorf("sender account %q not found: %w", req.From, err))
		return
	}

	amountInt, ok := math.NewIntFromString(req.Amount)
	if !ok {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid amount %q", req.Amount))
		return
	}
	amount := sdk.NewCoins(sdk.NewCoin("uaeth", amountInt))

	client, err := wallet.NewClient(grpcEndpoint)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	defer client.Close()

	accountNumber, sequence, err := client.GetAccountInfo(fromAccount.Address)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("failed to fetch sender account info: %w", err))
		return
	}

	signed, err := wal.BuildAndSignSendTx(req.From, fromAccount.Address, req.To, amount, wallet.TxParams{
		ChainID:       chainID,
		AccountNumber: accountNumber,
		Sequence:      sequence,
		GasLimit:      400_000,
		Fees:          sdk.NewCoins(sdk.NewCoin("uaeth", math.NewInt(0))),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("failed to build/sign transaction: %w", err))
		return
	}

	result, err := client.BroadcastTx(signed)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("failed to broadcast: %w", err))
		return
	}

	if result.Code != 0 {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"success": false,
			"code":    result.Code,
			"message": result.RawLog,
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"tx_hash": result.TxHash,
	})
}

// GET /api/history?address=aether1...&limit=20
func handleHistory(w http.ResponseWriter, r *http.Request) {
	address := r.URL.Query().Get("address")
	if address == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("missing 'address' query parameter"))
		return
	}

	client, err := wallet.NewClient(grpcEndpoint)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	defer client.Close()

	txs, err := client.GetTransactionHistory(address, 20)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("failed to fetch history: %w", err))
		return
	}

	writeJSON(w, http.StatusOK, txs)
}

func main() {
	flag.StringVar(&keyringDir, "keyring-dir", defaultHomeDir(), "keyring storage directory")
	flag.StringVar(&keyringBackend, "keyring-backend", "test", "keyring backend (test|file|os)")
	flag.StringVar(&grpcEndpoint, "grpc", "157.245.252.221:9090", "node gRPC endpoint (defaults to the public testnet)")
	flag.StringVar(&rpcEndpoint, "rpc", "http://157.245.252.221:26657", "node RPC endpoint (defaults to the public testnet)")
	flag.StringVar(&chainID, "chain-id", "aether-testnet-1", "chain ID")
	flag.StringVar(&port, "port", "8090", "local HTTP port to listen on")
	flag.Parse()

	http.HandleFunc("/api/account", withCORS(handleAccount))
	http.HandleFunc("/api/accounts", withCORS(handleAccounts))
	http.HandleFunc("/api/send", withCORS(handleSend))
	http.HandleFunc("/api/history", withCORS(handleHistory))

	addr := "localhost:" + port
	log.Printf("Aether wallet API listening on %s (chain %s via gRPC %s)", addr, chainID, grpcEndpoint)
	log.Fatal(http.ListenAndServe(addr, nil))
}
