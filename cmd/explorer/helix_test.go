package main

import (
	"testing"
	"time"

	abci "github.com/cometbft/cometbft/abci/types"
	"github.com/stretchr/testify/require"
)

func packetEv(typ string, seq, src, dst string, data string) abci.Event {
	attrs := []abci.EventAttribute{
		{Key: "packet_sequence", Value: seq},
		{Key: "packet_src_port", Value: "transfer"}, {Key: "packet_src_channel", Value: src},
		{Key: "packet_dst_port", Value: "transfer"}, {Key: "packet_dst_channel", Value: dst},
	}
	if data != "" {
		attrs = append(attrs, abci.EventAttribute{Key: "packet_data", Value: data})
	}
	return abci.Event{Type: typ, Attributes: attrs}
}

func at(evs []packetEvent, chain string, h int64, t time.Time) []packetEvent {
	for i := range evs {
		evs[i].chain, evs[i].height, evs[i].time = chain, h, t
	}
	return evs
}

func TestPacketEventsOf_ReadsPacketsAndSkipsTheRest(t *testing.T) {
	evs := packetEventsOf([]abci.Event{
		{Type: "transfer"},
		packetEv("send_packet", "7", "channel-0", "channel-3", `{"denom":"uaeth","amount":"15000000","sender":"a","receiver":"b"}`),
		packetEv("timeout_on_close_packet", "8", "channel-0", "channel-3", ""),
		packetEv("recv_packet", "not-a-number", "channel-0", "channel-3", ""),
	})
	require.Len(t, evs, 2)
	require.Equal(t, "send_packet", evs[0].kind)
	require.Equal(t, uint64(7), evs[0].seq)
	require.Equal(t, "timeout_packet", evs[1].kind, "a timeout on close is a timeout")
}

// A packet Aether sends is received on the other chain and acknowledged
// back on Aether: one entry, all three steps. A packet the other chain
// sends and Aether hasn't received yet is in flight.
func TestJoinPackets_FollowsEachPacketAcrossBothChains(t *testing.T) {
	t0 := time.Unix(1_900_000_000, 0).UTC()
	data := `{"denom":"uaeth","amount":"15000000","sender":"aether1s","receiver":"cp1r"}`
	var evs []packetEvent
	evs = append(evs, at(packetEventsOf([]abci.Event{packetEv("send_packet", "402", "channel-0", "channel-3", data)}), "aether", 100, t0)...)
	evs = append(evs, at(packetEventsOf([]abci.Event{packetEv("recv_packet", "402", "channel-0", "channel-3", data)}), "ibc", 500, t0.Add(8*time.Second))...)
	evs = append(evs, at(packetEventsOf([]abci.Event{packetEv("acknowledge_packet", "402", "channel-0", "channel-3", "")}), "aether", 103, t0.Add(18*time.Second))...)
	evs = append(evs, at(packetEventsOf([]abci.Event{packetEv("send_packet", "9", "channel-3", "channel-0", `{"denom":"transfer/channel-3/uaeth","amount":"9000000"}`)}), "ibc", 505, t0.Add(20*time.Second))...)
	// Another channel of Aether's, not the one to this chain.
	evs = append(evs, at(packetEventsOf([]abci.Event{packetEv("send_packet", "1", "channel-5", "channel-0", data)}), "aether", 104, t0.Add(22*time.Second))...)

	ps := joinPackets(evs, "channel-0", "channel-3")
	require.Len(t, ps, 2, "the other channel's packet is left out")

	in := ps[0]
	require.Equal(t, "in", in.Direction)
	require.Equal(t, uint64(9), in.Sequence)
	require.Equal(t, "in-flight", in.Status)

	out := ps[1]
	require.Equal(t, "out", out.Direction)
	require.Equal(t, "acked", out.Status)
	require.Equal(t, "uaeth", out.Denom)
	require.Equal(t, "15000000", out.Amount)
	require.Equal(t, int64(100), out.Sent.Height)
	require.Equal(t, "ibc", out.Received.Chain)
	require.Equal(t, int64(103), out.Acked.Height)

	require.InDelta(t, 8.0, avgRelaySecs(ps), 0.001, "only the packet with both ends in the window counts")
}

// Both ends of a channel are often channel-0, so packet 1 each way has the
// same source port, channel and sequence. They're still two packets.
func TestJoinPackets_SameChannelIDBothWaysAreTwoPackets(t *testing.T) {
	t0 := time.Unix(1_900_000_000, 0).UTC()
	var evs []packetEvent
	evs = append(evs, at(packetEventsOf([]abci.Event{packetEv("send_packet", "1", "channel-0", "channel-0", "")}), "ibc", 50, t0)...)
	evs = append(evs, at(packetEventsOf([]abci.Event{packetEv("recv_packet", "1", "channel-0", "channel-0", "")}), "aether", 20, t0.Add(5*time.Second))...)
	evs = append(evs, at(packetEventsOf([]abci.Event{packetEv("send_packet", "1", "channel-0", "channel-0", "")}), "aether", 24, t0.Add(25*time.Second))...)

	ps := joinPackets(evs, "channel-0", "channel-0")
	require.Len(t, ps, 2)
	require.Equal(t, "out", ps[0].Direction)
	require.Equal(t, "in-flight", ps[0].Status)
	require.Nil(t, ps[0].Received, "the inbound packet's receipt isn't the outbound one's")
	require.Equal(t, "in", ps[1].Direction)
	require.Equal(t, "received", ps[1].Status)
}
