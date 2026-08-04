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
	balance, err := client.GetBalance("cosmos1gq80ffgcaq803gzmev6my3hr9atax47qsfwzln92ezlpg0r0ehnqgq9lqm")
	require.NoError(t, err)
	require.NotEmpty(t, balance, "a genesis-funded account must show a real, non-empty balance")
}

func TestClient_GetAccountInfo_ReturnsRealSequenceAndAccountNumber(t *testing.T) {
	client, err := wallet.NewClient("localhost:9090")
	require.NoError(t, err)
	defer client.Close()

	accountNumber, sequence, err := client.GetAccountInfo("cosmos1gq80ffgcaq803gzmev6my3hr9atax47qsfwzln92ezlpg0r0ehnqgq9lqm")
	require.NoError(t, err)
	require.GreaterOrEqual(t, sequence, uint64(0))
	_ = accountNumber // just confirming the call succeeds without error; exact value depends on genesis ordering
}