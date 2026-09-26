package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/whoyoujoshin/aether/wallet"
)

// Onboarding: `agentmcp init` sets up an agent wallet in one step, and
// request_testnet_funds lets an agent top itself up from the testnet
// faucet.

// The public testnet's endpoints: what `agentmcp init` connects to by default.
const (
	testnetFaucet = "http://157.245.252.221:8080/request"
	testnetGRPC   = "157.245.252.221:9090"
	testnetRPC    = "http://157.245.252.221:26657"
)

var (
	faucetURL string    // "" disables request_testnet_funds
	initOut   io.Writer = os.Stdout
	fundsWait           = 2 * time.Minute
)

func defaultFaucet(chain string) string {
	if chain == "aether-testnet-1" {
		return testnetFaucet
	}
	return ""
}

type faucetResult struct {
	Status  int
	Success bool   `json:"success"`
	Message string `json:"message"`
	TxHash  string `json:"tx_hash"`
}

func askFaucet(ctx context.Context, url, address string) (faucetResult, error) {
	body, _ := json.Marshal(map[string]string{"address": address})
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return faucetResult{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return faucetResult{}, err
	}
	defer resp.Body.Close()
	var r faucetResult
	bz, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if json.Unmarshal(bz, &r) != nil {
		r.Message = strings.TrimSpace(string(bz))
	}
	r.Status = resp.StatusCode
	return r, nil
}

type requestFundsInput struct{}

type requestFundsOutput struct {
	Status  string `json:"status" jsonschema:"sent (in a block), pending (sent, not confirmed yet)"`
	Address string `json:"address"`
	TxHash  string `json:"txHash,omitempty"`
	Message string `json:"message,omitempty"`
}

func toolRequestTestnetFunds(ctx context.Context, _ *mcp.CallToolRequest, _ requestFundsInput) (*mcp.CallToolResult, requestFundsOutput, error) {
	if faucetURL == "" {
		return nil, requestFundsOutput{}, newError(codeFaucetUnavailable, "no faucet is configured for "+chainID+" (testnet only; set --faucet)")
	}
	w, err := newWallet()
	if err != nil {
		return nil, requestFundsOutput{}, err
	}
	acc, err := getOrCreateAgentAccount(w)
	if err != nil {
		return nil, requestFundsOutput{}, err
	}
	r, err := askFaucet(ctx, faucetURL, acc.Address)
	if err != nil {
		e := newError(codeFaucetUnavailable, "couldn't reach the faucet: "+err.Error())
		e.Retryable = true
		return nil, requestFundsOutput{}, e
	}
	switch {
	case r.Status == http.StatusOK && r.Success:
		return nil, requestFundsOutput{Status: "sent", Address: acc.Address, TxHash: r.TxHash, Message: r.Message}, nil
	case r.Status == http.StatusAccepted:
		return nil, requestFundsOutput{Status: "pending", Address: acc.Address, TxHash: r.TxHash, Message: r.Message}, nil
	case r.Status == http.StatusTooManyRequests:
		return nil, requestFundsOutput{}, newError(codeFaucetRateLimited, "the faucet says: "+r.Message)
	}
	return nil, requestFundsOutput{}, newError(codeFaucetUnavailable, fmt.Sprintf("the faucet refused (HTTP %d): %s", r.Status, r.Message))
}

