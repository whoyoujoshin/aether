package wallet

import (
	"encoding/json"
	"math"
	"os"
	"testing"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"
)

// clients/testdata/fees.json: the fee rule every client applies.
func TestFeeFor_SharedVectors(t *testing.T) {
	bz, err := os.ReadFile("../clients/testdata/fees.json")
	require.NoError(t, err)
	var file struct {
		Vectors []struct {
			Name, Prices, Denom, Fee string
			Gas                      uint64
			Tip                      float64
		}
	}
	require.NoError(t, json.Unmarshal(bz, &file))
	require.NotEmpty(t, file.Vectors)
	for _, v := range file.Vectors {
		prices, err := sdk.ParseDecCoins(v.Prices)
		require.NoError(t, err, v.Name)
		got, err := FeeFor(prices, v.Denom, v.Gas, v.Tip)
		require.NoError(t, err, v.Name)
		require.Equal(t, v.Fee, got.String(), v.Name)
	}
}

func TestFeeSetting_Validate(t *testing.T) {
	require.NoError(t, AutoFees.Validate())
	require.NoError(t, FeeSetting{GasPrice: "0.0001uaeth", Tip: 2}.Validate())
	require.ErrorContains(t, FeeSetting{GasPrice: "auto", Tip: 0.5}.Validate(), "at least 1")
	require.ErrorContains(t, FeeSetting{GasPrice: "cheap", Tip: 1}.Validate(), "--gas-price")
	_, err := FeeFor(nil, BaseDenom, 1, 0.9)
	require.Error(t, err)
}

// A transaction paying a fee lists the fee's transfer to the fee collector
// first; the payment is the next transfer, and the fee is Fee.
func TestSummarizeTransfers_FeeIsNotThePayment(t *testing.T) {
	payer, payee := "aether1payer", "aether1payee"
	d := &TransactionDetail{Transfers: []Transfer{
		{From: payer, To: FeeCollector, Amount: "40uaeth"},
		{From: payer, To: payee, Amount: "500uaeth"},
	}}
	summarizeTransfers(d)
	require.Equal(t, payer, d.From)
	require.Equal(t, payee, d.To)
	require.Equal(t, "500uaeth", d.Amount)
	require.Equal(t, "40uaeth", d.Fee)

	// No fee: the first transfer, as before.
	d = &TransactionDetail{Transfers: []Transfer{{From: payer, To: payee, Amount: "1uaeth"}}}
	summarizeTransfers(d)
	require.Equal(t, "1uaeth", d.Amount)
	require.Empty(t, d.Fee)
	require.Contains(t, FeeCollector, "aether1")
}

// A gas limit past MaxInt64 (a --gas typo) is priced exactly, never
// wrapped negative.
func TestFeeFor_HugeGasDoesNotWrap(t *testing.T) {
	prices, err := sdk.ParseDecCoins("0.0001uaeth")
	require.NoError(t, err)
	got, err := FeeFor(prices, BaseDenom, math.MaxUint64, 1)
	require.NoError(t, err)
	require.Equal(t, "1844674407370956uaeth", got.String()) // ceil(18446744073709551615 x 0.0001)
}
