package main

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/whoyoujoshin/aether/wallet"
)

func networkFlags(t *testing.T, args ...string) (*flag.FlagSet, *string, *wallet.USDCSetting) {
	t.Helper()
	origChain, origGRPC, origRPC, origBackend := chainID, grpcEndpoint, rpcEndpoint, keyringBackend
	t.Cleanup(func() { chainID, grpcEndpoint, rpcEndpoint, keyringBackend = origChain, origGRPC, origRPC, origBackend })
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	fs.StringVar(&grpcEndpoint, "grpc", "localhost:9090", "")
	fs.StringVar(&rpcEndpoint, "rpc", "http://localhost:26657", "")
	fs.StringVar(&chainID, "chain-id", "aether-testnet-1", "")
	fs.StringVar(&keyringBackend, "keyring-backend", "test", "")
	network := fs.String("network", "", "")
	usdc := wallet.USDCFlags(fs, wallet.USDCSetting{})
	require.NoError(t, fs.Parse(args))
	return fs, network, usdc
}

func TestNetwork_TestnetSetsItsEndpointsAndUSDC(t *testing.T) {
	fs, network, usdc := networkFlags(t, "--network", "testnet")
	require.NoError(t, applyNetwork(fs, *network, usdc))
	require.Equal(t, "aether-testnet-1", chainID)
	require.Equal(t, wallet.TestnetNetwork.GRPC, grpcEndpoint)
	require.Equal(t, wallet.TestnetNetwork.RPC, rpcEndpoint)
	require.Equal(t, wallet.TestnetUSDC, *usdc)
	require.Equal(t, "test", keyringBackend)
	require.Equal(t, wallet.TestnetNetwork.JobBoard, testnetJobBoard)
	require.Equal(t, wallet.TestnetNetwork.Faucet, defaultFaucet(chainID))
}

func TestNetwork_MainnetDefaults(t *testing.T) {
	fs, network, usdc := networkFlags(t, "--network", "mainnet", "--usdc-path", "transfer/channel-7")
	require.NoError(t, applyNetwork(fs, *network, usdc))
	require.Equal(t, "aether-1", chainID)
	require.Equal(t, "localhost:9090", grpcEndpoint, "no public endpoint yet: one's own node")
	require.Equal(t, "file", keyringBackend, "real funds: an encrypted keyring by default")
	require.Equal(t, wallet.MainnetUSDC("channel-7"), *usdc, "a path alone is Circle's USDC on Injective")
	require.Empty(t, defaultFaucet(chainID), "no faucet on mainnet")
	require.Empty(t, defaultJobBoard(chainID))
	require.NoError(t, checkRealFundsKeyring())

	// Without a USDC path, AETH only: the channel isn't open yet.
	fs, network, usdc = networkFlags(t, "--network", "mainnet")
	require.NoError(t, applyNetwork(fs, *network, usdc))
	require.Equal(t, wallet.USDCSetting{}, *usdc)
}

func TestNetwork_GivenFlagsWinAndConflictsAreRefused(t *testing.T) {
	fs, network, usdc := networkFlags(t, "--network", "testnet", "--grpc", "localhost:9999")
	require.NoError(t, applyNetwork(fs, *network, usdc))
	require.Equal(t, "localhost:9999", grpcEndpoint)

	fs, network, usdc = networkFlags(t, "--network", "mainnet", "--chain-id", "aether-testnet-1")
	require.ErrorContains(t, applyNetwork(fs, *network, usdc), "is chain aether-1")

	fs, network, usdc = networkFlags(t, "--network", "devnet")
	require.ErrorContains(t, applyNetwork(fs, *network, usdc), "unknown network")

	// No --network: nothing changes.
	fs, network, usdc = networkFlags(t)
	require.NoError(t, applyNetwork(fs, *network, usdc))
	require.Equal(t, "localhost:9090", grpcEndpoint)
	require.Equal(t, wallet.USDCSetting{}, *usdc)
}

// On aether-1 the unencrypted test keyring is refused however the chain
// was named; testnets, devnets and rehearsals keep it.
func TestNetwork_RealFundsRefuseTheTestKeyring(t *testing.T) {
	fs, network, usdc := networkFlags(t, "--network", "mainnet", "--keyring-backend", "test")
	require.NoError(t, applyNetwork(fs, *network, usdc))
	require.ErrorContains(t, checkRealFundsKeyring(), "aether-1 holds real funds")

	_, _, _ = networkFlags(t, "--chain-id", "aether-1")
	require.ErrorContains(t, checkRealFundsKeyring(), "encrypted keyring")

	for _, chain := range []string{"aether-testnet-1", "aether-rc-1", "aether-devnet"} {
		_, _, _ = networkFlags(t, "--chain-id", chain)
		require.NoError(t, checkRealFundsKeyring(), chain)
	}
}

func TestInit_MainnetUsesAnEncryptedKeyringAndNoFaucet(t *testing.T) {
	setupAgent(t)
	origChain, origBackend := chainID, keyringBackend
	t.Cleanup(func() { chainID, keyringBackend = origChain, origBackend })
	dir := t.TempDir()
	pass := filepath.Join(dir, "pass")
	require.NoError(t, os.WriteFile(pass, []byte("correct horse battery"), 0o600))
	t.Setenv("AETHER_KEYRING_PASSPHRASE_FILE", pass)
	var buf bytes.Buffer
	origOut, origWait := initOut, fundsWait
	initOut, fundsWait = &buf, 10*time.Millisecond
	t.Cleanup(func() { initOut, fundsWait = origOut, origWait })

	keys := filepath.Join(dir, "keys")
	require.NoError(t, runInit([]string{"--keyring-dir", keys, "--network", "mainnet", "--grpc", "127.0.0.1:1", "--rpc", "http://127.0.0.1:2"}))
	out := buf.String()
	require.Contains(t, out, "Created the agent's account aether1")
	require.NotContains(t, out, "Requesting testnet funds")
	require.Contains(t, out, "--chain-id aether-1")
	require.Contains(t, out, "--keyring-backend file")
	require.Contains(t, out, "AETHER_KEYRING_PASSPHRASE_FILE")
	_, err := os.Stat(filepath.Join(keys, "keyring-file"))
	require.NoError(t, err, "the key is in the file keyring")
	_, err = os.Stat(filepath.Join(keys, "keyring-test"))
	require.True(t, os.IsNotExist(err), "nothing in the test keyring")
}
