package cli_test

import (
	"context"
	"testing"

	sdkmath "cosmossdk.io/math"
	"github.com/cosmos/cosmos-sdk/client"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	clienttypes "github.com/cosmos/ibc-go/v8/modules/core/02-client/types"
	channeltypes "github.com/cosmos/ibc-go/v8/modules/core/04-channel/types"
	ibctm "github.com/cosmos/ibc-go/v8/modules/light-clients/07-tendermint"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"

	"github.com/whoyoujoshin/aether/app"
	"github.com/whoyoujoshin/aether/wallet"
	"github.com/whoyoujoshin/aether/x/inflowcap"
	"github.com/whoyoujoshin/aether/x/inflowcap/client/cli"
)

func TestApply(t *testing.T) {
	usdc := wallet.InjectiveMainnetUSDCBaseDenom
	live := inflowcap.Params{Entries: []inflowcap.Entry{{ChannelId: "channel-3", Denom: "uosmo", Cap: sdkmath.NewInt(1)}}}

	p, added, err := cli.Apply(live, []string{"channel-0," + usdc + ",5000000000"}, nil)
	require.NoError(t, err)
	require.Equal(t, []string{"channel-0"}, added, "a new channel is reported for checking")
	require.Len(t, p.Entries, 2)
	require.Equal(t, "uosmo", p.Entries[0].Denom, "the other entry is kept")
	require.Equal(t, int64(5_000_000_000), p.Entries[1].Cap.Int64())

	// Raising a cap on a listed channel needs no new check.
	p2, added, err := cli.Apply(p, []string{"channel-0," + usdc + ",6000000000"}, nil)
	require.NoError(t, err)
	require.Empty(t, added)
	require.Len(t, p2.Entries, 2)
	require.Equal(t, int64(6_000_000_000), p2.Entries[1].Cap.Int64())

	p3, _, err := cli.Apply(p2, nil, []string{"channel-3,uosmo"})
	require.NoError(t, err)
	require.Len(t, p3.Entries, 1)
	require.Equal(t, "channel-0", p3.Entries[0].ChannelId)

	for name, c := range map[string]struct{ sets, removes []string }{
		"set missing a part":  {sets: []string{"channel-0," + usdc}},
		"cap not a number":    {sets: []string{"channel-0," + usdc + ",5k"}},
		"negative cap":        {sets: []string{"channel-0," + usdc + ",-1"}},
		"bad channel":         {sets: []string{"chan0," + usdc + ",1"}},
		"remove not listed":   {removes: []string{"channel-9,uatom"}},
		"remove missing part": {removes: []string{"channel-3"}},
	} {
		_, _, err := cli.Apply(live, c.sets, c.removes)
		require.Error(t, err, name)
	}
}

type fakeChannels struct {
	channeltypes.QueryClient
	state   channeltypes.State
	chainID string
}

func (f fakeChannels) Channel(context.Context, *channeltypes.QueryChannelRequest, ...grpc.CallOption) (*channeltypes.QueryChannelResponse, error) {
	return &channeltypes.QueryChannelResponse{Channel: &channeltypes.Channel{
		State: f.state, ConnectionHops: []string{"connection-0"},
		Counterparty: channeltypes.Counterparty{PortId: "transfer", ChannelId: "channel-412"},
	}}, nil
}

func (f fakeChannels) ChannelClientState(context.Context, *channeltypes.QueryChannelClientStateRequest, ...grpc.CallOption) (*channeltypes.QueryChannelClientStateResponse, error) {
	cs, err := codectypes.NewAnyWithValue(&ibctm.ClientState{ChainId: f.chainID, LatestHeight: clienttypes.NewHeight(1, 100)})
	if err != nil {
		return nil, err
	}
	return &channeltypes.QueryChannelClientStateResponse{IdentifiedClientState: &clienttypes.IdentifiedClientState{ClientId: "07-tendermint-0", ClientState: cs}}, nil
}

func TestCheckChannel(t *testing.T) {
	ctx := client.Context{}.WithInterfaceRegistry(app.MakeEncodingConfig().InterfaceRegistry)

	info, err := cli.CheckChannel(context.Background(), fakeChannels{state: channeltypes.OPEN, chainID: "injective-1"}, ctx, "channel-0", "injective-1")
	require.NoError(t, err)
	require.Contains(t, info, "client 07-tendermint-0 of injective-1")
	require.Contains(t, info, "transfer/channel-412", "the other end, for voters to check")

	_, err = cli.CheckChannel(context.Background(), fakeChannels{state: channeltypes.OPEN, chainID: "lookalike-1"}, ctx, "channel-0", "injective-1")
	require.ErrorContains(t, err, `follows "lookalike-1"`)

	_, err = cli.CheckChannel(context.Background(), fakeChannels{state: channeltypes.INIT, chainID: "injective-1"}, ctx, "channel-0", "injective-1")
	require.ErrorContains(t, err, "isn't open")
}
