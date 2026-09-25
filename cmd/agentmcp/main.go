// cmd/agentmcp/main.go
//
// An MCP server exposing Aether wallet operations as tool calls, so
// an AI agent can check balances and send AETH directly through tool
// calls rather than only a human clicking through a wallet UI.
//
// SECURITY MODEL (read before deploying this anywhere real):
//
// This server holds a real, unencrypted signing key for one dedicated
// "agent" account (keyring backend "test" -- unavoidable for
// unattended signing; there is no human present to type a passphrase
// on every tool call). Anyone with filesystem access to --keyring-dir
// can spend from this account. Two independent guards limit the blast
// radius if that key or this process is ever compromised:
//
//  1. A hard per-transaction cap (--per-tx-limit).
//  2. A rolling 24h spend cap (--daily-limit), tracked in
//     --state-file across restarts.
//
// Both are enforced HERE, in this process, before a transaction is
// ever built or signed -- NOT by the chain itself. A bug in this
// server, or direct use of the underlying keyring by another tool,
// bypasses them entirely. The real fix -- an on-chain-enforced,
// revocable spending limit via Cosmos SDK's x/authz (a human account
// grants this agent account a scoped SendAuthorization with its own
// SpendLimit and expiration, optionally paired with x/feegrant so the
// agent never needs its own gas) -- requires wiring x/authz (and
// optionally x/feegrant) into app.go, which neither module currently
// is. That's real, valuable follow-up work: it moves enforcement from
// "this process promises to behave" to "the chain itself rejects
// anything over the grant," the same shape every serious agentic
// wallet (Coinbase Agentic Wallets, Turnkey, etc.) converges on as of
// 2026. Until then, treat this account as a hot wallet: fund it with
// only what you're comfortable an agent losing, never your main funds.
//
// Usage (stdio transport, for a local MCP client):
//
//	go run ./cmd/agentmcp --grpc localhost:9090 --chain-id aether-testnet-1 \
//	    --per-tx-limit 1000000 --daily-limit 5000000
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"cosmossdk.io/math"
	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/whoyoujoshin/aether/app"
	"github.com/whoyoujoshin/aether/crypto/mldsa"
	"github.com/whoyoujoshin/aether/wallet"
)

func init() {
	app.SetAddressPrefixes()
}

var (
	grpcEndpoint   string
	chainID        string
	keyringDir     string
	keyringBackend string
	accountName    string
	perTxLimit     int64
	dailyLimit     int64
	stateFile      string
)

func defaultKeyringDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".aether-agent"
	}
	// Deliberately a separate directory from a human's own aetherd/
	// wallet keyring -- this key is meant to hold a small, disposable
	// agent-spending balance, never a human's main funds.
	return filepath.Join(home, ".aether-agent")
}

func newWallet() (*wallet.Wallet, error) {
	registry := codectypes.NewInterfaceRegistry()
	mldsa.RegisterInterfaces(registry)
	cdc := codec.NewProtoCodec(registry)
	return wallet.NewWallet("aetherd", keyringBackend, keyringDir, cdc)
}

// getOrCreateAgentAccount returns the managed agent account, creating
// it on first use. The one-time mnemonic is logged to stderr (never
// returned to an MCP tool caller, which could otherwise hand full
// account control to whatever agent asked) -- back it up from there.
func getOrCreateAgentAccount(w *wallet.Wallet) (wallet.Account, error) {
	if acc, err := w.GetAccount(accountName); err == nil {
		return acc, nil
	}
	acc, mnemonic, err := w.CreateAccount(accountName)
	if err != nil {
		return wallet.Account{}, err
	}
	log.Printf("Created new agent account %q (%s).", accountName, acc.Address)
	log.Printf("Mnemonic (back this up now, shown only once): %s", mnemonic)
	return acc, nil
}

