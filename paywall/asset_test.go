package paywall

import (
	"net/http"
	"strings"
	"testing"

	"cosmossdk.io/math"
	"github.com/stretchr/testify/require"

	"github.com/whoyoujoshin/aether/wallet"
)

// A paywall priced in USDC offers USDC, takes only USDC, and says so.
func TestPaywall_ChargesItsOwnAsset(t *testing.T) {
	usdc, err := wallet.USDC("channel-3")
	require.NoError(t, err)
	h := newHarnessIn(t, usdc)

	req := h.invoice("/weather")
	require.Equal(t, usdc.Denom, req.Asset)
	require.Equal(t, "10000", req.MaxAmountRequired)
	require.Equal(t, "USDC", req.Extra.Symbol)
	require.Equal(t, "0.01", req.Extra.Amount)
	require.Empty(t, req.Extra.AmountAeth, "not an AETH price")
	require.Contains(t, req.Extra.Instructions, "uusdc")
	require.NotContains(t, req.Extra.Instructions, "uaeth")

	// The same number of uaeth doesn't pay it.
	h.ledger.pay("AETH", req.Extra.Invoice, seller(), 10_000, h.now, 0)
	h.expectRefusal(proof(req.Extra.Invoice, "AETH"), ErrInsufficient)

	// USDC does.
	h.ledger.payIn("USDC", req.Extra.Invoice, seller(), "10000"+usdc.Denom, h.now, 0)
	resp, body := h.get("/weather", proof(req.Extra.Invoice, "USDC"))
	require.Equal(t, http.StatusOK, resp.StatusCode, body)
	require.True(t, strings.HasPrefix(body, "sunny"))
}

// An AETH paywall reads exactly as it did before assets.
func TestPaywall_AETHOfferUnchanged(t *testing.T) {
	h := newHarness(t)
	req := h.invoice("/weather")
	require.Equal(t, Asset, req.Asset)
	require.Equal(t, "0.01", req.Extra.AmountAeth)
	require.Equal(t, instructions, req.Extra.Instructions)
}

func TestReceiptAmount(t *testing.T) {
	require.Equal(t, "150000", ReceiptAmount(math.NewInt(150_000), "uaeth"), "AETH receipts are as before")
	require.Equal(t, "150000", ReceiptAmount(math.NewInt(150_000), ""))
	require.Equal(t, "150000ibc/ABC", ReceiptAmount(math.NewInt(150_000), "ibc/ABC"))
}

func TestManifest_StatesItsAsset(t *testing.T) {
	usdc, err := wallet.USDC("channel-3")
	require.NoError(t, err)
	p, err := New(Config{PayTo: seller(), Price: math.NewInt(50_000), Asset: usdc, Network: "n", Lookup: (&fakeLedger{}).lookup})
	require.NoError(t, err)
	m := p.Manifest("svc", "")
	require.Equal(t, usdc.Denom, m.Denom())
	require.Equal(t, "0.05", m.PriceAmount)
	require.Empty(t, m.PriceAeth)
	require.Equal(t, Asset, Manifest{}.Denom(), "a manifest from before assets is AETH")
}
