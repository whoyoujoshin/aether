// cmd/explorer/main.go
//
// A read-only JSON API for Aether's block explorer frontend
// (explorer-web/). Queries real chain state via gRPC (x/pow,
// x/governance, bank) and CometBFT RPC (latest height) on every
// request -- no caching at this scale.
//
// This used to render server-side HTML directly; that moved to a real
// React frontend (see explorer-web/README.md), so this binary's only
// job now is serving JSON.
//
// Usage:
//
//	go run ./cmd/explorer --grpc localhost:9090 --rpc http://localhost:26657 --port 8081
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/whoyoujoshin/aether/app"
	"github.com/whoyoujoshin/aether/wallet"
	"github.com/whoyoujoshin/aether/x/governance"
	"github.com/whoyoujoshin/aether/x/pow"
)

func init() {
	app.SetAddressPrefixes()
}

var (
	grpcEndpoint string
	rpcEndpoint  string
)

// --- shared helpers ---

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
		w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		h(w, r)
	}
}

func fetchLatestHeight(rpcAddr string) (int64, error) {
	resp, err := http.Get(rpcAddr + "/status")
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	var status struct {
		Result struct {
			SyncInfo struct {
				LatestBlockHeight string `json:"latest_block_height"`
			} `json:"sync_info"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		return 0, err
	}
	return strconv.ParseInt(status.Result.SyncInfo.LatestBlockHeight, 10, 64)
}

// --- GET /api/stats ---

type statsResponse struct {
	LatestHeight    int64  `json:"latestHeight"`
	Difficulty      string `json:"difficulty"`
	BlockReward     string `json:"blockReward"`
	CurrentEpoch    int64  `json:"currentEpoch"`
	TreasuryBalance string `json:"treasuryBalance"`
}

func handleStats(w http.ResponseWriter, r *http.Request) {
	height, err := fetchLatestHeight(rpcEndpoint)
	if err != nil {
		writeError(w, http.StatusBadGateway, fmt.Errorf("failed to fetch latest height: %w", err))
		return
	}

	conn, err := grpc.NewClient(grpcEndpoint, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		writeError(w, http.StatusBadGateway, fmt.Errorf("failed to connect to gRPC server: %w", err))
		return
	}
	defer conn.Close()

	ctx := context.Background()
	resp := statsResponse{LatestHeight: height}

	powClient := pow.NewQueryClient(conn)
	if diffResp, err := powClient.Difficulty(ctx, &pow.QueryDifficultyRequest{}); err == nil {
		resp.Difficulty = diffResp.Difficulty
	}
	if rewardResp, err := powClient.BlockReward(ctx, &pow.QueryBlockRewardRequest{}); err == nil {
		resp.BlockReward = rewardResp.BlockReward
	}
	if epochResp, err := powClient.CurrentEpoch(ctx, &pow.QueryCurrentEpochRequest{}); err == nil {
		resp.CurrentEpoch = epochResp.Epoch
	}

	bankClient := banktypes.NewQueryClient(conn)
	treasuryAddr := authtypes.NewModuleAddress("treasury")
	if balResp, err := bankClient.AllBalances(ctx, &banktypes.QueryAllBalancesRequest{Address: treasuryAddr.String()}); err == nil {
		if len(balResp.Balances) > 0 {
			resp.TreasuryBalance = balResp.Balances.String()
		} else {
			resp.TreasuryBalance = "0uaeth"
		}
	}

	writeJSON(w, http.StatusOK, resp)
}

// --- GET /api/validators ---

func handleValidators(w http.ResponseWriter, r *http.Request) {
	conn, err := grpc.NewClient(grpcEndpoint, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	defer conn.Close()

	powClient := pow.NewQueryClient(conn)
	resp, err := powClient.ValidatorInfo(context.Background(), &pow.QueryValidatorInfoRequest{})
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, toValidatorInfoDTOs(resp.Validators))
}

// --- GET /api/leaderboard?epoch= ---

func handleLeaderboard(w http.ResponseWriter, r *http.Request) {
	var epoch int64
	if q := r.URL.Query().Get("epoch"); q != "" {
		parsed, err := strconv.ParseInt(q, 10, 64)
		if err != nil {
			writeError(w, http.StatusBadRequest, fmt.Errorf("invalid epoch %q", q))
			return
		}
		epoch = parsed
	}

	conn, err := grpc.NewClient(grpcEndpoint, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	defer conn.Close()

	powClient := pow.NewQueryClient(conn)
	resp, err := powClient.MinerLeaderboard(context.Background(), &pow.QueryMinerLeaderboardRequest{Epoch: epoch})
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// --- GET /api/proposals ---

func handleProposals(w http.ResponseWriter, r *http.Request) {
	conn, err := grpc.NewClient(grpcEndpoint, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	defer conn.Close()

	govClient := governance.NewQueryClient(conn)
	resp, err := govClient.Proposals(context.Background(), &governance.QueryProposalsRequest{})
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, toProposalDTOs(resp.Proposals))
}

// --- GET /api/proposals/tally?id= ---

func handleProposalTally(w http.ResponseWriter, r *http.Request) {
	idStr := r.URL.Query().Get("id")
	id, err := strconv.ParseUint(idStr, 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid proposal id %q", idStr))
		return
	}

	conn, err := grpc.NewClient(grpcEndpoint, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	defer conn.Close()

	govClient := governance.NewQueryClient(conn)
	ctx := context.Background()

	tally, err := govClient.Tally(ctx, &governance.QueryTallyRequest{ProposalId: id})
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	votes, err := govClient.Votes(ctx, &governance.QueryVotesRequest{ProposalId: id})
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"tally": toTallyDTO(tally),
		"votes": toVoteDTOs(votes.Votes),
	})
}

// --- GET /api/recent-transactions?limit= ---

func handleRecentTransactions(w http.ResponseWriter, r *http.Request) {
	limit := uint64(20)
	if q := r.URL.Query().Get("limit"); q != "" {
		parsed, err := strconv.ParseUint(q, 10, 64)
		if err != nil {
			writeError(w, http.StatusBadRequest, fmt.Errorf("invalid limit %q", q))
			return
		}
		limit = parsed
	}

	client, err := wallet.NewClient(grpcEndpoint)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	defer client.Close()

	txs, err := client.GetRecentTransactions(limit)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, toRecentTransactionDTOs(txs))
}

// --- GET /api/address?addr= ---

type addressResponse struct {
	Address      string           `json:"address"`
	Balance      string           `json:"balance"`
	Transactions []transactionDTO `json:"transactions"`
}

func handleAddress(w http.ResponseWriter, r *http.Request) {
	addr := r.URL.Query().Get("addr")
	if addr == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("missing 'addr' query parameter"))
		return
	}

	client, err := wallet.NewClient(grpcEndpoint)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	defer client.Close()

	balance, err := client.GetBalance(addr)
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("failed to fetch balance (is this a valid address?): %w", err))
		return
	}

	txs, err := client.GetTransactionHistory(addr, 20)
	if err != nil {
		writeError(w, http.StatusBadGateway, fmt.Errorf("failed to fetch transaction history: %w", err))
		return
	}

	writeJSON(w, http.StatusOK, addressResponse{
		Address:      addr,
		Balance:      balance.AmountOf("uaeth").String(),
		Transactions: toTransactionDTOs(txs),
	})
}

// --- GET /api/tx?hash= ---

// handleSearch inspects a single, shared search box's input and
// reports which kind of page it resolves to -- an address (real
// bech32 prefix) or a transaction hash (hex string) -- so the frontend
// can navigate client-side rather than the backend issuing a redirect.
func handleSearch(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("empty query"))
		return
	}

	if strings.HasPrefix(q, "aether1") {
		writeJSON(w, http.StatusOK, map[string]string{"kind": "address", "value": q})
		return
	}

	hexCandidate := strings.TrimPrefix(strings.TrimPrefix(q, "0x"), "0X")
	isHex := len(hexCandidate) >= 32
	for _, c := range hexCandidate {
		if !strings.ContainsRune("0123456789abcdefABCDEF", c) {
			isHex = false
			break
		}
	}
	if isHex {
		writeJSON(w, http.StatusOK, map[string]string{"kind": "tx", "value": hexCandidate})
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"kind": "address", "value": q})
}

func handleTx(w http.ResponseWriter, r *http.Request) {
	hash := r.URL.Query().Get("hash")
	if hash == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("missing 'hash' query parameter"))
		return
	}

	client, err := wallet.NewClient(grpcEndpoint)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	defer client.Close()

	detail, err := client.GetTransactionByHash(hash)
	if err != nil {
		writeError(w, http.StatusNotFound, fmt.Errorf("transaction not found: %w", err))
		return
	}
	writeJSON(w, http.StatusOK, toTransactionDetailDTO(detail))
}

// spaFallback serves the file at the requested path when it exists on
// disk (JS/CSS bundles, images, etc.), and falls back to index.html
// otherwise -- react-router's client-side routes (e.g. /validators,
// /tx/:hash) have no matching file, so without this a direct
// navigation or page refresh on one of those paths 404s instead of
// loading the app.
func spaFallback(staticDir string, fileServer http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requested := filepath.Join(staticDir, filepath.Clean(r.URL.Path))
		if info, err := os.Stat(requested); err == nil && !info.IsDir() {
			fileServer.ServeHTTP(w, r)
			return
		}
		http.ServeFile(w, r, filepath.Join(staticDir, "index.html"))
	})
}

func main() {
	flag.StringVar(&grpcEndpoint, "grpc", "localhost:9090", "node gRPC endpoint")
	flag.StringVar(&rpcEndpoint, "rpc", "http://localhost:26657", "node CometBFT RPC endpoint")
	port := flag.String("port", "8081", "HTTP port to serve the API on")
	staticDir := flag.String("static", "", "optional path to explorer-web's built static assets to serve alongside the API")
	flag.Parse()

	// Deliberately use our own dedicated mux, never the shared global
	// http.DefaultServeMux -- the same real issue found live in the
	// faucet: a transitive dependency silently registers Go's pprof
	// debug handlers on the global default mux as an import side
	// effect. Passing nil to ListenAndServe meant this public
	// dashboard was unknowingly also serving full profiling/heap-dump
	// endpoints to the internet.
	mux := http.NewServeMux()

	mux.HandleFunc("/api/stats", withCORS(handleStats))
	mux.HandleFunc("/api/validators", withCORS(handleValidators))
	mux.HandleFunc("/api/leaderboard", withCORS(handleLeaderboard))
	mux.HandleFunc("/api/proposals", withCORS(handleProposals))
	mux.HandleFunc("/api/proposals/tally", withCORS(handleProposalTally))
	mux.HandleFunc("/api/recent-transactions", withCORS(handleRecentTransactions))
	mux.HandleFunc("/api/address", withCORS(handleAddress))
	mux.HandleFunc("/api/tx", withCORS(handleTx))
	mux.HandleFunc("/api/search", withCORS(handleSearch))

	// Optional: serve explorer-web's built static assets from the same
	// process/port, so production deploys are a single binary + one
	// static directory rather than two separately-run servers. In dev,
	// leave --static unset and run explorer-web's own Vite dev server
	// (which proxies /api to this process) instead.
	if *staticDir != "" {
		fileServer := http.FileServer(http.Dir(*staticDir))
		mux.Handle("/", spaFallback(*staticDir, fileServer))
	}

	addr := ":" + *port
	log.Printf("Aether explorer API listening on %s (querying gRPC %s, RPC %s)", addr, grpcEndpoint, rpcEndpoint)
	log.Fatal(http.ListenAndServe(addr, mux))
}
