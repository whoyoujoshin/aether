package shopper

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSaveQuoteRoundTrip(t *testing.T) {
	dir := t.TempDir()
	o := Offer{Rail: RailAPI, QuoteID: "abc", Name: "Hello", Amount: "1", Asset: "uaeth", PriceDisplay: "0.000001 AETH"}
	d := decide(CodeNeedsConfirm, NextConfirm, "confirm", o.QuoteID)
	require.NoError(t, SaveQuote(dir, "aether-testnet-1", o, d))
	got, err := LoadQuote(dir, "abc")
	require.NoError(t, err)
	require.Equal(t, "Hello", got.Offer.Name)
	require.Equal(t, CodeNeedsConfirm, got.Decision.Code)
	require.NoError(t, SaveQuote(dir, "aether-testnet-1", Offer{}, Decision{}))
}
