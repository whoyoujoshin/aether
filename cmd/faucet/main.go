// cmd/faucet/main.go
//
// A minimal, standalone HTTP faucet for the Aether testnet. Dispenses
// a small, fixed amount of testnet aeth to any valid address, once per
// address per cooldown period, and at most --caller-limit addresses per
// caller per window. Signs and broadcasts with the wallet library.
//
// Built for bots as much as people:
//
//	GET  /                  what this faucet gives and its limits, as JSON
//	GET  /status?address=   whether an address can be funded now, and when if not
//	POST /request           {"address":"aether1..."}
//	POST /request/batch     {"addresses":["aether1...", ...]}: one transaction for all
//	GET  /challenge?address= a small proof of work a browser solves (pow.go)
//	POST /agents            {"name","public_key"}: register an agent key (agents.go)
//	GET  /stats             drips, wallets funded and created, by source
//	GET  /drips?limit=      the newest drips
//
// Each drip is tagged with where its request came from: a browser that
// solved a /challenge ("web"), a request signed with a registered agent
// key ("agent", with its own quota), or neither ("api", held to the
// per-IP limit). A drip to an address the chain had no account for is
// a new wallet. Drips are kept in --drip-log so /stats survives restarts.
//
// Every answer has a stable "code" and carries the caller's quota as
// RateLimit-* headers; a 429 carries Retry-After.
//
// Usage:
//
//	go run ./cmd/faucet --from faucet --chain-id aether-testnet-1 --amount 1000000
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"sync"
	"time"

	"cosmossdk.io/math"
	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	"github.com/whoyoujoshin/aether/app"
	"github.com/whoyoujoshin/aether/crypto/mldsa"
	"github.com/whoyoujoshin/aether/wallet"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func init() {
	app.SetAddressPrefixes()
}

var bech32Pattern = regexp.MustCompile(`^aether1[a-z0-9]{38,90}$`)

// Response codes are part of this faucet's interface: bots switch on
// them, so they're never renamed.
const (
	codeSent            = "sent"
	codePending         = "pending"          // broadcast, not confirmed in a block yet
	codeInvalidRequest  = "invalid_request"  // malformed body or method
	codeInvalidAddress  = "invalid_address"  // not an aether1... address
	codeBatchTooLarge   = "batch_too_large"  // more addresses than --batch-max
	codeAddressCooldown = "address_cooldown" // funded too recently; see retry_after_seconds
	codeCallerLimit     = "caller_limit"     // this caller's quota is used up; see retry_after_seconds
	codeSendFailed      = "send_failed"      // nothing was sent; safe to retry
	codeInvalidPow      = "invalid_pow"      // the /challenge solution doesn't check out; fetch a new one
	codeInvalidAgent    = "invalid_agent"    // agent headers present but unknown key, bad signature or clock skew
	codeIBCUnavailable  = "ibc_unavailable"  // strand "ibc": no IBC drips until the Osmosis channel is open
)

// Gas for a transaction paying n addresses: one send keeps the 400,000
// it always had. Measured on a devnet, one send used 233k and nine
// 519k, so each extra MsgSend costs about 36k next to the signature
// check they share.
const (
	gasBase    = 350_000
	gasPerSend = 50_000
)

type faucetServer struct {
	mu          sync.Mutex // serializes real sends AND protects the in-memory sequence counter
	limits      *limiter
	trusted     map[string]bool // reverse proxies whose X-Forwarded-For names the caller
	batchMax    int
	wal         *wallet.Wallet
	client      *wallet.Client
	fromKey     string
	fromAddr    string
	chainID     string
	amountUaeth int64
	gasPrice    sdk.DecCoin

	accountNumber uint64
	sequence      uint64 // tracked in-memory; incremented locally after each successful broadcast, never re-queried from the chain per-request -- avoids the exact stale-sequence race a real, independent stress test found: re-querying the chain for sequence on every send fails whenever two sends happen within the same ~60s block window, since sequence only updates once a tx is actually included in a block, not merely broadcast.

	confirmTimeout      time.Duration
	confirmPollInterval time.Duration

	// send and confirm are sendCoins and confirmTxOnChain, or fakes in tests.
	send    func(addresses []string) (string, error)
	confirm func(txHash string) (*wallet.TransactionDetail, error)

	ledger    *ledger
	pow       *powIssuer
	agents    *agentRegistry
	regLimits *limiter // agent registrations per IP
	// accountExists reports whether the chain has an account at an
	// address; a drip to one that doesn't is a new wallet.
	accountExists func(address string) (bool, error)
	// balance is the faucet account's uaeth, for /stats.
	balance func() (int64, error)
	now     func() time.Time
}

