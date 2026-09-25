package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func fakeFaucet(t *testing.T, status int, body string) (*httptest.Server, *atomic.Value) {
	var got atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]string
		_ = json.NewDecoder(r.Body).Decode(&req)
		got.Store(req["address"])
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, &got
}

func TestRequestTestnetFunds(t *testing.T) {
	setupAgent(t)
	defer func(u string) { faucetURL = u }(faucetURL)
	w, _ := newWallet()
	agent, _ := getOrCreateAgentAccount(w)

	srv, got := fakeFaucet(t, http.StatusOK, `{"success":true,"message":"sent 10 AETH","tx_hash":"ABC"}`)
	faucetURL = srv.URL
	_, out, err := toolRequestTestnetFunds(context.Background(), nil, requestFundsInput{})
	require.NoError(t, err)
	require.Equal(t, requestFundsOutput{Status: "sent", Address: agent.Address, TxHash: "ABC", Message: "sent 10 AETH"}, out)
	require.Equal(t, agent.Address, got.Load(), "funds go to the agent's own address")

	srv, _ = fakeFaucet(t, http.StatusAccepted, `{"success":false,"message":"not yet confirmed","tx_hash":"DEF"}`)
	faucetURL = srv.URL
	_, out, err = toolRequestTestnetFunds(context.Background(), nil, requestFundsInput{})
	require.NoError(t, err)
	require.Equal(t, "pending", out.Status)

	srv, _ = fakeFaucet(t, http.StatusTooManyRequests, `{"success":false,"message":"please wait 23h59m"}`)
	faucetURL = srv.URL
	_, _, err = toolRequestTestnetFunds(context.Background(), nil, requestFundsInput{})
	ae := requireCode(t, err, codeFaucetRateLimited)
	require.Contains(t, ae.Message, "23h59m")

	faucetURL = "http://127.0.0.1:1"
	_, _, err = toolRequestTestnetFunds(context.Background(), nil, requestFundsInput{})
	ae = requireCode(t, err, codeFaucetUnavailable)
	require.True(t, ae.Retryable)

	faucetURL = ""
	_, _, err = toolRequestTestnetFunds(context.Background(), nil, requestFundsInput{})
	requireCode(t, err, codeFaucetUnavailable)
}

func TestInit_CreatesFundsAndPrintsClientConfig(t *testing.T) {
	setupAgent(t)
	dir := t.TempDir()
	srv, got := fakeFaucet(t, http.StatusOK, `{"success":true,"message":"sent"}`)
	var buf bytes.Buffer
	origOut, origWait := initOut, fundsWait
	initOut, fundsWait = &buf, 10*time.Millisecond
	t.Cleanup(func() { initOut, fundsWait = origOut, origWait })

	keys := filepath.Join(dir, "agent keys")
	require.NoError(t, runInit([]string{"--keyring-dir", keys, "--faucet", srv.URL, "--grpc", "127.0.0.1:1"}))
	out := buf.String()
	require.Contains(t, out, "Created the agent's account aether1")
	require.Contains(t, out, "recovery phrase")
	addr := strings.Fields(strings.SplitN(out, "account ", 2)[1])[0]
	require.Equal(t, addr, got.Load(), "the new account is what gets funded")
	require.Contains(t, out, "claude mcp add aether-wallet -- agentmcp", "a go-test binary isn't a stable command: suggest installing")
	require.Contains(t, out, "'"+keys+"'", "paths with spaces are quoted for the shell")

	// The printed JSON is valid config naming the same keyring.
	start := strings.Index(out, "{")
	var cfg struct {
		MCPServers map[string]struct {
			Command string   `json:"command"`
			Args    []string `json:"args"`
		} `json:"mcpServers"`
	}
	require.NoError(t, json.NewDecoder(strings.NewReader(out[start:])).Decode(&cfg))
	require.Contains(t, cfg.MCPServers["aether-wallet"].Args, keys)

	// Running it again reuses the account and never prints the phrase again.
	buf.Reset()
	require.NoError(t, runInit([]string{"--keyring-dir", keys, "--no-faucet"}))
	require.Contains(t, buf.String(), "Using the existing agent account "+addr)
	require.NotContains(t, buf.String(), "recovery phrase")
}
