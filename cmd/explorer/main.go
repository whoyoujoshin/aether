// cmd/explorer/main.go
//
// A minimal, read-only block explorer dashboard for Aether. Queries
// real chain state via gRPC (x/pow, x/governance, bank) and CometBFT
// RPC (latest height), rendering a simple server-side HTML page on
// each request -- no caching needed at this scale, no JavaScript
// framework, no client-side build step.
//
// Usage:
//   go run ./cmd/explorer --grpc localhost:9090 --rpc http://localhost:26657 --port 8081
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"strconv"

	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/whoyoujoshin/aether/x/governance"
	"github.com/whoyoujoshin/aether/x/pow"
)

var (
	grpcEndpoint string
	rpcEndpoint  string
)

type dashboardData struct {
	LatestHeight     int64
	Difficulty       string
	BlockReward      string
	CurrentEpoch     int64
	ActiveValidators []string
	TreasuryBalance  string
	Proposals        []*governance.Proposal
	GovParams        *governance.QueryParamsResponse
	Error            string
}

const dashboardTemplate = `
<!DOCTYPE html>
<html>
<head>
	<title>Aether Explorer</title>
	<meta charset="utf-8">
	<style>
		body { font-family: -apple-system, sans-serif; max-width: 900px; margin: 40px auto; padding: 0 20px; background: #0b0e14; color: #e0e0e0; }
		h1 { color: #7fd1ff; }
		h2 { color: #9fe3a0; margin-top: 40px; border-bottom: 1px solid #333; padding-bottom: 6px; }
		table { width: 100%; border-collapse: collapse; margin-top: 10px; }
		td, th { padding: 6px 10px; text-align: left; border-bottom: 1px solid #222; }
		.stat-label { color: #888; }
		.stat-value { font-weight: bold; color: #fff; }
		.address { font-family: monospace; font-size: 0.85em; color: #7fd1ff; word-break: break-all; }
		.error { color: #ff6b6b; background: #2a1515; padding: 10px; border-radius: 4px; }
		.empty { color: #666; font-style: italic; }
	</style>
</head>
<body>
	<h1>⚡ Aether Explorer</h1>
	{{if .Error}}
	<div class="error">{{.Error}}</div>
	{{end}}

	<h2>Chain Status</h2>
	<table>
		<tr><td class="stat-label">Latest Height</td><td class="stat-value">{{.LatestHeight}}</td></tr>
		<tr><td class="stat-label">Current Difficulty</td><td class="stat-value">{{.Difficulty}}</td></tr>
		<tr><td class="stat-label">Current Block Reward</td><td class="stat-value">{{.BlockReward}}</td></tr>
		<tr><td class="stat-label">Current Epoch</td><td class="stat-value">{{.CurrentEpoch}}</td></tr>
		<tr><td class="stat-label">Treasury Balance</td><td class="stat-value">{{.TreasuryBalance}}</td></tr>
	</table>

	<h2>Active Validators ({{len .ActiveValidators}})</h2>
	{{if .ActiveValidators}}
	<table>
		{{range .ActiveValidators}}
		<tr><td class="address">{{.}}</td></tr>
		{{end}}
	</table>
	{{else}}
	<p class="empty">No active validators.</p>
	{{end}}

	<h2>Governance</h2>
	{{if .GovParams}}
	<table>
		<tr><td class="stat-label">Min Deposit</td><td class="stat-value">{{.GovParams.MinDeposit}} uaeth</td></tr>
		<tr><td class="stat-label">Deposit Period</td><td class="stat-value">{{.GovParams.DepositPeriod}}s</td></tr>
		<tr><td class="stat-label">Voting Period</td><td class="stat-value">{{.GovParams.VotingPeriod}}s</td></tr>
	</table>
	{{end}}

	<h3>Proposals ({{len .Proposals}})</h3>
	{{if .Proposals}}
	<table>
		<tr><th>ID</th><th>Status</th><th>Recipient</th><th>Amount</th><th>Deposit</th></tr>
		{{range .Proposals}}
		<tr>
			<td>{{.Id}}</td>
			<td>{{.Status}}</td>
			<td class="address">{{.Recipient}}</td>
			<td>{{.Amount}} uaeth</td>
			<td>{{.TotalDeposit}} uaeth</td>
		</tr>
		{{end}}
	</table>
	{{else}}
	<p class="empty">No proposals yet.</p>
	{{end}}

	<p style="margin-top:40px;color:#555;font-size:0.8em;">Refreshes on reload. Not a substitute for a full audited explorer -- read-only view of real chain state.</p>
</body>
</html>
`

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

func buildDashboard() dashboardData {
	data := dashboardData{}

	height, err := fetchLatestHeight(rpcEndpoint)
	if err != nil {
		data.Error = fmt.Sprintf("failed to fetch latest height: %v", err)
		return data
	}
	data.LatestHeight = height

	conn, err := grpc.NewClient(grpcEndpoint, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		data.Error = fmt.Sprintf("failed to connect to gRPC server: %v", err)
		return data
	}
	defer conn.Close()

	ctx := context.Background()

	powClient := pow.NewQueryClient(conn)
	if diffResp, err := powClient.Difficulty(ctx, &pow.QueryDifficultyRequest{}); err == nil {
		data.Difficulty = diffResp.Difficulty
	}
	if rewardResp, err := powClient.BlockReward(ctx, &pow.QueryBlockRewardRequest{}); err == nil {
		data.BlockReward = rewardResp.BlockReward
	}
	if epochResp, err := powClient.CurrentEpoch(ctx, &pow.QueryCurrentEpochRequest{}); err == nil {
		data.CurrentEpoch = epochResp.Epoch
	}
	if valResp, err := powClient.ActiveValidators(ctx, &pow.QueryActiveValidatorsRequest{}); err == nil {
		data.ActiveValidators = valResp.Validators
	}

	govClient := governance.NewQueryClient(conn)
	if propResp, err := govClient.Proposals(ctx, &governance.QueryProposalsRequest{}); err == nil {
		data.Proposals = propResp.Proposals
	}
	if paramsResp, err := govClient.Params(ctx, &governance.QueryParamsRequest{}); err == nil {
		data.GovParams = paramsResp
	}

	bankClient := banktypes.NewQueryClient(conn)
	treasuryAddr := authtypes.NewModuleAddress("treasury")
	if balResp, err := bankClient.AllBalances(ctx, &banktypes.QueryAllBalancesRequest{Address: treasuryAddr.String()}); err == nil {
		if len(balResp.Balances) > 0 {
			data.TreasuryBalance = balResp.Balances.String()
		} else {
			data.TreasuryBalance = "0uaeth"
		}
	}

	return data
}

func main() {
	flag.StringVar(&grpcEndpoint, "grpc", "localhost:9090", "node gRPC endpoint")
	flag.StringVar(&rpcEndpoint, "rpc", "http://localhost:26657", "node CometBFT RPC endpoint")
	port := flag.String("port", "8081", "HTTP port to serve the dashboard on")
	flag.Parse()

	tmpl := template.Must(template.New("dashboard").Parse(dashboardTemplate))

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		data := buildDashboard()
		if err := tmpl.Execute(w, data); err != nil {
			log.Printf("template execution error: %v", err)
		}
	})

	addr := ":" + *port
	log.Printf("Aether explorer listening on %s (querying gRPC %s, RPC %s)", addr, grpcEndpoint, rpcEndpoint)
	log.Fatal(http.ListenAndServe(addr, nil))
}