// origin is who a request is from, for limits and the drip log.
type origin struct {
	caller string // limiter identity: client IP, or agentCallerPrefix + key id
	source string
	agent  agentKey
}

type skippedAddress struct {
	Address           string `json:"address"`
	Code              string `json:"code"`
	RetryAfterSeconds int    `json:"retry_after_seconds"`
}

type responseBody struct {
	Success           bool             `json:"success"`
	Code              string           `json:"code"`
	Message           string           `json:"message"`
	TxHash            string           `json:"tx_hash,omitempty"`
	RetryAfterSeconds int              `json:"retry_after_seconds,omitempty"`
	Sent              []string         `json:"sent,omitempty"`
	Skipped           []skippedAddress `json:"skipped,omitempty"`
	Invalid           []string         `json:"invalid,omitempty"`
	// NewWallet, on a single request's "sent": the chain had no account
	// at the address before this drip.
	NewWallet *bool `json:"new_wallet,omitempty"`
}

// reply writes body with the caller's current quota in the headers,
// and Retry-After on a 429.
func (f *faucetServer) reply(w http.ResponseWriter, caller string, status int, body responseBody) {
	setQuotaHeaders(w.Header(), f.limits.quota(caller), f.limits.callerWindow)
	if status == http.StatusTooManyRequests && body.RetryAfterSeconds > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(body.RetryAfterSeconds))
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(body)
}

// POST /request {"address":"aether1...", "strand":"aether",
// "pow":{"challenge":"...","nonce":"..."}}; strand and pow optional.
func (f *faucetServer) handleRequest(w http.ResponseWriter, r *http.Request) {
	caller := clientIP(r, f.trusted)
	if r.Method != http.MethodPost {
		f.reply(w, caller, http.StatusMethodNotAllowed, responseBody{Code: codeInvalidRequest, Message: "use POST"})
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 4<<10))
	var req struct {
		Address string `json:"address"`
		Strand  string `json:"strand"`
		Pow     *struct {
			Challenge string `json:"challenge"`
			Nonce     string `json:"nonce"`
		} `json:"pow"`
	}
	if err != nil || json.Unmarshal(body, &req) != nil {
		f.reply(w, caller, http.StatusBadRequest, responseBody{Code: codeInvalidRequest, Message: "invalid request body"})
		return
	}
	o, ok := f.originOf(w, r, caller, body)
	if !ok {
		return
	}
	if !bech32Pattern.MatchString(req.Address) {
		f.reply(w, o.caller, http.StatusBadRequest, responseBody{Code: codeInvalidAddress, Message: "invalid address format"})
		return
	}
	if !f.strandOK(w, o.caller, req.Strand) {
		return
	}
	if req.Pow != nil && o.source != sourceAgent {
		if err := f.pow.redeem(req.Pow.Challenge, req.Pow.Nonce, req.Address); err != nil {
			f.reply(w, o.caller, http.StatusBadRequest, responseBody{Code: codeInvalidPow, Message: err.Error()})
			return
		}
		o.source = sourceWeb
	}
	f.dispense(w, o, []string{req.Address}, false)
}

// originOf works out who's asking: a verified agent, or the client IP.
// It answers the request itself, and returns false, when agent headers
// are present but don't verify.
func (f *faucetServer) originOf(w http.ResponseWriter, r *http.Request, ip string, body []byte) (origin, bool) {
	k, claimed, err := f.agents.verify(r.Header, body)
	if !claimed {
		return origin{caller: ip, source: sourceAPI}, true
	}
	if err != nil {
		f.reply(w, ip, http.StatusUnauthorized, responseBody{Code: codeInvalidAgent, Message: err.Error()})
		return origin{}, false
	}
	return origin{caller: agentCallerPrefix + k.ID, source: sourceAgent, agent: k}, true
}

