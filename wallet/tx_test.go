package wallet_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"cosmossdk.io/math"
	"github.com/stretchr/testify/require"

	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/whoyoujoshin/aether/crypto/mldsa"
	"github.com/whoyoujoshin/aether/wallet"
)

func requestFaucetFunds(t *testing.T, address string) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"address": address})
	resp, err := http.Post("http://localhost:8080/request", "application/json", bytes.NewReader(body))
	require.NoError(t, err, "faucet must be running (go run ./cmd/faucet) for this test")
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode, "faucet request must succeed -- check faucet's own logs if this fails")
}

// Genuine live integration test, exercising the complete real
// pipeline: wallet key creation, real external funding via the
// already-built faucet, a real chain query for account info, real
// transaction build/sign/broadcast, and confirming the recipient's
// balance genuinely changed on-chain -- not merely that no error was
// returned.
func TestWallet_BuildSignAndBroadcast_RealTransferSucceeds(t *testing.T) {
	registry := codectypes.NewInterfaceRegistry()
	mldsa.RegisterInterfaces(registry)
	cdc := codec.NewProtoCodec(registry)

	w, err := wallet.NewWallet("aetherd-test", "test", t.TempDir(), cdc)
	require.NoError(t, err)

	sender, _, err := w.CreateAccount("sender")
	require.NoError(t, err)
	recipient, _, err := w.CreateAccount("recipient")
	require.NoError(t, err)

	// Fund the sender via the real faucet HTTP service.
	requestFaucetFunds(t, sender.Address)
time.Sleep(8 * time.Second) // let the faucet's tx actually land on-chain

	client, err := wallet.NewClient("localhost:9090")
	require.NoError(t, err)
	defer client.Close()

	senderBalanceBefore, err := client.GetBalance(sender.Address)
	require.NoError(t, err)
	require.False(t, senderBalanceBefore.IsZero(), "sender must show a real balance after faucet funding")

	accountNumber, sequence, err := client.GetAccountInfo(sender.Address)
	require.NoError(t, err)

	sendAmount := sdk.NewCoins(sdk.NewCoin("uaeth", math.NewInt(100)))

	signed, err := w.BuildAndSignSendTx(
		"sender", sender.Address, recipient.Address, sendAmount,
		wallet.TxParams{
			ChainID:       "aether-testnet-1",
			AccountNumber: accountNumber,
			Sequence:      sequence,
			GasLimit:      400_000,
			Fees:          sdk.NewCoins(sdk.NewCoin("uaeth", math.NewInt(0))),
		},
	)
	require.NoError(t, err)
	require.NotEmpty(t, signed.Bytes)

result, err := client.BroadcastTx(signed)
require.NoError(t, err)
t.Logf("broadcast result: code=%d hash=%s log=%s", result.Code, result.TxHash, result.RawLog)
require.Equal(t, uint32(0), result.Code, "transaction must genuinely succeed on-chain: %s", result.RawLog)

time.Sleep(8 * time.Second) // let the send tx actually land on-chain

	recipientBalance, err := client.GetBalance(recipient.Address)
	require.NoError(t, err)
	require.True(t, recipientBalance.AmountOf("uaeth").Equal(math.NewInt(100)), "recipient must genuinely receive exactly the sent amount")
}