// --- spend tracking ---
//
// Persisted to disk (not just in-memory) so restarting this process
// doesn't reset the rolling window -- that would turn "daily limit"
// into "limit per process lifetime," defeating the point.

type spendEvent struct {
	Time   time.Time `json:"time"`
	Amount int64     `json:"amountUaeth"`
}

type spendLog struct {
	Events []spendEvent `json:"events"`
}

func loadSpendLog() (*spendLog, error) {
	bz, err := os.ReadFile(stateFile)
	if os.IsNotExist(err) {
		return &spendLog{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read spend state: %w", err)
	}
	var l spendLog
	if err := json.Unmarshal(bz, &l); err != nil {
		return nil, fmt.Errorf("failed to parse spend state: %w", err)
	}
	return &l, nil
}

func (l *spendLog) save() error {
	bz, err := json.MarshalIndent(l, "", "  ")
	if err != nil {
		return err
	}
	if dir := filepath.Dir(stateFile); dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	}
	return os.WriteFile(stateFile, bz, 0o600)
}

// spentInWindow returns the total spent within the last 24h, pruning
// older entries as a side effect so the log doesn't grow forever.
func (l *spendLog) spentInWindow(now time.Time) int64 {
	cutoff := now.Add(-24 * time.Hour)
	kept := l.Events[:0]
	var total int64
	for _, e := range l.Events {
		if e.Time.After(cutoff) {
			kept = append(kept, e)
			total += e.Amount
		}
	}
	l.Events = kept
	return total
}

// spendMu serializes the whole check-build-sign-broadcast-record
// sequence for send_aeth. Without it, two concurrent calls could both
// pass the daily-limit check before either one's amount is recorded,
// jointly exceeding it -- a classic check-then-act race.
var spendMu sync.Mutex

// checkLimit enforces both limits BEFORE any transaction is built or
// signed, without yet recording the spend -- recordSpend does that,
// only once a broadcast actually succeeds, so a failed or rejected
// transaction doesn't burn the agent's budget for nothing. Callers
// must hold spendMu for the whole check-through-record sequence.
func checkLimit(amount int64) error {
	if amount > perTxLimit {
		return fmt.Errorf("amount %d uaeth exceeds the per-transaction limit of %d uaeth", amount, perTxLimit)
	}

	l, err := loadSpendLog()
	if err != nil {
		return err
	}
	spent := l.spentInWindow(time.Now())
	if spent+amount > dailyLimit {
		return fmt.Errorf("amount %d uaeth would exceed the rolling 24h limit of %d uaeth (%d already spent in the last 24h)", amount, dailyLimit, spent)
	}
	return nil
}

// recordSpend persists a completed spend. Caller must hold spendMu.
func recordSpend(amount int64) error {
	l, err := loadSpendLog()
	if err != nil {
		return err
	}
	now := time.Now()
	l.spentInWindow(now) // prune stale entries before appending
	l.Events = append(l.Events, spendEvent{Time: now, Amount: amount})
	return l.save()
}

// --- DTOs ---
//
// wallet.Transaction/TransactionDetail have no JSON tags (they
// serialize as PascalCase, matching the desktop wallet's direct field
// access -- see cmd/explorer/dto.go for the same issue). Converting
// explicitly here keeps this server's own tool output consistently
// camelCase without touching that shared type.

type transactionSummaryDTO struct {
	Hash      string `json:"hash"`
	Height    int64  `json:"height"`
	Code      uint32 `json:"code"`
	Direction string `json:"direction"`
	Amount    string `json:"amount"`
	Timestamp string `json:"timestamp"`
}

func toTransactionSummaryDTOs(txs []wallet.Transaction) []transactionSummaryDTO {
	out := make([]transactionSummaryDTO, 0, len(txs))
	for _, t := range txs {
		out = append(out, transactionSummaryDTO{
			Hash: t.Hash, Height: t.Height, Code: t.Code,
			Direction: t.Direction, Amount: t.Amount, Timestamp: t.Timestamp,
		})
	}
	return out
}

