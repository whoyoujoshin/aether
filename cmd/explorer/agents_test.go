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
	rpcEndpoint, chainID = node.URL, "aether-testnet-1"
	publicRPC, publicGRPC, publicSeed, publicFaucet = "", "", "", faucet.URL
	resolvePublicEndpoints()

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
	card := get()
	require.Equal(t, "aether-testnet-1", card.ChainID)
	require.Equal(t, int64(113900), card.Height)
	require.Equal(t, agentEndpointsDTO{RPC: testnetRPC, GRPC: testnetGRPC, Faucet: faucet.URL, Seed: testnetSeed,
		Explorer: "http://explorer.example:8081"}, card.Endpoints, "flags win; the rest default to the public testnet")
	require.True(t, card.Authz.Active)
	require.NotNil(t, card.Faucet)
	require.True(t, *card.Faucet.Reachable)
	require.Contains(t, card.MCP.Install, "@main")
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

// The card must list exactly the tools agentmcp registers.
func TestAgentCard_ListsEveryAgentmcpTool(t *testing.T) {
	files, err := filepath.Glob("../agentmcp/*.go")
	require.NoError(t, err)
	re := regexp.MustCompile(`Name:\s+"([a-z_]+)"`)
	var registered []string
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		require.NoError(t, err)
		for _, m := range re.FindAllStringSubmatch(string(src), -1) {
			registered = append(registered, m[1])
		}
	}
	listed := append([]string(nil), agentmcpTools...)
	sort.Strings(registered)
	sort.Strings(listed)
	require.NotEmpty(t, registered)
	require.Equal(t, registered, listed)
}
