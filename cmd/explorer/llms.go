package main

import (
	"fmt"
	"net/http"
	"strings"
)

// --- GET /llms.txt ---
//
// The llms.txt convention (https://llmstxt.org): a short Markdown file at
// a site's root telling a language model what the site is and where the
// machine-readable parts are. Built per request from the same endpoints
// the agent card advertises, so it names this deployment's hosts.

// faucetEndpoints derives the batch and status URLs from the faucet's
// /request URL.
func faucetEndpoints(requestURL string) (batch, status string) {
	base, ok := strings.CutSuffix(requestURL, "/request")
	if !ok {
		return "", ""
	}
	return base + "/request/batch", base + "/status"
}

func handleLLMsTxt(w http.ResponseWriter, r *http.Request) {
	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	host := scheme + "://" + r.Host

	var b strings.Builder
	p := func(format string, args ...any) { fmt.Fprintf(&b, format+"\n", args...) }

	p("# Aether")
	p("")
	p("> Aether is a proof-of-work Cosmos SDK chain with post-quantum (ML-DSA-44) signatures, built for AI agents to pay and get paid. This explorer serves chain %s. It's a testnet: use disposable keys, never anything of value; there's been no independent security audit yet.", chainID)
	p("")
	p("Amounts are integers in uaeth (1 AETH = 1,000,000 uaeth). Addresses start with `aether1`. Every account transaction is signed with ML-DSA-44, so generic Cosmos wallets and secp256k1 signers don't work: use the tools below.")
	p("")
	p("## Start here")
	p("")
	p("- [Start page](%s/start.md): a key, test funds, a balance and a first payment, copy-paste for MCP, TypeScript and Python, with this deployment's URLs", host)
	p("- [Agent card](%s/api/agents): chain ID, public endpoints, the faucet, the MCP wallet and its tools, as one JSON object", host)
	p("- [Explorer API](%s/api/openapi.json): OpenAPI 3.1 for every read endpoint (blocks, transactions, balances, grants, mining, governance, services, IBC)", host)
	p("- [AI agents: start here](%s#ai-agents-start-here): get an address, get test funds, make a first payment", repoURL)
	p("- [MCP wallet](%s#ai-agent-wallet-mcp): `go install github.com/whoyoujoshin/aether/cmd/agentmcp@latest && agentmcp init`, or the `aether-wallet.mcpb` bundle from the releases; MCP Registry name `io.github.whoyoujoshin/aether-wallet`. Tools: %s", repoURL, strings.Join(agentmcpTools, ", "))
	p("- [MCP server card](%s/.well-known/mcp/server-card.json): the wallet's registry name, install lines, tools, and the public read-only endpoint if there is one", host)
	p("- [API stability](%s/blob/main/docs/API-STABILITY.md): which of these URLs and fields only ever gain things, and how anything else changes", repoURL)
	p("")
	p("## Endpoints")
	p("")
	if publicRPC != "" {
		p("- CometBFT RPC: %s", publicRPC)
	}
	if publicGRPC != "" {
		p("- gRPC (TLS): %s", publicGRPC)
	}
	if publicFaucet != "" {
		p("- Faucet: `POST %s` with `{\"address\":\"aether1...\"}`", publicFaucet)
		if batch, status := faucetEndpoints(publicFaucet); batch != "" {
			p("- Faucet, several wallets in one transaction: `POST %s` with `{\"addresses\":[...]}`; check before asking: `GET %s?address=aether1...`. Answers carry a stable `code`, `RateLimit-*` headers and, on 429, `Retry-After`", batch, status)
		}
	}
	p("- Explorer: %s", host)
	p("")
	p("## Build on it")
	p("")
	p("- [Agent integration](%s/blob/main/docs/AGENT_INTEGRATION.md): what agents can do today, spend-limited grants, key custody", repoURL)
	p("- [Paid APIs](%s/api/services): services announced on chain, with prices and payment activity; charge for your own with `cmd/paywall` (x402-style HTTP 402)", host)
	p("- [Clients](%s/tree/main/clients): TypeScript and Python: keys, payments, paid APIs (buying and selling)", repoURL)
	p("- [Miner alerts](%s#miner-and-validator-alerts): `cmd/minerwatch` pushes signed webhooks when a PoW submission lands or fails, the validator set picks or drops you, an epoch is about to close without you on track, your balance runs low, or the chain stalls", repoURL)
	p("")
	p("## Optional")
	p("")
	p("- [Whitepaper](%s/blob/main/docs/WHITEPAPER.md): consensus, economics, cryptography, known limitations", repoURL)
	p("- [IBC](%s/blob/main/docs/IBC.md): ICS-20 transfers and ICS-27 interchain accounts", repoURL)
	p("- [Account abstraction](%s/blob/main/docs/ACCOUNT_ABSTRACTION.md): session keys and guardian thresholds, live since block 122,000", repoURL)

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Write([]byte(b.String()))
}