// --- MCP tools ---

type getAgentAddressInput struct{}

type getAgentAddressOutput struct {
	Address string `json:"address" jsonschema:"the agent's Aether account address; fund this with AETH before send_aeth can succeed"`
}

func toolGetAgentAddress(_ context.Context, _ *mcp.CallToolRequest, _ getAgentAddressInput) (*mcp.CallToolResult, getAgentAddressOutput, error) {
	w, err := newWallet()
	if err != nil {
		return nil, getAgentAddressOutput{}, err
	}
	acc, err := getOrCreateAgentAccount(w)
	if err != nil {
		return nil, getAgentAddressOutput{}, err
	}
	return nil, getAgentAddressOutput{Address: acc.Address}, nil
}

type getBalanceInput struct {
	Address string `json:"address,omitempty" jsonschema:"address to check; defaults to this agent's own account if omitted"`
}

type getBalanceOutput struct {
	Address string `json:"address"`
	Balance string `json:"balance" jsonschema:"coins held, e.g. '1000000uaeth'"`
}

func toolGetBalance(_ context.Context, _ *mcp.CallToolRequest, input getBalanceInput) (*mcp.CallToolResult, getBalanceOutput, error) {
	address := input.Address
	if address == "" {
		w, err := newWallet()
		if err != nil {
			return nil, getBalanceOutput{}, err
		}
		acc, err := getOrCreateAgentAccount(w)
		if err != nil {
			return nil, getBalanceOutput{}, err
		}
		address = acc.Address
	}

	client, err := wallet.NewClient(grpcEndpoint)
	if err != nil {
		return nil, getBalanceOutput{}, err
	}
	defer client.Close()

	balance, err := client.GetBalance(address)
	if err != nil {
		return nil, getBalanceOutput{}, err
	}
	return nil, getBalanceOutput{Address: address, Balance: balance.String()}, nil
}

type getSpendingStatusInput struct{}

type getSpendingStatusOutput struct {
	PerTxLimitUaeth   int64 `json:"perTxLimitUaeth"`
	DailyLimitUaeth   int64 `json:"dailyLimitUaeth"`
	SpentLast24hUaeth int64 `json:"spentLast24hUaeth"`
	RemainingUaeth    int64 `json:"remainingUaeth"`
}

func toolGetSpendingStatus(_ context.Context, _ *mcp.CallToolRequest, _ getSpendingStatusInput) (*mcp.CallToolResult, getSpendingStatusOutput, error) {
	spendMu.Lock()
	defer spendMu.Unlock()

	l, err := loadSpendLog()
	if err != nil {
		return nil, getSpendingStatusOutput{}, err
	}
	spent := l.spentInWindow(time.Now())
	remaining := dailyLimit - spent
	if remaining < 0 {
		remaining = 0
	}
	return nil, getSpendingStatusOutput{
		PerTxLimitUaeth:   perTxLimit,
		DailyLimitUaeth:   dailyLimit,
		SpentLast24hUaeth: spent,
		RemainingUaeth:    remaining,
	}, nil
}

type sendAethInput struct {
	To     string `json:"to" jsonschema:"recipient Aether address (aether1...)"`
	Amount string `json:"amount" jsonschema:"amount to send in uaeth, the chain's base denom (1 AETH = 1,000,000 uaeth), as a plain positive integer string"`
}