// strandOK accepts the Aether strand. IBC drips (to an address on the
// counterparty chain) wait for the Osmosis channel; until then they're
// refused with a code a page can show.
func (f *faucetServer) strandOK(w http.ResponseWriter, caller, strand string) bool {
	switch strand {
	case "", strandAether:
		return true
	case "ibc":
		f.reply(w, caller, http.StatusBadRequest, responseBody{Code: codeIBCUnavailable,
			Message: "IBC drips open with the Osmosis channel; use strand \"aether\" for now"})
	default:
		f.reply(w, caller, http.StatusBadRequest, responseBody{Code: codeInvalidRequest, Message: `strand: "aether" or "ibc"`})
	}
	return false
}

// POST /request/batch {"addresses":["aether1...", ...]}
//
// Funds every address that isn't cooling down in one transaction, and
// lists the ones skipped. All or nothing against the caller's quota: if
// it can't cover them all, nothing is sent and the answer says how many
// it can.
func (f *faucetServer) handleBatch(w http.ResponseWriter, r *http.Request) {
	caller := clientIP(r, f.trusted)
	if r.Method != http.MethodPost {
		f.reply(w, caller, http.StatusMethodNotAllowed, responseBody{Code: codeInvalidRequest, Message: "use POST"})
		return
	}
	var req struct {
		Addresses []string `json:"addresses"`
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 64<<10))
	if err != nil || json.Unmarshal(body, &req) != nil || len(req.Addresses) == 0 {
		f.reply(w, caller, http.StatusBadRequest, responseBody{Code: codeInvalidRequest, Message: `send {"addresses":["aether1...", ...]}`})
		return
	}
	o, ok := f.originOf(w, r, caller, body)
	if !ok {
		return
	}
	caller = o.caller
	var addresses, invalid []string
	seen := map[string]bool{}
	for _, a := range req.Addresses {
		switch {
		case !bech32Pattern.MatchString(a):
			invalid = append(invalid, a)
		case !seen[a]:
			seen[a] = true
			addresses = append(addresses, a)
		}
	}
	if len(invalid) > 0 {
		f.reply(w, caller, http.StatusBadRequest, responseBody{Code: codeInvalidAddress, Message: "invalid address format", Invalid: invalid})
		return
	}
	if len(addresses) > f.batchMax {
		f.reply(w, caller, http.StatusBadRequest, responseBody{Code: codeBatchTooLarge,
			Message: fmt.Sprintf("at most %d addresses per batch, got %d", f.batchMax, len(addresses))})
		return
	}
	f.dispense(w, o, addresses, true)
}

