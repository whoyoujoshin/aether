package inflowcap_test

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"testing"

	sdkmath "cosmossdk.io/math"
	"github.com/stretchr/testify/require"

	"github.com/whoyoujoshin/aether/x/inflowcap"
)

// clients/testdata/inflowcaps.json holds Caps query answers as the node
// encodes them (one from a devnet node, one encoded here) and what each
// decodes to, for the TypeScript and Python clients' decoders. This
// checks the file against the generated Go types.
type capsVector struct {
	Name     string `json:"name"`
	Value    string `json:"value"` // base64, as abci_query returns it
	Expected []struct {
		Channel      string `json:"channel"`
		Denom        string `json:"denom"`
		VoucherDenom string `json:"voucherDenom"`
		Cap          string `json:"cap"`
		Supply       string `json:"supply"`
		Room         string `json:"room"`
	} `json:"expected"`
}

func TestCapsVectors(t *testing.T) {
	bz, err := os.ReadFile("../../clients/testdata/inflowcaps.json")
	require.NoError(t, err)
	var file struct {
		Vectors []capsVector `json:"vectors"`
	}
	require.NoError(t, json.Unmarshal(bz, &file))
	require.NotEmpty(t, file.Vectors)
	for _, v := range file.Vectors {
		raw, err := base64.StdEncoding.DecodeString(v.Value)
		require.NoError(t, err, v.Name)
		var res inflowcap.QueryCapsResponse
		require.NoError(t, res.Unmarshal(raw), v.Name)
		require.Len(t, res.Caps, len(v.Expected), v.Name)
		for i, e := range v.Expected {
			c := res.Caps[i]
			require.Equal(t, e.Channel, c.Entry.ChannelId, v.Name)
			require.Equal(t, e.Denom, c.Entry.Denom, v.Name)
			require.Equal(t, e.VoucherDenom, c.VoucherDenom, v.Name)
			require.Equal(t, e.VoucherDenom, c.Entry.VoucherDenom(), v.Name)
			require.Equal(t, e.Cap, c.Entry.Cap.String(), v.Name)
			require.Equal(t, e.Supply, c.Supply.String(), v.Name)
			require.Equal(t, e.Room, c.Room.String(), v.Name)
		}
	}
}

// TestEncodeTestnetVector prints the second case's encoding, to
// regenerate it: go test ./x/inflowcap -run TestEncodeTestnetVector -v
func TestEncodeTestnetVector(t *testing.T) {
	usdc := inflowcap.Entry{ChannelId: "channel-2", Denom: "erc20:0x0C382e685bbeeFE5d3d9C29e29E341fEE8E84C5d", Cap: sdkmath.NewInt(100_000_000)}
	osmo := inflowcap.Entry{ChannelId: "channel-3", Denom: "uosmo", Cap: sdkmath.NewInt(1_000_000_000)}
	res := inflowcap.QueryCapsResponse{Caps: []inflowcap.CapStatus{
		{Entry: usdc, VoucherDenom: usdc.VoucherDenom(), Supply: sdkmath.NewInt(10_010_000), Room: sdkmath.NewInt(89_990_000)},
		{Entry: osmo, VoucherDenom: osmo.VoucherDenom(), Supply: sdkmath.NewInt(1_000_000_123), Room: sdkmath.ZeroInt()},
	}}
	bz, err := res.Marshal()
	require.NoError(t, err)
	t.Log(base64.StdEncoding.EncodeToString(bz))
}
