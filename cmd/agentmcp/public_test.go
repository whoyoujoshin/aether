package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"

	"github.com/whoyoujoshin/aether/directory"
	"github.com/whoyoujoshin/aether/wallet"
)

func sortedCopy(in []string) []string {
	out := append([]string(nil), in...)
	slices.Sort(out)
	return out
}

func toolNames(t *testing.T, server *mcp.Server) []string {
	t.Helper()
	ctx := context.Background()
	serverT, clientT := mcp.NewInMemoryTransports()
	ss, err := server.Connect(ctx, serverT, nil)
	require.NoError(t, err)
	t.Cleanup(func() { ss.Close() })
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "public-test"}, nil).Connect(ctx, clientT, nil)
	require.NoError(t, err)
	t.Cleanup(func() { cs.Close() })
	var names []string
	for tool, err := range cs.Tools(ctx, nil) {
		require.NoError(t, err)
		names = append(names, tool.Name)
	}
	return names
}

func TestPublicServerTools(t *testing.T) {
	names := toolNames(t, newPublicServer())
	require.Equal(t, sortedCopy(publicToolNames), sortedCopy(names))

	// The wallet server's spend, sign, key-creation and faucet tools
	// must not be registered, even under another name we already ship.
	for _, forbidden := range []string{
		"get_agent_address",
		"get_spending_status",
		"send_aeth",
		"wait_for_payment",
		"create_invoice",
		"fetch_paid",
		"rate_service",
		"announce_service",
		"withdraw_prepaid",
		"list_purchases",
		"list_prepaid_balances",
		"request_testnet_funds",
		"create_escrow",
		"release_escrow",
		"refund_escrow",
		"get_escrow", // opens the keyring to label what this agent may do
		"list_escrows",
		"get_transaction_history",
	} {
		require.NotContains(t, names, forbidden)
	}

	walletTools := toolNames(t, newServer())
	require.Contains(t, walletTools, "send_aeth")
	require.Contains(t, walletTools, "request_testnet_funds")
	require.Contains(t, walletTools, "find_services")
}

func TestPublicAddressRequiredDoesNotOpenKeyring(t *testing.T) {
	kr := filepath.Join(t.TempDir(), "keyring")
	prev := keyringDir
	keyringDir = kr
	t.Cleanup(func() { keyringDir = prev })

	_, _, err := toolGetBalancePublic(context.Background(), nil, publicAddressInput{})
	requireCode(t, err, codeInvalidArgument)
	_, _, err = toolGetMinerStatusPublic(context.Background(), nil, publicAddressInput{})
	requireCode(t, err, codeInvalidArgument)
	_, _, err = toolGetAccountAuthenticatorsPublic(context.Background(), nil, publicAddressInput{})
	requireCode(t, err, codeInvalidArgument)

	_, _, err = toolGetBalancePublic(context.Background(), nil, publicAddressInput{Address: "not-an-address"})
	requireCode(t, err, codeInvalidAddress)

	_, statErr := os.Stat(kr)
	require.True(t, os.IsNotExist(statErr), "public tools created a keyring at %s", kr)
}

func TestPublicFindServicesDoesNotOpenKeyring(t *testing.T) {
	kr := filepath.Join(t.TempDir(), "keyring")
	prevKR, prevTrust := keyringDir, trustedRaters
	keyringDir = kr
	trustedRaters = nil
	fired := sync.Once{}
	fired.Do(func() {})
	dirOnce = fired
	dir = &directory.Directory{
		Scan: func() ([]wallet.IncomingPayment, error) { return nil, nil },
	}
	t.Cleanup(func() {
		dir, dirOnce = nil, sync.Once{}
		keyringDir, trustedRaters = prevKR, prevTrust
	})

	_, out, err := toolFindServicesPublic(context.Background(), nil, findServicesInput{})
	require.NoError(t, err)
	require.Empty(t, out.Services)
	_, statErr := os.Stat(kr)
	require.True(t, os.IsNotExist(statErr), "find_services opened a keyring at %s", kr)
}

func TestPublicHTTP(t *testing.T) {
	srv := httptest.NewServer(publicMux(newPublicServer()))
	t.Cleanup(srv.Close)

	res, err := http.Get(srv.URL + "/healthz")
	require.NoError(t, err)
	body, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode)
	require.Equal(t, "ok\n", string(body))

	res, err = http.Get(srv.URL + "/validators")
	require.NoError(t, err)
	body, err = io.ReadAll(res.Body)
	require.NoError(t, err)
	res.Body.Close()
	require.Equal(t, http.StatusNotFound, res.StatusCode)
	require.NotContains(t, string(body), "<html")
	require.NotContains(t, string(body), "index.html")

	ctx := context.Background()
	session, err := mcp.NewClient(&mcp.Implementation{Name: "public-http-test"}, nil).Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint:             srv.URL + "/mcp",
		DisableStandaloneSSE: true,
	}, nil)
	require.NoError(t, err)
	t.Cleanup(func() { session.Close() })
	var names []string
	for tool, err := range session.Tools(ctx, nil) {
		require.NoError(t, err)
		names = append(names, tool.Name)
	}
	require.Equal(t, sortedCopy(publicToolNames), sortedCopy(names))

	// The public schema requires address, so an omitted one never reaches
	// a handler that could create a key.
	_, err = session.CallTool(ctx, &mcp.CallToolParams{Name: "get_balance", Arguments: map[string]any{}})
	require.Error(t, err)
	require.Contains(t, err.Error(), "address")
}

func TestParseRemoteURL(t *testing.T) {
	got, err := parseRemoteURL("")
	require.NoError(t, err)
	require.Empty(t, got)
	got, err = parseRemoteURL("https://explorer.example/mcp")
	require.NoError(t, err)
	require.Equal(t, "https://explorer.example/mcp", got)
	_, err = parseRemoteURL("not a url")
	require.Error(t, err)
	_, err = parseRemoteURL("ftp://explorer.example/mcp")
	require.Error(t, err)
}