func (f *faucetServer) dispense(w http.ResponseWriter, o origin, addresses []string, batch bool) {
	caller := o.caller
	res, waiting, q, ok := f.limits.reserve(caller, addresses)
	var skipped []skippedAddress
	var soonest time.Duration
	for _, a := range addresses {
		if wait, cooling := waiting[a]; cooling {
			skipped = append(skipped, skippedAddress{Address: a, Code: codeAddressCooldown, RetryAfterSeconds: ceilSeconds(wait)})
			if soonest == 0 || wait < soonest {
				soonest = wait
			}
		}
	}
	if !batch {
		skipped = nil // the single-address answer says it all
	}

	if !ok {
		f.reply(w, caller, http.StatusTooManyRequests, responseBody{
			Code: codeCallerLimit,
			Message: fmt.Sprintf("this caller may fund %d more address(es) in this window, which resets in %s",
				max(q.remaining, 0), q.reset.Round(time.Second)),
			RetryAfterSeconds: ceilSeconds(q.reset),
			Skipped:           skipped,
		})
		return
	}
	if len(res.addresses) == 0 {
		msg := fmt.Sprintf("please wait %s before requesting again", soonest.Round(time.Second))
		if batch {
			msg = "every address was funded too recently; see skipped"
		}
		f.reply(w, caller, http.StatusTooManyRequests, responseBody{Code: codeAddressCooldown, Message: msg,
			RetryAfterSeconds: ceilSeconds(soonest), Skipped: skipped})
		return
	}

	// Which of these the chain has never seen: checked before the send,
	// since after it every one of them has an account.
	fresh := map[string]bool{}
	for _, a := range res.addresses {
		exists, err := f.accountExists(a)
		if err != nil {
			log.Printf("faucet couldn't check whether %s is a new wallet: %v", a, err)
			continue
		}
		fresh[a] = !exists
	}

	// Serialize the actual send -- avoids two concurrent requests
	// racing on the faucet account's real sequence number.
	f.mu.Lock()
	txHash, err := f.send(res.addresses)
	f.mu.Unlock()
	if err != nil {
		f.limits.release(res)
		log.Printf("faucet send failed for %v: %v", res.addresses, err)
		f.reply(w, caller, http.StatusInternalServerError, responseBody{Code: codeSendFailed, Message: "send failed, please try again later", Skipped: skipped})
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
	detail, confirmErr := f.confirm(txHash)
	switch {
	case confirmErr != nil:
		// Genuinely inconclusive within the timeout -- deliberately
		// does NOT release the cooldown: the tx may still land later,
		// and releasing here risks letting the same address claim
		// twice for what might turn out to be one real send.
		log.Printf("faucet could not confirm tx %s for %v within %s: %v", txHash, res.addresses, f.confirmTimeout, confirmErr)
		f.reply(w, caller, http.StatusAccepted, responseBody{
			Code:    codePending,
			Message: fmt.Sprintf("broadcast accepted but not yet confirmed on-chain; check tx %s later", txHash),
			TxHash:  txHash,
			Sent:    sentList(batch, res.addresses),
			Skipped: skipped,
		})
		return
	case detail.Code != 0:
		// Passed CheckTx, failed for real at DeliverTx -- a genuine
		// send failure, so the cooldown is released the same as any
		// other failure.
		f.limits.release(res)
		log.Printf("faucet tx %s for %v failed on-chain: %s", txHash, res.addresses, detail.RawLog)
		f.reply(w, caller, http.StatusInternalServerError, responseBody{Code: codeSendFailed, Message: "send failed on-chain, please try again later", TxHash: txHash, Skipped: skipped})
		return
	}

	log.Printf("faucet sent %d uaeth to %v for %s, tx %s (confirmed at height %d)", f.amountUaeth, res.addresses, caller, txHash, detail.Height)
	now := f.now().UTC()
	drips := make([]drip, len(res.addresses))
	for i, a := range res.addresses {
		drips[i] = drip{Time: now, TxHash: txHash, Address: a, AmountUaeth: f.amountUaeth, Strand: strandAether,
			Source: o.source, AgentID: o.agent.ID, AgentName: o.agent.Name, NewWallet: fresh[a]}
	}
	if err := f.ledger.add(drips); err != nil {
		log.Printf("faucet couldn't record drips for tx %s: %v", txHash, err)
	}
	body := responseBody{Success: true, Code: codeSent, Message: "sent", TxHash: txHash,
		Sent: sentList(batch, res.addresses), Skipped: skipped}
	if !batch {
		nw := fresh[res.addresses[0]]
		body.NewWallet = &nw
	}
	f.reply(w, caller, http.StatusOK, body)
}

func sentList(batch bool, addresses []string) []string {
	if batch {
		return addresses
	}
	return nil
}

// GET /status?address=aether1... -- whether a request would be
// funded now, without asking for anything.
func (f *faucetServer) handleStatus(w http.ResponseWriter, r *http.Request) {
	caller := clientIP(r, f.trusted)
	address := r.URL.Query().Get("address")
	setQuotaHeaders(w.Header(), f.limits.quota(caller), f.limits.callerWindow)
	w.Header().Set("Content-Type", "application/json")
	if !bech32Pattern.MatchString(address) {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(responseBody{Code: codeInvalidAddress, Message: "pass ?address=aether1..."})
		return
	}
	wait := f.limits.addressWait(address)
	q := f.limits.quota(caller)
	body := map[string]any{
		"address":             address,
		"eligible":            wait == 0 && (q.limit == 0 || q.remaining > 0),
		"retry_after_seconds": ceilSeconds(wait),
		"amount_uaeth":        f.amountUaeth,
	}
	if q.limit > 0 {
		body["caller_remaining"] = q.remaining
		if q.remaining <= 0 && ceilSeconds(q.reset) > ceilSeconds(wait) {
			body["retry_after_seconds"] = ceilSeconds(q.reset)
		}
	}
	json.NewEncoder(w).Encode(body)
}

// GET / -- what this faucet gives, and its limits.
func (f *faucetServer) handleInfo(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"chain_id":              f.chainID,
		"denom":                 "uaeth",
		"amount_uaeth":          f.amountUaeth,
		"address_cooldown_secs": ceilSeconds(f.limits.cooldown),
		"caller_limit":          f.limits.callerLimit,
		"caller_window_secs":    ceilSeconds(f.limits.callerWindow),
		"batch_max":             f.batchMax,
		"endpoints": map[string]string{
			"request":   `POST /request {"address":"aether1..."}`,
			"batch":     `POST /request/batch {"addresses":["aether1...", ...]}`,
			"status":    "GET /status?address=aether1...",
			"challenge": "GET /challenge?address=aether1...",
			"agents":    `POST /agents {"name":"my-bot","public_key":"<base64 ed25519>"}`,
			"stats":     "GET /stats",
			"drips":     "GET /drips?limit=20",
		},
		"pow_bits":    f.pow.bits,
		"agent_limit": f.limits.agentLimit,
	})
}

