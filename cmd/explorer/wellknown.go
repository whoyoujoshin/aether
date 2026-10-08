package main

import (
	"net/http"
)

// --- GET /.well-known/mcp/server-card.json (and /.well-known/mcp.json) ---
//
// An MCP server card: what an MCP directory or client needs to list or
// connect to the Aether wallet without running it first -- its MCP
// Registry name, how to install it, the public read-only endpoint (when
// --public-mcp names one) and the tools on each. Directories that scan a
// site for MCP servers look under /.well-known/mcp.
//
// There's deliberately no A2A /.well-known/agent-card.json: that card's
// url promises an A2A endpoint (message/send and the rest), and nothing
// here speaks A2A. The chain's own agent card is /api/agents.

// publicMCP is the public read-only Streamable HTTP endpoint (cmd/agentmcp
// --http behind the reverse proxy) to advertise. Empty: none is, because
// the explorer can't tell from here whether the proxy routes /mcp to it.
var publicMCP string

// publicMCPTools are the tools agentmcp --http registers;
// TestServerCard_ListsEveryPublicTool keeps this in step with
// cmd/agentmcp's publicToolNames.
var publicMCPTools = []string{
	"get_balance", "get_miner_status", "get_account_authenticators", "get_transaction_status", "find_services",
}

const mcpRegistryName = "io.github.whoyoujoshin/aether-wallet"

type serverCardInfoDTO struct {
	Name  string `json:"name"`
	Title string `json:"title"`
}

type serverCardRepoDTO struct {
	URL    string `json:"url"`
	Source string `json:"source"`
}

type serverCardAuthDTO struct {
	Required bool   `json:"required"`
	Note     string `json:"note"`
}

type serverCardRemoteDTO struct {
	Type     string   `json:"type"`
	URL      string   `json:"url"`
	ReadOnly bool     `json:"readOnly"`
	Tools    []string `json:"tools"`
}

type serverCardInstallDTO struct {
	Go       string   `json:"go"`
	Init     string   `json:"init"`
	Bundle   string   `json:"bundle"`   // Claude Desktop (.mcpb), from the latest release
	Registry string   `json:"registry"` // the MCP Registry entry's name
	Tools    []string `json:"tools"`
}

type serverCardChainDTO struct {
	ChainID       string `json:"chainId"`
	AddressPrefix string `json:"addressPrefix"`
	Denom         string `json:"denom"`
	AgentCard     string `json:"agentCard"`
	Start         string `json:"start"`
}

type serverCardDTO struct {
	ServerInfo     serverCardInfoDTO     `json:"serverInfo"`
	Name           string                `json:"name"`
	Description    string                `json:"description"`
	WebsiteURL     string                `json:"websiteUrl"`
	Repository     serverCardRepoDTO     `json:"repository"`
	Authentication serverCardAuthDTO     `json:"authentication"`
	Remotes        []serverCardRemoteDTO `json:"remotes"`
	Install        serverCardInstallDTO  `json:"install"`
	Chain          serverCardChainDTO    `json:"chain"`
	Warnings       []string              `json:"warnings"`
}

func handleServerCard(w http.ResponseWriter, r *http.Request) {
	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	host := scheme + "://" + r.Host

	card := serverCardDTO{
		ServerInfo: serverCardInfoDTO{Name: "aether-wallet", Title: "Aether wallet"},
		Name:       mcpRegistryName,
		Description: "A wallet for AI agents on Aether, a proof-of-work chain with post-quantum (ML-DSA-44) signatures: " +
			"get an address and test funds, send and receive payments in AETH or USDC under spending limits, " +
			"pay for APIs that answer HTTP 402, sell your own, and use on-chain escrow.",
		WebsiteURL:     host,
		Repository:     serverCardRepoDTO{URL: repoURL, Source: "github"},
		Authentication: serverCardAuthDTO{Note: "No account or API key. The installed wallet keeps its own key on the agent's machine; the public endpoint holds no key and only reads."},
		Remotes:        []serverCardRemoteDTO{},
		Install: serverCardInstallDTO{
			Go:       "go install github.com/whoyoujoshin/aether/cmd/agentmcp@latest",
			Init:     "agentmcp init",
			Bundle:   repoURL + "/releases/latest",
			Registry: mcpRegistryName,
			Tools:    agentmcpTools,
		},
		Chain: serverCardChainDTO{
			ChainID:       chainID,
			AddressPrefix: "aether",
			Denom:         "uaeth",
			AgentCard:     host + "/api/agents",
			Start:         host + "/start.md",
		},
		Warnings: []string{
			"Testnet: use disposable keys and never anything of value.",
			"No independent security audit yet.",
		},
	}
	if publicMCP != "" {
		card.Remotes = append(card.Remotes, serverCardRemoteDTO{
			Type: "streamable-http", URL: publicMCP, ReadOnly: true, Tools: publicMCPTools,
		})
	}
	writeJSON(w, http.StatusOK, card)
}