type sendAethOutput struct {
	Success bool   `json:"success"`
	TxHash  string `json:"txHash,omitempty"`
	Code    uint32 `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
}

func toolSendAeth(_ context.Context, _ *mcp.CallToolRequest, input sendAethInput) (*mcp.CallToolResult, sendAethOutput, error) {
	amount, ok := math.NewIntFromString(input.Amount)
	if !ok || !amount.IsPositive() {
		return nil, sendAethOutput{}, fmt.Errorf("invalid amount %q: must be a positive integer number of uaeth", input.Amount)
	}
	amountInt64 := amount.Int64()

	// Held for the whole check-build-sign-broadcast-record sequence:
	// checking the limit and recording the spend must be atomic with
	// respect to each other, or two concurrent calls could both pass
	// the check before either is recorded.
	spendMu.Lock()
	defer spendMu.Unlock()

	if err := checkLimit(amountInt64); err != nil {
		return nil, sendAethOutput{}, err
	}

	w, err := newWallet()
	if err != nil {
		return nil, sendAethOutput{}, err
	}
	fromAccount, err := getOrCreateAgentAccount(w)
	if err != nil {
		return nil, sendAethOutput{}, err
	}

	client, err := wallet.NewClient(grpcEndpoint)
	if err != nil {
		return nil, sendAethOutput{}, err
	}
	defer client.Close()

	accountNumber, sequence, err := client.GetAccountInfo(fromAccount.Address)
	if err != nil {
		return nil, sendAethOutput{}, fmt.Errorf("failed to fetch agent account info (is %s funded yet?): %w", fromAccount.Address, err)
	}

	coins := sdk.NewCoins(sdk.NewCoin("uaeth", amount))
	signed, err := w.BuildAndSignSendTx(accountName, fromAccount.Address, input.To, coins, wallet.TxParams{
		ChainID:       chainID,
		AccountNumber: accountNumber,
		Sequence:      sequence,
		GasLimit:      400_000,
		Fees:          sdk.NewCoins(sdk.NewCoin("uaeth", math.NewInt(0))),
	})
	if err != nil {
		return nil, sendAethOutput{}, fmt.Errorf("failed to build/sign transaction: %w", err)
	}

	result, err := client.BroadcastTx(signed)
	if err != nil {
		return nil, sendAethOutput{}, fmt.Errorf("failed to broadcast: %w", err)
	}

	// Only a confirmed on-chain success burns the agent's budget -- a
	// rejected or failed transaction (bad sequence, insufficient
	// balance, node-side error) spent nothing, so it shouldn't count
	// against the limit either.
	if result.Code == 0 {
		if err := recordSpend(amountInt64); err != nil {
			return nil, sendAethOutput{}, fmt.Errorf("transaction succeeded (tx %s) but failed to record spend: %w", result.TxHash, err)
		}
	}

	return nil, sendAethOutput{
		Success: result.Code == 0,
		TxHash:  result.TxHash,
		Code:    result.Code,
		Message: result.RawLog,
	}, nil
}

type getTransactionStatusInput struct {
	Hash string `json:"hash" jsonschema:"transaction hash to look up"`
}

type getTransactionStatusOutput struct {
	Hash      string `json:"hash"`
	Height    int64  `json:"height"`
	Code      uint32 `json:"code"`
	RawLog    string `json:"rawLog,omitempty"`
	From      string `json:"from"`
	To        string `json:"to"`
	Amount    string `json:"amount"`
	Timestamp string `json:"timestamp"`
}

func toolGetTransactionStatus(_ context.Context, _ *mcp.CallToolRequest, input getTransactionStatusInput) (*mcp.CallToolResult, getTransactionStatusOutput, error) {
	client, err := wallet.NewClient(grpcEndpoint)
	if err != nil {
		return nil, getTransactionStatusOutput{}, err
	}
	defer client.Close()

	tx, err := client.GetTransactionByHash(input.Hash)
	if err != nil {
		return nil, getTransactionStatusOutput{}, err
	}
	return nil, getTransactionStatusOutput{
		Hash: tx.Hash, Height: tx.Height, Code: tx.Code, RawLog: tx.RawLog,
		From: tx.From, To: tx.To, Amount: tx.Amount, Timestamp: tx.Timestamp,
	}, nil
}

type getTransactionHistoryInput struct {
	Limit uint64 `json:"limit,omitempty" jsonschema:"maximum transactions to return; defaults to 10"`
}

type getTransactionHistoryOutput struct {
	Transactions []transactionSummaryDTO `json:"transactions"`
}

func toolGetTransactionHistory(_ context.Context, _ *mcp.CallToolRequest, input getTransactionHistoryInput) (*mcp.CallToolResult, getTransactionHistoryOutput, error) {
	limit := input.Limit
	if limit == 0 {
		limit = 10
	}

	w, err := newWallet()
	if err != nil {
		return nil, getTransactionHistoryOutput{}, err
	}
	acc, err := getOrCreateAgentAccount(w)
	if err != nil {
		return nil, getTransactionHistoryOutput{}, err
	}

	client, err := wallet.NewClient(grpcEndpoint)
	if err != nil {
		return nil, getTransactionHistoryOutput{}, err
	}
	defer client.Close()

	txs, err := client.GetTransactionHistory(acc.Address, limit)
	if err != nil {
		return nil, getTransactionHistoryOutput{}, err
	}
	return nil, getTransactionHistoryOutput{Transactions: toTransactionSummaryDTOs(txs)}, nil
}

func main() {
	flag.StringVar(&grpcEndpoint, "grpc", "localhost:9090", "node gRPC endpoint")
	flag.StringVar(&chainID, "chain-id", "aether-testnet-1", "chain ID")
	flag.StringVar(&keyringDir, "keyring-dir", defaultKeyringDir(), "directory for the agent's dedicated keyring")
	flag.StringVar(&keyringBackend, "keyring-backend", "test", `keyring backend -- "test" (unencrypted) is required for unattended signing; see this file's package doc comment before using anything but a small, disposable balance`)
	flag.StringVar(&accountName, "account-name", "agent", "name of the managed account within the keyring")
	flag.Int64Var(&perTxLimit, "per-tx-limit", 1_000_000, "maximum uaeth spendable in a single send_aeth call")
	flag.Int64Var(&dailyLimit, "daily-limit", 5_000_000, "maximum uaeth spendable in any rolling 24h window")
	flag.StringVar(&stateFile, "state-file", "", "path to persist spend tracking across restarts (defaults to <keyring-dir>/agentmcp-spend.json)")
	flag.Parse()

	if stateFile == "" {
		stateFile = filepath.Join(keyringDir, "agentmcp-spend.json")
	}

	server := mcp.NewServer(&mcp.Implementation{Name: "aether-wallet", Version: "v0.1.0"}, nil)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_agent_address",
		Description: "Get this agent's own Aether account address. Fund this address before send_aeth can succeed.",
	}, toolGetAgentAddress)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_balance",
		Description: "Check the AETH balance of an address. Defaults to this agent's own account if no address is given.",
	}, toolGetBalance)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_spending_status",
		Description: "See this agent's configured spending limits and how much of its rolling 24h budget remains.",
	}, toolGetSpendingStatus)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "send_aeth",
		Description: "Send AETH from this agent's account to another address. Checked against a per-transaction cap and a rolling 24h cap enforced by this server -- not by the chain itself.",
	}, toolSendAeth)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_transaction_status",
		Description: "Look up a transaction by hash and see whether it succeeded.",
	}, toolGetTransactionStatus)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_transaction_history",
		Description: "List this agent's own recent transactions, most recent first.",
	}, toolGetTransactionHistory)

	// Go's log package already defaults to stderr, which matters here:
	// stdio transport reserves stdout entirely for the MCP protocol
	// stream, so any stray stdout write (a future fmt.Println, a
	// misbehaving dependency) would corrupt every client connected to
	// this process.
	log.Printf("Aether agent wallet MCP server starting (grpc=%s chain-id=%s account=%s keyring-dir=%s)",
		grpcEndpoint, chainID, accountName, keyringDir)

	if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		log.Fatal(err)
	}
}