// confirmTxOnChain polls GetTransactionByHash until the transaction is
// actually found (meaning it was included in a block and its real
// DeliverTx result is known) or confirmTimeout elapses. See
// dispense's comment for why this exists: SYNC-mode broadcast
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

// sendCoins pays every address the drip amount in one transaction,
// which the chain applies all-or-nothing.
func (f *faucetServer) sendCoins(addresses []string) (string, error) {
	amount := sdk.NewCoins(sdk.NewCoin("uaeth", math.NewInt(f.amountUaeth)))
	msgs := make([]sdk.Msg, len(addresses))
	for i, a := range addresses {
		msgs[i] = &banktypes.MsgSend{FromAddress: f.fromAddr, ToAddress: a, Amount: amount}
	}
	gas := uint64(gasBase + gasPerSend*len(addresses))
	fee := f.gasPrice.Amount.MulInt64(int64(gas)).Ceil().TruncateInt()

	signed, err := f.wal.BuildAndSignMsgsTx(f.fromKey, msgs, wallet.TxParams{
		ChainID:       f.chainID,
		AccountNumber: f.accountNumber,
		Sequence:      f.sequence,
		GasLimit:      gas,
		Fees:          sdk.NewCoins(sdk.NewCoin("uaeth", fee)),
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
	callerLimit := flag.Int("caller-limit", 20, "addresses one caller (client IP) may fund per --caller-window-minutes; 0: no limit")
	callerWindowMinutes := flag.Int("caller-window-minutes", 60, "length of the per-caller window")
	batchMax := flag.Int("batch-max", 10, "most addresses one /request/batch may fund")
	trustedProxies := flag.String("trusted-proxies", "127.0.0.1,::1", "comma-separated addresses of reverse proxies whose X-Forwarded-For names the real caller")
	gasPriceStr := flag.String("gas-price", "0uaeth", "gas price to pay, e.g. 0.0001uaeth for a node with --minimum-gas-prices")
	port := flag.String("port", "8080", "HTTP port to listen on")
	confirmTimeoutSeconds := flag.Int("confirm-timeout-seconds", 90, "how long to wait for a broadcast tx to actually land on-chain before giving up (comfortably more than one block interval)")
	confirmPollSeconds := flag.Int("confirm-poll-seconds", 3, "how often to poll for on-chain confirmation while waiting")
	dripLog := flag.String("drip-log", "", "file keeping every drip for /stats and /drips (default <keyring dir>/faucet-drips.jsonl)")
	agentRegistryPath := flag.String("agent-registry", "", "file keeping registered agent keys (default <keyring dir>/faucet-agents.json)")
	agentLimit := flag.Int("agent-limit", 100, "addresses one registered agent key may fund per --caller-window-minutes")
	powBits := flag.Int("pow-bits", 18, "leading zero bits a browser's /challenge solution needs (18: about a second in a browser)")
	flag.Parse()

	gasPrice, err := sdk.ParseDecCoin(*gasPriceStr)
	if err != nil || gasPrice.Denom != "uaeth" {
		log.Fatalf("--gas-price %q: want e.g. 0.0001uaeth (%v)", *gasPriceStr, err)
	}
	if *batchMax < 1 {
		*batchMax = 1
	}

	dir := *keyringDir
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			log.Fatalf("failed to determine home directory: %v", err)
		}
		dir = home + "/.aether"
	}

	if *dripLog == "" {
		*dripLog = dir + "/faucet-drips.jsonl"
	}
	if *agentRegistryPath == "" {
		*agentRegistryPath = dir + "/faucet-agents.json"
	}
	drips, err := openLedger(*dripLog)
	if err != nil {
		log.Fatalf("--drip-log %s: %v", *dripLog, err)
	}
	agents, err := openAgentRegistry(*agentRegistryPath)
	if err != nil {
		log.Fatalf("--agent-registry %s: %v", *agentRegistryPath, err)
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

	limits := newLimiter(time.Duration(*cooldownMinutes)*time.Minute, *callerLimit,
		time.Duration(*callerWindowMinutes)*time.Minute)
	limits.agentLimit = *agentLimit
	server := &faucetServer{
		limits:              limits,
		ledger:              drips,
		pow:                 newPowIssuer(*powBits),
		agents:              agents,
		regLimits:           newLimiter(0, 5, time.Hour),
		now:                 time.Now,
		trusted:             parseTrusted(*trustedProxies),
		batchMax:            *batchMax,
		wal:                 wal,
		client:              client,
		fromKey:             *fromKey,
		fromAddr:            account.Address,
		chainID:             *chainID,
		amountUaeth:         *amount,
		gasPrice:            gasPrice,
		accountNumber:       accountNumber,
		sequence:            sequence,
		confirmTimeout:      time.Duration(*confirmTimeoutSeconds) * time.Second,
		confirmPollInterval: time.Duration(*confirmPollSeconds) * time.Second,
	}
	server.send = server.sendCoins
	server.confirm = server.confirmTxOnChain
	server.accountExists = func(address string) (bool, error) {
		_, _, err := client.GetAccountInfo(address)
		if status.Code(err) == codes.NotFound {
			return false, nil
		}
		return err == nil, err
	}
	server.balance = cachedBalance(30*time.Second, func() (int64, error) {
		coins, err := client.GetBalance(account.Address)
		if err != nil {
			return 0, err
		}
		return coins.AmountOf("uaeth").Int64(), nil
	})

	log.Printf("Aether faucet listening on :%s (dispensing %d uaeth per address, %d-minute cooldown, %d addresses per caller per %d minutes, batches of up to %d, from %s on chain %q, starting sequence %d)",
		*port, *amount, *cooldownMinutes, *callerLimit, *callerWindowMinutes, *batchMax, account.Address, *chainID, sequence)
	log.Fatal(http.ListenAndServe(":"+*port, server.routes()))
}

// routes deliberately uses its own dedicated mux, never the shared
// global http.DefaultServeMux -- a real, severe issue found live: some
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
func (f *faucetServer) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/request", f.handleRequest)
	mux.HandleFunc("/request/batch", f.handleBatch)
	mux.HandleFunc("/status", f.handleStatus)
	mux.HandleFunc("/challenge", f.handleChallenge)
	mux.HandleFunc("/agents", f.handleAgents)
	mux.HandleFunc("/stats", f.handleStats)
	mux.HandleFunc("/drips", f.handleDrips)
	mux.HandleFunc("/", f.handleInfo)
	return mux
}
