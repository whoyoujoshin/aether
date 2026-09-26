package main

import (
	"cmp"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/whoyoujoshin/aether/app"
)

// --- GET /api/agents ---
//
// The agent card: everything a bot needs to start on this chain -- chain ID,
// public endpoints, the faucet, the MCP wallet and its tools -- in one
// machine-readable response. The explorer's /agents page renders it.

// The public testnet's endpoints: the defaults on aether-testnet-1.
const (
	testnetRPC    = "http://157.245.252.221:26657"
	testnetGRPC   = "157.245.252.221:9090"
	testnetFaucet = "http://157.245.252.221:8080/request"
	testnetSeed   = "dfa6aae4b7bfd5b0eb1e22fabbae3e83a475b938@157.245.252.221:26656"
)

// Public endpoints to advertise (not the ones the explorer itself queries,
// which are usually localhost). Set by flags; see resolvePublicEndpoints.
var publicRPC, publicGRPC, publicFaucet, publicSeed string

// agentmcpTools are the MCP wallet's tools; TestAgentCard_ListsEveryAgentmcpTool
// keeps this in step with cmd/agentmcp.
var agentmcpTools = []string{
	"get_agent_address", "get_balance", "get_spending_status", "request_testnet_funds",
	"send_aeth", "get_transaction_status", "wait_for_transaction", "get_transaction_history",
	"create_invoice", "wait_for_payment",
	"fetch_paid", "list_purchases", "list_prepaid_balances", "withdraw_prepaid",
	"find_services", "rate_service", "announce_service",
}

const repoURL = "https://github.com/whoyoujoshin/aether"

func resolvePublicEndpoints() {
	if chainID != "aether-testnet-1" {
		return
	}
	publicRPC = cmp.Or(publicRPC, testnetRPC)
	publicGRPC = cmp.Or(publicGRPC, testnetGRPC)
	publicFaucet = cmp.Or(publicFaucet, testnetFaucet)
	publicSeed = cmp.Or(publicSeed, testnetSeed)
}

type agentEndpointsDTO struct {
	RPC      string `json:"rpc"`
	GRPC     string `json:"grpc"`
	Faucet   string `json:"faucet"`
	Explorer string `json:"explorer"`
	Seed     string `json:"seed"`
}

type agentFaucetDTO struct {
	URL       string `json:"url"`
	Request   string `json:"request"`   // how to ask it for funds
	Reachable *bool  `json:"reachable"` // nil: not checked
}

type agentAuthzDTO struct {
	ActivationHeight int64 `json:"activationHeight"`
	Active           bool  `json:"active"`
}

type agentMCPDTO struct {
	Install string   `json:"install"`
	Init    string   `json:"init"`
	Tools   []string `json:"tools"`
}

type agentCardDTO struct {
	Name          string            `json:"name"`
	ChainID       string            `json:"chainId"`
	Height        int64             `json:"height"` // 0: the node didn't answer
	AddressPrefix string            `json:"addressPrefix"`
	Denom         string            `json:"denom"`
	DisplayDenom  string            `json:"displayDenom"`
	Decimals      int               `json:"decimals"`
	Signatures    string            `json:"signatures"`
	Endpoints     agentEndpointsDTO `json:"endpoints"`
	Faucet        *agentFaucetDTO   `json:"faucet"` // nil: no faucet
	Authz         agentAuthzDTO     `json:"authz"`
	MCP           agentMCPDTO       `json:"mcp"`
	Payments      []string          `json:"paymentSchemes"`
	Docs          map[string]string `json:"docs"`
	Warnings      []string          `json:"warnings"`
}

// faucetReachable reports whether the faucet answers HTTP at all (a GET to
// its POST-only endpoint: no funds are requested), cached for a minute.
var faucetCheck struct {
	sync.Mutex
	url string
	at  time.Time
	ok  bool
}

var faucetHTTP = &http.Client{Timeout: 3 * time.Second}

func faucetReachable(url string) bool {
	faucetCheck.Lock()
	defer faucetCheck.Unlock()
	if faucetCheck.url == url && time.Since(faucetCheck.at) < time.Minute {
		return faucetCheck.ok
	}
	ok := false
	if resp, err := faucetHTTP.Get(url); err == nil {
		resp.Body.Close()
		ok = resp.StatusCode < 500
	}
	faucetCheck.url, faucetCheck.at, faucetCheck.ok = url, time.Now(), ok
	return ok
}

func handleAgents(w http.ResponseWriter, r *http.Request) {
	height, _ := fetchLatestHeight(rpcEndpoint) // the card is still useful when the node is down

	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	card := agentCardDTO{
		Name:          "Aether",
		ChainID:       chainID,
		Height:        height,
		AddressPrefix: "aether",
		Denom:         "uaeth",
		DisplayDenom:  "AETH",
		Decimals:      6,
		Signatures:    "ML-DSA-44 (post-quantum) for every account transaction",
		Endpoints: agentEndpointsDTO{
			RPC: publicRPC, GRPC: publicGRPC, Faucet: publicFaucet, Seed: publicSeed,
			Explorer: scheme + "://" + r.Host,
		},
		Authz: agentAuthzDTO{
			ActivationHeight: app.AuthzFeegrantActivationHeight,
			Active:           height >= app.AuthzFeegrantActivationHeight,
		},
		MCP: agentMCPDTO{
			Install: "go install github.com/whoyoujoshin/aether/cmd/agentmcp@main",
			Init:    "agentmcp init",
			Tools:   agentmcpTools,
		},
		Payments: []string{"aether-memo", "aether-prepaid", "aether-pull"},
		Docs: map[string]string{
			"start":       repoURL + "#ai-agents-start-here",
			"mcp":         repoURL + "#ai-agent-wallet-mcp",
			"integration": repoURL + "/blob/main/docs/AGENT_INTEGRATION.md",
			"services":    "/api/services",
		},
		Warnings: []string{
			"Testnet: use disposable keys and never anything of value.",
			"No independent security audit yet.",
		},
	}
	if publicFaucet != "" {
		ok := faucetReachable(publicFaucet)
		card.Faucet = &agentFaucetDTO{
			URL:       publicFaucet,
			Request:   `POST {"address":"aether1..."} as application/json`,
			Reachable: &ok,
		}
	}
	if strings.HasPrefix(publicRPC, "http://") || strings.HasPrefix(publicFaucet, "http://") {
		card.Warnings = append(card.Warnings, "Endpoints are plain HTTP (no TLS): some sandboxes won't reach them.")
	}
	writeJSON(w, http.StatusOK, card)
}
