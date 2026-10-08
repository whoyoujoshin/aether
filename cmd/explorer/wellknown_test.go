package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func getServerCard(t *testing.T, path string) serverCardDTO {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", path, nil)
	req.Host = "explorer.example"
	req.Header.Set("X-Forwarded-Proto", "https")
	withCORS(handleServerCard)(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "*", rec.Header().Get("Access-Control-Allow-Origin"))
	require.Equal(t, "application/json", rec.Header().Get("Content-Type"))
	var card serverCardDTO
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &card))
	return card
}

func TestServerCard(t *testing.T) {
	saved, savedChain := publicMCP, chainID
	t.Cleanup(func() { publicMCP, chainID = saved, savedChain })
	chainID = "aether-testnet-1"

	// No --public-mcp: nothing claims a remote that may not be routed.
	publicMCP = ""
	card := getServerCard(t, "/.well-known/mcp/server-card.json")
	require.Equal(t, "io.github.whoyoujoshin/aether-wallet", card.Name)
	require.Equal(t, "aether-wallet", card.ServerInfo.Name)
	require.False(t, card.Authentication.Required)
	require.Empty(t, card.Remotes)
	require.Equal(t, agentmcpTools, card.Install.Tools)
	require.Contains(t, card.Install.Go, "@latest")
	require.Equal(t, "https://explorer.example/api/agents", card.Chain.AgentCard)
	require.Equal(t, "aether-testnet-1", card.Chain.ChainID)

	// The empty list is [] in JSON, not null.
	rec := httptest.NewRecorder()
	handleServerCard(rec, httptest.NewRequest("GET", "/.well-known/mcp.json", nil))
	require.Contains(t, rec.Body.String(), `"remotes":[]`)

	// With one, it's listed read-only with the public tools, and the agent
	// card points at it too.
	publicMCP = "https://explorer.example/mcp"
	card = getServerCard(t, "/.well-known/mcp.json")
	require.Equal(t, []serverCardRemoteDTO{{Type: "streamable-http", URL: publicMCP, ReadOnly: true, Tools: publicMCPTools}}, card.Remotes)
	for _, tool := range publicMCPTools {
		require.Contains(t, agentmcpTools, tool, "a public tool is also a wallet tool")
	}
}

func TestAgentCard_LinksServerCard(t *testing.T) {
	saved := []string{rpcEndpoint, publicMCP}
	t.Cleanup(func() { rpcEndpoint, publicMCP = saved[0], saved[1] })
	rpcEndpoint, publicMCP = "http://127.0.0.1:1", "https://explorer.example/mcp" // node down: the card still answers

	rec := httptest.NewRecorder()
	handleAgents(rec, httptest.NewRequest("GET", "/api/agents", nil))
	var card agentCardDTO
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &card))
	require.Equal(t, "/.well-known/mcp/server-card.json", card.MCP.ServerCard)
	require.Equal(t, publicMCP, card.MCP.Remote)

	rec = httptest.NewRecorder()
	handleLLMsTxt(rec, httptest.NewRequest("GET", "/llms.txt", nil))
	require.Contains(t, rec.Body.String(), "/.well-known/mcp/server-card.json")
}

// publicMCPTools must be exactly agentmcp's publicToolNames, in order.
func TestServerCard_ListsEveryPublicTool(t *testing.T) {
	src, err := os.ReadFile("../agentmcp/public.go")
	require.NoError(t, err)
	block := regexp.MustCompile(`(?s)var publicToolNames = \[\]string\{(.*?)\}`).FindSubmatch(src)
	require.NotNil(t, block, "cmd/agentmcp/public.go still declares publicToolNames")
	var names []string
	for _, m := range regexp.MustCompile(`"([a-z_]+)"`).FindAllSubmatch(block[1], -1) {
		names = append(names, string(m[1]))
	}
	require.Equal(t, names, publicMCPTools, strings.Join(names, ", "))
}
