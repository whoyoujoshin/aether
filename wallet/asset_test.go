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
