package wallet

import (
	"testing"

	abci "github.com/cometbft/cometbft/abci/types"
	"github.com/stretchr/testify/require"
)

func ev(typ string, kv ...string) abci.Event {
	e := abci.Event{Type: typ}
	for i := 0; i+1 < len(kv); i += 2 {
		e.Attributes = append(e.Attributes, abci.EventAttribute{Key: kv[i], Value: kv[i+1]})
	}
	return e
}

// A relay carrying two acks, as ibc-go v8 and SDK 0.50 emit them: each
// message's events carry its msg_index; an error ack adds an
// fungible_token_packet event with "error".
func TestPacketOutcomeIn_BatchedAcks(t *testing.T) {
	events := []abci.Event{
		ev("acknowledge_packet", "packet_sequence", "5", "packet_src_channel", "channel-3", "msg_index", "1"),
		ev("fungible_token_packet", "module", "transfer", "acknowledgement", "result:\"\\001\"", "msg_index", "1"),
		ev("fungible_token_packet", "success", "\x01", "msg_index", "1"),
		ev("acknowledge_packet", "packet_sequence", "7", "packet_src_channel", "channel-3", "msg_index", "2"),
		ev("fungible_token_packet", "module", "transfer", "msg_index", "2"),
		ev("fungible_token_packet", "error", "ABCI code: 6: error handling packet: see events for details", "msg_index", "2"),
		// the same sequence on another channel is someone else's packet
		ev("acknowledge_packet", "packet_sequence", "9", "packet_src_channel", "channel-2", "msg_index", "3"),
	}
	require.Equal(t, &PacketOutcome{Status: PacketAcked}, packetOutcomeIn(events, "acknowledge_packet", "channel-3", 5))
	require.Equal(t, &PacketOutcome{Status: PacketFailed, Error: "ABCI code: 6: error handling packet: see events for details"},
		packetOutcomeIn(events, "acknowledge_packet", "channel-3", 7))
	require.Nil(t, packetOutcomeIn(events, "acknowledge_packet", "channel-3", 9))
	require.Nil(t, packetOutcomeIn(events, "acknowledge_packet", "channel-3", 6))
}

func TestPacketOutcomeIn_Timeout(t *testing.T) {
	events := []abci.Event{
		ev("timeout_packet", "packet_sequence", "7", "packet_src_channel", "channel-3", "msg_index", "0"),
		ev("timeout", "refund_receiver", "aether1x", "msg_index", "0"),
	}
	require.Equal(t, &PacketOutcome{Status: PacketTimedOut}, packetOutcomeIn(events, "timeout_packet", "channel-3", 7))
	require.Nil(t, packetOutcomeIn(events, "acknowledge_packet", "channel-3", 7))
}
