package wallet

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"

	"cosmossdk.io/math"
	"github.com/stretchr/testify/require"
)

func TestUSDCDenom(t *testing.T) {
	usdc, err := USDC("channel-3")
	require.NoError(t, err)
	// ICS-20: ibc/ + upper-hex SHA-256 of the trace path.
	want := fmt.Sprintf("ibc/%X", sha256.Sum256([]byte("transfer/channel-3/uusdc")))
	require.Equal(t, want, usdc.Denom)
	require.Equal(t, "Noble over transfer/channel-3", usdc.Origin)

	for _, bad := range []string{"", "3", "channel-", "channel-3/uusdc", "connection-0"} {
		_, err := USDC(bad)
		require.Error(t, err, bad)
	}
}

func TestUSDCAtRoutes(t *testing.T) {
	ibc := func(trace string) string { return fmt.Sprintf("ibc/%X", sha256.Sum256([]byte(trace))) }

	direct, err := USDC("channel-3")
	require.NoError(t, err)
	same, err := USDCAt("transfer/channel-3", "uusdc")
	require.NoError(t, err)
	require.Equal(t, direct, same, "the channel shorthand is a one-hop path")

	viaOsmosis, err := USDCAt("transfer/channel-1/transfer/channel-4280", "uusdc")
	require.NoError(t, err)
	require.Equal(t, ibc("transfer/channel-1/transfer/channel-4280/uusdc"), viaOsmosis.Denom)
	require.Equal(t, "Noble over transfer/channel-1/transfer/channel-4280", viaOsmosis.Origin)

	inj, err := USDCAt("transfer/channel-1/transfer/channel-10092", "erc20:0x0C382e685bbeeFE5d3d9C29e29E341fEE8E84C5d")
	require.NoError(t, err)
	require.Equal(t, ibc("transfer/channel-1/transfer/channel-10092/erc20:0x0C382e685bbeeFE5d3d9C29e29E341fEE8E84C5d"), inj.Denom)
	require.Equal(t, "erc20:0x0C382e685bbeeFE5d3d9C29e29E341fEE8E84C5d over transfer/channel-1/transfer/channel-10092", inj.Origin)
	require.Equal(t, "USDC", inj.Symbol)
	require.Equal(t, "uusdc", inj.BaseUnit, "people still write uusdc for its smallest unit")
	require.Equal(t, 6, inj.Decimals)

	// The base denom is hashed exactly: case matters.
	lower, err := USDCAt("transfer/channel-1/transfer/channel-10092", "erc20:0x0c382e685bbeefe5d3d9c29e29e341fee8e84c5d")
	require.NoError(t, err)
	require.NotEqual(t, inj.Denom, lower.Denom)

	for _, c := range []struct{ path, base string }{
		{"", "uusdc"},
		{"transfer", "uusdc"},
		{"transfer/channel-1/transfer", "uusdc"},
		{"transfer/connection-1", "uusdc"},
		{"transfer/channel-1/", "uusdc"},
		{"/channel-1", "uusdc"},
		{"transfer/channel-1", ""},
		{"transfer/channel-1", "ibc/0D80A29BCBE8A38AAE75313264A0741092566D4EF5A7FEDAD7F86E12193C2328"},
		{"transfer/channel-1", "5usdc"},
	} {
		_, err := USDCAt(c.path, c.base)
		require.Error(t, err, "%q %q", c.path, c.base)
	}
}

func TestTestnetUSDC(t *testing.T) {
	usdc, ok, err := TestnetUSDC.Asset()
	require.NoError(t, err)
	require.True(t, ok)
	// The same denom clients/testdata/vectors.json pins as usdc.routed.
	require.Equal(t, "ibc/4F4C931B9AC39222C0EC5EB909F2BA8C0C615A1E343D8C1448ACDBBE6AF3743A", usdc.Denom)
	require.Equal(t, "Injective over transfer/channel-1/transfer/channel-10092", usdc.Origin)
}

