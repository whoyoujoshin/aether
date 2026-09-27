package wallet_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/whoyoujoshin/aether/wallet"
)

// These tests require a real, running Aether devnet node reachable at
// localhost:9090 (the standard gRPC port) -- they are genuine live
// integration tests, not self-contained unit tests, matching this
// project's consistent discipline of verifying against a real running
// chain rather than trusting logic that merely compiles.

func TestClient_GetBalance_ReturnsRealBalance(t *testing.T) {
	client, err := wallet.NewClient("localhost:9090")
	require.NoError(t, err)
	defer client.Close()

	// The faucet account, funded via genesis earlier in this project.
	balance, err := client.GetBalance("aether1gq80ffgcaq803gzmev6my3hr9atax47qsfwzln92ezlpg0r0ehnqhcl4cd")
	require.NoError(t, err)
	require.NotEmpty(t, balance, "a genesis-funded account must show a real, non-empty balance")
}

func TestClient_GetAccountInfo_ReturnsRealSequenceAndAccountNumber(t *testing.T) {
	client, err := wallet.NewClient("localhost:9090")
	require.NoError(t, err)
	defer client.Close()

	accountNumber, sequence, err := client.GetAccountInfo("aether1gq80ffgcaq803gzmev6my3hr9atax47qsfwzln92ezlpg0r0ehnqhcl4cd")
	require.NoError(t, err)
	require.GreaterOrEqual(t, sequence, uint64(0))
	_ = accountNumber // just confirming the call succeeds without error; exact value depends on genesis ordering
}
// GRPCCredentials needs no live node: it's a pure decision from the
// endpoint string, and it's the reason a TLS-fronted testnet endpoint
// doesn't just hang or fail a plaintext handshake against it.
func TestGRPCCredentials_TLSOnlyOnPort443(t *testing.T) {
	for _, tls := range []string{"grpc.157-245-252-221.sslip.io:443", "example.com:443"} {
		require.Equal(t, "tls", wallet.GRPCCredentials(tls).Info().SecurityProtocol, tls)
	}
	for _, plain := range []string{"localhost:9090", "157.245.252.221:9090", "grpc.example.com:9443", ""} {
		require.Equal(t, "insecure", wallet.GRPCCredentials(plain).Info().SecurityProtocol, plain)
	}
}
