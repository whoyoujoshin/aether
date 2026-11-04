package main

import (
	"flag"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/whoyoujoshin/aether/wallet"
)

func walletFlags(t *testing.T, args ...string) (*flag.FlagSet, *wallet.USDCSetting) {
	t.Helper()
	orig := []string{chainID, grpcEndpoint, rpcEndpoint, explorerURL, keyringBackend, network}
	t.Cleanup(func() {
		chainID, grpcEndpoint, rpcEndpoint, explorerURL, keyringBackend, network = orig[0], orig[1], orig[2], orig[3], orig[4], orig[5]
	})
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	fs.StringVar(&chainID, "chain-id", "aether-testnet-1", "")
	fs.StringVar(&grpcEndpoint, "grpc", "grpc.157-245-252-221.sslip.io:443", "")
	fs.StringVar(&rpcEndpoint, "rpc", "https://rpc.157-245-252-221.sslip.io", "")
	fs.StringVar(&explorerURL, "explorer", testnetExplorer, "")
	fs.StringVar(&keyringBackend, "keyring-backend", "test", "")
	usdc := wallet.USDCFlags(fs, wallet.USDCSetting{})
	require.NoError(t, fs.Parse(args))
	return fs, usdc
}

func TestNetwork_TestnetAndMainnet(t *testing.T) {
	fs, usdc := walletFlags(t)
	require.NoError(t, applyNetwork(fs, "testnet", usdc))
	require.Equal(t, "aether-testnet-1", chainID)
	require.Equal(t, wallet.TestnetUSDC, *usdc)
	require.Equal(t, testnetExplorer, explorerURL)
	code, out := call(t, handleNetwork, "GET", "/api/network", "")
	require.Equal(t, http.StatusOK, code)
	require.Equal(t, "testnet", out["name"])
	require.Equal(t, false, out["real"])
	require.Equal(t, wallet.TestnetNetwork.Faucet, out["faucet"])

	// Mainnet: no public node yet, and only the encrypted keyring.
	fs, usdc = walletFlags(t, "--keyring-backend", "file")
	require.ErrorContains(t, applyNetwork(fs, "mainnet", usdc), "no public node yet")
	fs, usdc = walletFlags(t, "--grpc", "localhost:9090", "--rpc", "http://localhost:26657")
	require.ErrorContains(t, applyNetwork(fs, "mainnet", usdc), "encrypted keyring")
	fs, usdc = walletFlags(t, "--grpc", "localhost:9090", "--rpc", "http://localhost:26657", "--keyring-backend", "file", "--usdc-path", "transfer/channel-7")
	require.NoError(t, applyNetwork(fs, "mainnet", usdc))
	require.Equal(t, "aether-1", chainID)
	require.Equal(t, "localhost:9090", grpcEndpoint)
	require.Empty(t, explorerURL, "no mainnet explorer yet: the Mine tab reads the node")
	require.Equal(t, wallet.MainnetUSDC("channel-7"), *usdc)
	_, out = call(t, handleNetwork, "GET", "/api/network", "")
	require.Equal(t, "mainnet", out["name"])
	require.Equal(t, true, out["real"])
	require.Equal(t, "", out["faucet"])

	fs, usdc = walletFlags(t, "--chain-id", "aether-testnet-1")
	require.ErrorContains(t, applyNetwork(fs, "mainnet", usdc), "is chain aether-1")
}
