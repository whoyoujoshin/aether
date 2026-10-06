package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAgentCard(t *testing.T) {
	node := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"result":{"sync_info":{"latest_block_height":"113900"}}}`))
	}))
	defer node.Close()
	faucetGets := 0
	faucet := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method, "checking the faucet never asks it for funds")
		faucetGets++
		w.WriteHeader(http.StatusMethodNotAllowed)
	}))
	defer faucet.Close()

	saved := []string{rpcEndpoint, chainID, publicRPC, publicGRPC, publicFaucet, publicSeed}
	t.Cleanup(func() {
		rpcEndpoint, chainID, publicRPC, publicGRPC, publicFaucet, publicSeed = saved[0], saved[1], saved[2], saved[3], saved[4], saved[5]
	})

	get := func() agentCardDTO {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/api/agents", nil)
		req.Host = "explorer.example:8081"
		handleAgents(rec, req)
		require.Equal(t, http.StatusOK, rec.Code)
		var card agentCardDTO
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &card))
		return card
	}

	// Full defaults: the testnet's own endpoints, all TLS since scripts/tls
	// went live. No plain-HTTP warning here -- that's the point of that work.
	rpcEndpoint, chainID = node.URL, "aether-testnet-1"
	publicRPC, publicGRPC, publicSeed, publicFaucet = "", "", "", ""
	resolvePublicEndpoints()
	card := get()
	require.Equal(t, "aether-testnet-1", card.ChainID)
	require.Equal(t, int64(113900), card.Height)
	require.Equal(t, agentEndpointsDTO{RPC: testnetRPC, GRPC: testnetGRPC, Faucet: testnetFaucet, Seed: testnetSeed,
		Explorer: "http://explorer.example:8081"}, card.Endpoints)
	require.True(t, strings.HasPrefix(card.Endpoints.RPC, "https://"), "the default is TLS now, not the plain-HTTP seed port")
	require.NotContains(t, card.Warnings, "Endpoints are plain HTTP (no TLS): some sandboxes won't reach them.")

	// An operator who overrides the faucet with a plain-HTTP one still gets
	// warned -- the check is real, not just satisfied by the new defaults.
	publicFaucet = faucet.URL
	resolvePublicEndpoints()
	card = get()
	require.Equal(t, faucet.URL, card.Endpoints.Faucet, "flags win over the default")
	require.True(t, card.Authz.Active)
	require.NotNil(t, card.Faucet)
	require.True(t, *card.Faucet.Reachable)
	require.Contains(t, card.MCP.Install, "@latest")
	require.Contains(t, card.Warnings, "Endpoints are plain HTTP (no TLS): some sandboxes won't reach them.")
	get()
	require.Equal(t, 1, faucetGets, "the faucet check is cached")

	// Another chain advertises only what it's told to.
	chainID, publicRPC, publicGRPC, publicFaucet, publicSeed = "aether-devnet", "", "", "", ""
	resolvePublicEndpoints()
	card = get()
	require.Empty(t, card.Endpoints.RPC)
	require.Nil(t, card.Faucet)
}

// The card must list exactly the tools agentmcp registers, each once.
// Public mode (--http) registers its read-only tools again under the
// same names, so a name found twice is still one tool.
func TestAgentCard_ListsEveryAgentmcpTool(t *testing.T) {
	files, err := filepath.Glob("../agentmcp/*.go")
	require.NoError(t, err)
	re := regexp.MustCompile(`Name:\s+"([a-z_]+)"`)
	seen := map[string]bool{}
	var registered []string
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		require.NoError(t, err)
		for _, m := range re.FindAllStringSubmatch(string(src), -1) {
			if !seen[m[1]] {
				seen[m[1]] = true
				registered = append(registered, m[1])
			}
		}
	}
	listed := append([]string(nil), agentmcpTools...)
	sort.Strings(registered)
	sort.Strings(listed)
	require.NotEmpty(t, registered)
	require.Equal(t, registered, listed)
}