// runInit is `agentmcp init`: create the agent wallet, fund it from the
// testnet faucet, and print how to connect an MCP client.
func runInit(args []string) error {
	fs := flag.NewFlagSet("init", flag.ExitOnError)
	fs.StringVar(&keyringDir, "keyring-dir", defaultKeyringDir(), "directory for the agent's dedicated keyring")
	fs.StringVar(&accountName, "account-name", "agent", "name of the agent's key")
	fs.StringVar(&grpcEndpoint, "grpc", "", "node gRPC endpoint (default: the public testnet node on aether-testnet-1, else localhost:9090)")
	fs.StringVar(&rpcEndpoint, "rpc", "", "node CometBFT RPC endpoint (default: the public testnet node on aether-testnet-1, else http://localhost:26657)")
	fs.StringVar(&chainID, "chain-id", "aether-testnet-1", "chain ID")
	faucet := fs.String("faucet", "", "faucet URL (default: the public testnet faucet on aether-testnet-1)")
	noFaucet := fs.Bool("no-faucet", false, "don't request starter funds")
	if err := fs.Parse(args); err != nil {
		return err
	}
	keyringBackend = "test"
	if grpcEndpoint == "" {
		grpcEndpoint = "localhost:9090"
		if chainID == "aether-testnet-1" {
			grpcEndpoint = testnetGRPC
		}
	}
	if rpcEndpoint == "" {
		rpcEndpoint = "http://localhost:26657"
		if chainID == "aether-testnet-1" {
			rpcEndpoint = testnetRPC
		}
	}
	if *faucet == "" && !*noFaucet {
		*faucet = defaultFaucet(chainID)
	}
	out := initOut

	w, err := newWallet()
	if err != nil {
		return err
	}
	acc, err := w.GetAccount(accountName)
	if err != nil {
		created, mnemonic, err := w.CreateAccount(accountName)
		if err != nil {
			return err
		}
		acc = created
		fmt.Fprintf(out, "Created the agent's account %s\n\n", acc.Address)
		fmt.Fprintf(out, "  Back up this recovery phrase now -- it's shown only once, and anyone with it controls the account:\n\n    %s\n\n", mnemonic)
	} else {
		fmt.Fprintf(out, "Using the existing agent account %s\n\n", acc.Address)
	}

	if *faucet != "" {
		fmt.Fprintf(out, "Requesting testnet funds from %s ...\n", *faucet)
		r, err := askFaucet(context.Background(), *faucet, acc.Address)
		switch {
		case err != nil:
			fmt.Fprintf(out, "  couldn't reach the faucet (%v); fund %s yourself\n", err, acc.Address)
		case r.Success || r.Status == http.StatusAccepted:
			fmt.Fprintf(out, "  %s\n", firstNonEmpty(r.Message, "sent"))
			waitForFunds(out, acc.Address)
		default:
			fmt.Fprintf(out, "  the faucet said: %s\n", firstNonEmpty(r.Message, fmt.Sprintf("HTTP %d", r.Status)))
		}
	}

	command, installHint := selfCommand()
	serverArgs := []string{"--grpc", grpcEndpoint, "--rpc", rpcEndpoint, "--chain-id", chainID, "--keyring-dir", keyringDir,
		"--per-tx-limit", "1000000", "--daily-limit", "5000000"}
	if accountName != "agent" {
		serverArgs = append(serverArgs, "--account-name", accountName)
	}
	fmt.Fprintf(out, "\nConnect an MCP client:\n")
	if installHint != "" {
		fmt.Fprintf(out, "  (first: %s)\n", installHint)
	}
	fmt.Fprintf(out, "\n  Claude Code:\n    claude mcp add aether-wallet -- %s %s\n", command, shellJoin(serverArgs))
	type server struct {
		Command string   `json:"command"`
		Args    []string `json:"args"`
	}
	cfg, _ := json.MarshalIndent(map[string]any{"mcpServers": map[string]server{"aether-wallet": {command, serverArgs}}}, "  ", "  ")
	fmt.Fprintf(out, "\n  Claude Desktop and other MCP clients (add to the client's MCP config):\n  %s\n", cfg)
	fmt.Fprintf(out, "\nLimits: at most 1 AETH per payment and 5 AETH per 24h (change --per-tx-limit/--daily-limit, in uaeth).\n")
	fmt.Fprintf(out, "For oversight add --approval-threshold \"0.5 AETH\" --approver <your address> and --notify-webhook <url>.\n")
	return nil
}

func waitForFunds(out io.Writer, address string) {
	client, err := wallet.NewClient(grpcEndpoint)
	if err != nil {
		return
	}
	defer client.Close()
	deadline := time.Now().Add(fundsWait)
	for time.Now().Before(deadline) {
		if bal, err := client.GetBalance(address); err == nil && bal.AmountOf(baseDenom).IsPositive() {
			fmt.Fprintf(out, "  balance: %s AETH\n", formatAeth(bal.AmountOf(baseDenom)))
			return
		}
		time.Sleep(3 * time.Second)
	}
	fmt.Fprintf(out, "  not in a block yet (blocks are ~60s apart); get_balance will show it shortly\n")
}

// selfCommand is how an MCP client should start this program. A `go run`
// binary lives in a temp dir that disappears, so it suggests installing.
func selfCommand() (command, installHint string) {
	exe, err := os.Executable()
	if err == nil {
		exe, _ = filepath.EvalSymlinks(exe)
	}
	// go run builds into a temporary go-build directory that's gone afterwards.
	if err != nil || strings.Contains(exe, "go-build") {
		return "agentmcp", "go install github.com/whoyoujoshin/aether/cmd/agentmcp@main, so `agentmcp` is on your PATH"
	}
	return exe, ""
}

func shellJoin(args []string) string {
	quoted := make([]string, len(args))
	for i, a := range args {
		if a == "" || strings.ContainsAny(a, " \t\"'$`\\") {
			a = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
		}
		quoted[i] = a
	}
	return strings.Join(quoted, " ")
}

func firstNonEmpty(s ...string) string {
	for _, v := range s {
		if v != "" {
			return v
		}
	}
	return ""
}
