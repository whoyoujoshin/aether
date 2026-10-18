package wallet

import (
	"context"
	"fmt"
	"strconv"

	abci "github.com/cometbft/cometbft/abci/types"
	txtypes "github.com/cosmos/cosmos-sdk/types/tx"
	channeltypes "github.com/cosmos/ibc-go/v8/modules/core/04-channel/types"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// What became of an ICS-20 packet this chain sent, as seen from here.
const (
	PacketInFlight = "in_flight" // its commitment is still stored: no ack or timeout yet
	PacketAcked    = "acked"     // the other chain acknowledged it with success
	PacketFailed   = "failed"    // acknowledged with an error: the tokens were refunded
	PacketTimedOut = "timed_out" // timed out: the tokens were refunded
	PacketSettled  = "settled"   // acked or timed out, but the transaction isn't indexed here
)

// PacketOutcome is a sent packet's fate.
type PacketOutcome struct {
	Status string
	Error  string // the error acknowledgement, for PacketFailed
	TxHash string // the transaction that acknowledged or timed it out
	Height int64
}

// SentPacketSequence is the sequence of the packet txHash sent on
// srcChannel, from its send_packet event.
func (c *Client) SentPacketSequence(ctx context.Context, txHash, srcChannel string) (uint64, error) {
	res, err := txtypes.NewServiceClient(c.conn).GetTx(ctx, &txtypes.GetTxRequest{Hash: txHash})
	if status.Code(err) == codes.NotFound {
		return 0, fmt.Errorf("%w: %s", ErrTransactionNotFound, txHash)
	}
	if err != nil {
		return 0, err
	}
	for _, ev := range res.TxResponse.Events {
		if ev.Type != channeltypes.EventTypeSendPacket {
			continue
		}
		var seq, ch string
		for _, a := range ev.Attributes {
			switch a.Key {
			case channeltypes.AttributeKeySequence:
				seq = a.Value
			case channeltypes.AttributeKeySrcChannel:
				ch = a.Value
			}
		}
		if ch == srcChannel && seq != "" {
			return strconv.ParseUint(seq, 10, 64)
		}
	}
	return 0, fmt.Errorf("transaction %s sent no packet on %s", txHash, srcChannel)
}

// SentPacketOutcome says whether the packet srcChannel sent as seq is
// still in flight and, once it isn't, how it ended: the acknowledgement
// or timeout transaction is found by its events, and the ICS-20 result
// is read from the event with the same message index.
func (c *Client) SentPacketOutcome(ctx context.Context, port, srcChannel string, seq uint64) (*PacketOutcome, error) {
	_, err := channeltypes.NewQueryClient(c.conn).PacketCommitment(ctx, &channeltypes.QueryPacketCommitmentRequest{
		PortId: port, ChannelId: srcChannel, Sequence: seq,
	})
	if err == nil {
		return &PacketOutcome{Status: PacketInFlight}, nil
	}
	if status.Code(err) != codes.NotFound {
		return nil, err
	}
	if o, err := c.packetSettledBy(ctx, channeltypes.EventTypeAcknowledgePacket, srcChannel, seq); err != nil || o != nil {
		return o, err
	}
	if o, err := c.packetSettledBy(ctx, channeltypes.EventTypeTimeoutPacket, srcChannel, seq); err != nil || o != nil {
		return o, err
	}
	return &PacketOutcome{Status: PacketSettled}, nil
}

func (c *Client) packetSettledBy(ctx context.Context, eventType, srcChannel string, seq uint64) (*PacketOutcome, error) {
	q := fmt.Sprintf("%s.%s='%s' AND %s.%s='%d'", eventType, channeltypes.AttributeKeySrcChannel, srcChannel, eventType, channeltypes.AttributeKeySequence, seq)
	res, err := txtypes.NewServiceClient(c.conn).GetTxsEvent(ctx, &txtypes.GetTxsEventRequest{Query: q, Limit: 5})
	if err != nil {
		return nil, err
	}
	for _, tx := range res.TxResponses {
		if o := packetOutcomeIn(tx.Events, eventType, srcChannel, seq); o != nil {
			o.TxHash, o.Height = tx.TxHash, tx.Height
			return o, nil
		}
	}
	return nil, nil
}

// packetOutcomeIn reads a relay transaction's events: the eventType
// (acknowledge_packet or timeout_packet) for srcChannel/seq, then the
// ICS-20 result with the same msg_index, since one relay may carry
// several packets. nil if the transaction doesn't settle this packet.
func packetOutcomeIn(events []abci.Event, eventType, srcChannel string, seq uint64) *PacketOutcome {
	want := strconv.FormatUint(seq, 10)
	msgIndex, found := "", false
	for _, ev := range events {
		if ev.Type != eventType {
			continue
		}
		var s, ch, idx string
		for _, a := range ev.Attributes {
			switch a.Key {
			case channeltypes.AttributeKeySequence:
				s = a.Value
			case channeltypes.AttributeKeySrcChannel:
				ch = a.Value
			case "msg_index":
				idx = a.Value
			}
		}
		if ch == srcChannel && s == want {
			msgIndex, found = idx, true
			break
		}
	}
	if !found {
		return nil
	}
	if eventType == channeltypes.EventTypeTimeoutPacket {
		return &PacketOutcome{Status: PacketTimedOut}
	}
	o := &PacketOutcome{Status: PacketAcked}
	for _, ev := range events {
		if ev.Type != "fungible_token_packet" {
			continue
		}
		var idx, ackErr string
		for _, a := range ev.Attributes {
			switch a.Key {
			case "msg_index":
				idx = a.Value
			case "error":
				ackErr = a.Value
			}
		}
		if idx == msgIndex && ackErr != "" {
			o.Status, o.Error = PacketFailed, ackErr
		}
	}
	return o
}
