package wallet

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

type vecUSDC struct {
	Channel   string `json:"channel,omitempty"`
	Path      string `json:"path,omitempty"`
	BaseDenom string `json:"baseDenom,omitempty"`
	Issuer    string `json:"issuer,omitempty"`
}

func (v vecUSDC) setting() USDCSetting {
	return USDCSetting{Channel: v.Channel, Path: v.Path, BaseDenom: v.BaseDenom, Issuer: v.Issuer}
}

// clients/testdata/networks.json: the public networks every client names.
func TestNetworks_SharedVectors(t *testing.T) {
	bz, err := os.ReadFile("../clients/testdata/networks.json")
	require.NoError(t, err)
	var file struct {
		Networks []struct {
			Name, ChainID, RPC, GRPC, Faucet, JobBoard string
			USDC                                       vecUSDC
			Real                                       bool
		}
		UsdcWith []struct {
			Name, Network string
			Given, USDC   vecUSDC
		}
	}
	require.NoError(t, json.Unmarshal(bz, &file))

	require.Len(t, file.Networks, len(Networks()))
	for i, n := range Networks() {
		v := file.Networks[i]
		require.Equal(t, v.Name, n.Name)
		require.Equal(t, v.ChainID, n.ChainID, n.Name)
		require.Equal(t, v.RPC, n.RPC, n.Name)
		require.Equal(t, v.GRPC, n.GRPC, n.Name)
		require.Equal(t, v.Faucet, n.Faucet, n.Name)
		require.Equal(t, v.JobBoard, n.JobBoard, n.Name)
		require.Equal(t, v.USDC.setting(), n.USDC, n.Name)
		require.Equal(t, v.Real, n.Real, n.Name)

		byName, err := NetworkByName(n.Name)
		require.NoError(t, err)
		require.Equal(t, n, byName)
		byChain, ok := NetworkForChain(n.ChainID)
		require.True(t, ok)
		require.Equal(t, n, byChain)
	}
	for _, c := range file.UsdcWith {
		n, err := NetworkByName(c.Network)
		require.NoError(t, err)
		got := n.USDCWith(c.Given.setting())
		require.Equal(t, c.USDC.setting(), got, c.Name)
		_, _, err = got.Asset()
		require.NoError(t, err, c.Name)
	}

	_, err = NetworkByName("devnet")
	require.Error(t, err)
	_, ok := NetworkForChain("aether-rc-1")
	require.False(t, ok, "a rehearsal gets no public defaults")
}

// The mainnet USDC tools accept is the one x/inflowcap would list:
// wallet.MainnetUSDC for the same channel.
func TestNetworks_MainnetUSDCMatchesTheListedOne(t *testing.T) {
	got, ok, err := MainnetNetwork.USDCWith(USDCSetting{Path: "transfer/channel-7"}).Asset()
	require.NoError(t, err)
	require.True(t, ok)
	want, _, err := MainnetUSDC("channel-7").Asset()
	require.NoError(t, err)
	require.Equal(t, want, got)
	require.Equal(t, "Injective over transfer/channel-7", got.Origin)
}
