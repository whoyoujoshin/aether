package main

import (
	"flag"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

var updateStart = flag.Bool("update-docs", false, "rewrite docs/START.md from the /start.md template")

// docs/START.md is /start.md as the public testnet's explorer serves it.
// Edit the template in start.go, then: go test ./cmd/explorer -run TestStartDoc -update-docs
func TestStartDoc_MatchesTestnetRendering(t *testing.T) {
	got := renderStart(startParams{
		ChainID:   "aether-testnet-1",
		Explorer:  testnetExplorer,
		RPC:       testnetRPC,
		Faucet:    testnetFaucet,
		Recipient: testCounterparty,
	})
	const path = "../../docs/START.md"
	if *updateStart {
		require.NoError(t, os.WriteFile(path, []byte(got), 0o644))
	}
	want, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, string(want), got, "docs/START.md is stale: go test ./cmd/explorer -run TestStartDoc -update-docs")
}

func TestStartMd_NamesThisDeployment(t *testing.T) {
	saved := []string{chainID, publicRPC, publicFaucet}
	t.Cleanup(func() { chainID, publicRPC, publicFaucet = saved[0], saved[1], saved[2] })
	chainID, publicRPC, publicFaucet = "aether-devnet-7", "http://127.0.0.1:26657/", "http://127.0.0.1:4500/request"

	req := httptest.NewRequest("GET", "/start.md", nil)
	req.Host = "explorer.example:8081"
	req.Header.Set("X-Forwarded-Proto", "https")
	rec := httptest.NewRecorder()
	handleStartMd(rec, req)

	require.Equal(t, 200, rec.Code)
	require.Equal(t, "text/markdown; charset=utf-8", rec.Header().Get("Content-Type"))
	body := rec.Body.String()
	for _, want := range []string{
		"`aether-devnet-7`",
		`rpc: "http://127.0.0.1:26657"`, // trailing slash trimmed
		`fetch("http://127.0.0.1:4500/request"`,
		"https://explorer.example:8081/api/address?addr=",
		"https://explorer.example:8081/tx/<hash>",
		testCounterparty,
	} {
		require.Contains(t, body, want)
	}
	require.NotContains(t, body, "157-245-252-221", "a devnet's page must not point at the testnet")
	require.NotContains(t, body, "<no value>")
	// The three paths, in the order the page promises them.
	mcp, ts, py := strings.Index(body, "## 1. An MCP agent"), strings.Index(body, "## 2. TypeScript"), strings.Index(body, "## 3. Python")
	require.True(t, 0 < mcp && mcp < ts && ts < py, "sections out of order")
}
