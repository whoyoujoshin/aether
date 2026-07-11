// cmd/balancecheck/main.go
//
// Standalone helper to check an account's bank balance via gRPC, since
// this SDK version's bank module relies on autocli (reflection-based CLI
// generation) for its query commands, which isn't wired into aetherd's
// root command yet. This talks directly to the app's gRPC server
// (localhost:9090 by default) using the generated bank query client.
//
// Usage:
//   go run ./cmd/balancecheck --address cosmos1yt20n0mn2wurrfs7zn8p854p4mvw74ue2aakmz
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strconv"

	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
)

// fetchLatestHeight asks CometBFT's RPC (not the gRPC query server) for the
// current chain height. This is a separate, simpler endpoint that reliably
// reflects the last committed block.
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

func main() {
	addr := flag.String("address", "", "bech32 account address to check (required)")
	grpcAddr := flag.String("grpc", "localhost:9090", "gRPC server address")
	rpcAddr := flag.String("rpc", "http://localhost:26657", "CometBFT RPC address")
	height := flag.Int64("height", 0, "specific height to query at (0 = auto: latest-2, avoiding the 'latest height' race)")
	flag.Parse()

	if *addr == "" {
		fmt.Fprintln(os.Stderr, "error: --address is required")
		os.Exit(1)
	}

	queryHeight := *height
	if queryHeight == 0 {
		latest, err := fetchLatestHeight(*rpcAddr)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: failed to fetch latest height from %s: %v\n", *rpcAddr, err)
			os.Exit(1)
		}
		queryHeight = latest - 2
		if queryHeight < 1 {
			queryHeight = 1
		}
		fmt.Printf("(querying at height %d, 2 behind the reported latest of %d, to avoid the latest-height race)\n", queryHeight, latest)
	}

	conn, err := grpc.NewClient(*grpcAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: failed to connect to gRPC server at %s: %v\n", *grpcAddr, err)
		os.Exit(1)
	}
	defer conn.Close()

	client := banktypes.NewQueryClient(conn)

	// Attach the target height via gRPC metadata, the standard Cosmos SDK
	// mechanism for pinning a query to a specific historical height rather
	// than implicitly asking for "latest".
	ctx := metadata.AppendToOutgoingContext(context.Background(), "x-cosmos-block-height", strconv.FormatInt(queryHeight, 10))

	resp, err := client.AllBalances(ctx, &banktypes.QueryAllBalancesRequest{
		Address: *addr,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: query failed at height %d: %v\n", queryHeight, err)
		os.Exit(1)
	}

	if len(resp.Balances) == 0 {
		fmt.Printf("%s has no balance (account may not exist on-chain yet)\n", *addr)
		return
	}

	fmt.Printf("Balances for %s:\n", *addr)
	for _, coin := range resp.Balances {
		fmt.Printf("  %s\n", coin.String())
	}
}