func TestUSDCSetting(t *testing.T) {
	none, ok, err := USDCSetting{}.Asset()
	require.NoError(t, err)
	require.False(t, ok)
	require.Equal(t, Asset{}, none)

	direct, _ := USDC("channel-3")
	got, ok, err := USDCSetting{Channel: "channel-3"}.Asset()
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, direct, got)

	noble, _ := USDCAt("transfer/channel-1/transfer/channel-4280", "uusdc")
	got, ok, err = USDCSetting{Path: "transfer/channel-1/transfer/channel-4280"}.Asset()
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, noble, got, "the base denom defaults to uusdc")

	_, _, err = USDCSetting{Channel: "channel-3", Path: "transfer/channel-3"}.Asset()
	require.ErrorContains(t, err, "not both")
	_, _, err = USDCSetting{Channel: "channel-3", BaseDenom: "uusdc"}.Asset()
	require.ErrorContains(t, err, "not both")
	_, _, err = USDCSetting{BaseDenom: "uusdc"}.Asset()
	require.ErrorContains(t, err, "needs a USDC path")

	a, err := NewAssetsFor(USDCSetting{Path: "transfer/channel-1/transfer/channel-4280"})
	require.NoError(t, err)
	as, v, err := a.Parse("2.5 USDC")
	require.NoError(t, err)
	require.Equal(t, noble, as)
	require.Equal(t, int64(2_500_000), v.Int64())
	_, err = NewAssetsFor(USDCSetting{Path: "channel-1"})
	require.Error(t, err)

	// Issuer only renames: the denom is the route's and the base denom's.
	inj := USDCSetting{Path: "transfer/channel-1/transfer/channel-10092", BaseDenom: "erc20:0x0C382e685bbeeFE5d3d9C29e29E341fEE8E84C5d", Issuer: "Injective"}
	got, ok, err = inj.Asset()
	require.NoError(t, err)
	require.True(t, ok)
	plain, _ := USDCAt(inj.Path, inj.BaseDenom)
	require.Equal(t, plain.Denom, got.Denom)
	require.Equal(t, "Injective over transfer/channel-1/transfer/channel-10092", got.Origin)
	got, _, err = USDCSetting{Channel: "channel-3", Issuer: "Noble"}.Asset()
	require.NoError(t, err)
	require.Equal(t, direct, got)
	_, _, err = USDCSetting{Issuer: "Injective"}.Asset()
	require.ErrorContains(t, err, "needs a USDC channel or path")
	_, _, err = USDCSetting{Channel: "channel-3", Issuer: "Injective testnet"}.Asset()
	require.ErrorContains(t, err, "one word")
}

func TestAssetsParse(t *testing.T) {
	onlyAeth, err := NewAssets("")
	require.NoError(t, err)
	both, err := NewAssets("channel-3")
	require.NoError(t, err)
	usdc, _ := USDC("channel-3")

	for _, c := range []struct {
		in    string
		asset Asset
		base  int64
	}{
		{"1.5 AETH", AETH, 1_500_000},
		{"1500000uaeth", AETH, 1_500_000},
		{"5 USDC", usdc, 5_000_000},
		{"0.25usdc", usdc, 250_000},
		{"2250000 uusdc", usdc, 2_250_000},
		{"0.000001 USDC", usdc, 1},
	} {
		as, v, err := both.Parse(c.in)
		require.NoError(t, err, c.in)
		require.Equal(t, c.asset, as, c.in)
		require.Equal(t, c.base, v.Int64(), c.in)
	}

	for in, msg := range map[string]string{
		"5":              "no unit",
		"5 DAI":          "unknown unit",
		"0.0000001 USDC": "decimal places",
		"1.5uusdc":       "can't be fractional",
		"0 USDC":         "greater than zero",
	} {
		_, _, err := both.Parse(in)
		require.ErrorContains(t, err, msg, in)
	}

	// Without a USDC channel, USDC is unknown -- never guessed at.
	_, _, err = onlyAeth.Parse("5 USDC")
	require.ErrorContains(t, err, "unknown unit")
	require.True(t, strings.Contains(err.Error(), "AETH"))
}

func TestAssetsLookupAndFormat(t *testing.T) {
	a, err := NewAssets("channel-0")
	require.NoError(t, err)
	usdc, _ := USDC("channel-0")

	got, ok := a.BySymbol("usdc")
	require.True(t, ok)
	require.Equal(t, usdc, got)
	got, ok = a.ByDenom(usdc.Denom)
	require.True(t, ok)
	require.Equal(t, usdc, got)
	_, ok = a.ByDenom("ibc/0000")
	require.False(t, ok)
	require.Equal(t, []Asset{AETH, usdc}, a.List())

	require.Equal(t, "1.5 USDC", usdc.Format(math.NewInt(1_500_000)))
	require.Equal(t, "0.000001 AETH", AETH.Format(math.NewInt(1)))
	require.Equal(t, "12", usdc.Decimal(math.NewInt(12_000_000)))
	// The AETH-only helpers agree with the asset's.
	require.Equal(t, FormatAeth(math.NewInt(1_234_500)), AETH.Decimal(math.NewInt(1_234_500)))
}
