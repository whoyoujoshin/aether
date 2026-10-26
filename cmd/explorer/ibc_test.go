package main

import (
	"testing"
	"time"

	transfertypes "github.com/cosmos/ibc-go/v8/modules/apps/transfer/types"
	"github.com/stretchr/testify/require"
)

func TestClientAttention(t *testing.T) {
	now := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	require.Equal(t, "", clientAttention("Active", now.Add(48*time.Hour), now))
	require.Equal(t, "expiring", clientAttention("Active", now.Add(2*time.Hour), now))
	require.Equal(t, "expired", clientAttention("Active", now.Add(-time.Minute), now))
	require.Equal(t, "expired", clientAttention("Expired", time.Time{}, now))
	require.Equal(t, "frozen", clientAttention("Frozen", now.Add(48*time.Hour), now))
	require.Equal(t, "", clientAttention("Active", time.Time{}, now), "no consensus time is not guessed at")
	require.Equal(t, "", clientAttention("Unknown", time.Time{}, now))
}

func TestPickBridgeChannel_BusiestOpenPath(t *testing.T) {
	channels := []ibcChannelDTO{
		{PortID: "transfer", ChannelID: "channel-1", State: "STATE_OPEN", CounterpartyChainID: "osmo-test-5", PacketsSent: 5},
		{PortID: "transfer", ChannelID: "channel-2", State: "STATE_OPEN", CounterpartyChainID: "injective-888", PacketsSent: 100},
		{PortID: "transfer", ChannelID: "channel-3", State: "STATE_OPEN", CounterpartyChainID: "osmo-test-5", PacketsSent: 8},
		{PortID: "transfer", ChannelID: "channel-9", State: "STATE_CLOSED", CounterpartyChainID: "osmo-test-5", PacketsSent: 80},
	}
	got, ok := pickBridgeChannel(channels, "osmo-test-5")
	require.True(t, ok)
	require.Equal(t, "channel-3", got.ChannelID, "the reopened Osmosis path has carried more packets than channel-1")

	got, ok = pickBridgeChannel(channels, "injective-888")
	require.True(t, ok)
	require.Equal(t, "channel-2", got.ChannelID)

	_, ok = pickBridgeChannel(channels, "")
	require.False(t, ok)
	_, ok = pickBridgeChannel(channels, "noble-1")
	require.False(t, ok)
}

func TestDenomHoldings_OnlyWhatAetherHolds(t *testing.T) {
	held := transfertypes.DenomTrace{Path: "transfer/channel-2", BaseDenom: "uusdc"}
	idle := transfertypes.DenomTrace{Path: "transfer/channel-9", BaseDenom: "uatom"}
	rows := denomHoldings([]transfertypes.DenomTrace{held, idle}, map[string]string{
		held.IBCDenom(): "5000000",
		idle.IBCDenom(): "0",
		"ibc/DEADBEEF":  "9",
		"uaeth":         "100",
	})
	require.Len(t, rows, 2)
	require.Equal(t, held.IBCDenom(), rows[0].Denom)
	require.Equal(t, "transfer/channel-2", rows[0].Path)
	require.Equal(t, "uusdc", rows[0].Base)
	require.Equal(t, "5000000", rows[0].Amount)
	require.Equal(t, "ibc/DEADBEEF", rows[1].Denom)
	require.Empty(t, rows[1].Path, "a supply with no trace is still listed")
}

func TestSummarizeRelayers(t *testing.T) {
	t0 := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	hits := []relayHit{
		{address: "aether1relayer", action: "recv_packet", channel: "channel-3", height: 10, when: t0},
		{address: "aether1relayer", action: "acknowledge_packet", channel: "channel-3", height: 12, when: t0.Add(time.Minute)},
		{address: "aether1relayer", action: "recv_packet", channel: "channel-2", height: 11, when: t0},
		{address: "aether1other", action: "timeout_packet", channel: "channel-2", height: 9, when: t0},
		{address: "", action: "recv_packet", channel: "channel-3", height: 13},
	}
	rows := summarizeRelayers(hits)
	require.Len(t, rows, 2)
	require.Equal(t, "aether1relayer", rows[0].Address)
	require.Equal(t, 3, rows[0].Packets)
	require.Equal(t, int64(12), rows[0].LastHeight)
	require.Equal(t, "acknowledge_packet", rows[0].LastAction)
	require.Equal(t, []string{"channel-2", "channel-3"}, rows[0].Channels)
	require.Equal(t, 1, rows[1].Packets)
}
