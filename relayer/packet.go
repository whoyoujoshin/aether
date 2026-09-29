package relayer

import (
	"context"
	"encoding/hex"
	"fmt"
	"strconv"
	"time"

	abci "github.com/cometbft/cometbft/abci/types"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	sdk "github.com/cosmos/cosmos-sdk/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"

	transfertypes "github.com/cosmos/ibc-go/v8/modules/apps/transfer/types"
	clienttypes "github.com/cosmos/ibc-go/v8/modules/core/02-client/types"
	channelutils "github.com/cosmos/ibc-go/v8/modules/core/04-channel/client/utils"
	channeltypes "github.com/cosmos/ibc-go/v8/modules/core/04-channel/types"
)

// Transfer submits an ICS-20 MsgTransfer of coin from src's signer to
// receiver (an address on the other chain) over srcChannel, and returns
// the packet src committed.
func Transfer(src *Chain, srcChannel string, coin sdk.Coin, receiver string) (channeltypes.Packet, error) {
	return TransferWithTimeout(src, srcChannel, coin, receiver, 10*time.Minute)
}

// TransferWithTimeout is Transfer with a packet timeout this far from now.
func TransferWithTimeout(src *Chain, srcChannel string, coin sdk.Coin, receiver string, after time.Duration) (channeltypes.Packet, error) {
	timeout := uint64(time.Now().Add(after).UnixNano())
	msg := transfertypes.NewMsgTransfer(transfertypes.PortID, srcChannel, coin, src.FromAddrStr, receiver, clienttypes.ZeroHeight(), timeout, "")
	events, err := src.SignAndBroadcast(msg)
	if err != nil {
		return channeltypes.Packet{}, fmt.Errorf("%s: MsgTransfer: %w", src.Name, err)
	}
	return packetFromEvents(events)
}

func packetFromEvents(events []abci.Event) (channeltypes.Packet, error) {
	return packetFromEvent(events, channeltypes.EventTypeSendPacket)
}

// packetFromEvent reads the packet from the first eventType event:
// send_packet and write_acknowledgement both carry its fields.
func packetFromEvent(events []abci.Event, eventType string) (channeltypes.Packet, error) {
	get := func(key string) (string, error) {
		return EventAttr(events, eventType, key)
	}
	var (
		p   channeltypes.Packet
		err error
		v   string
	)
	if v, err = get(channeltypes.AttributeKeyDataHex); err != nil {
		return p, err
	}
	if p.Data, err = hex.DecodeString(v); err != nil {
		return p, err
	}
	if v, err = get(channeltypes.AttributeKeySequence); err != nil {
		return p, err
	}
	if p.Sequence, err = strconv.ParseUint(v, 10, 64); err != nil {
		return p, err
	}
	if v, err = get(channeltypes.AttributeKeyTimeoutHeight); err != nil {
		return p, err
	}
	if p.TimeoutHeight, err = clienttypes.ParseHeight(v); err != nil {
		return p, err
	}
	if v, err = get(channeltypes.AttributeKeyTimeoutTimestamp); err != nil {
		return p, err
	}
	if p.TimeoutTimestamp, err = strconv.ParseUint(v, 10, 64); err != nil {
		return p, err
	}
	for key, dst := range map[string]*string{
		channeltypes.AttributeKeySrcPort:    &p.SourcePort,
		channeltypes.AttributeKeySrcChannel: &p.SourceChannel,
		channeltypes.AttributeKeyDstPort:    &p.DestinationPort,
		channeltypes.AttributeKeyDstChannel: &p.DestinationChannel,
	} {
		if *dst, err = get(key); err != nil {
			return p, err
		}
	}
	return p, nil
}

// RelayPacket delivers packet (committed on src) to dst with a proof of
// its commitment, checks that dst wrote a success acknowledgement --
// a MsgRecvPacket tx can land fine and still record an error ack, e.g.
// if dst's transfer module refuses the denom -- and relays that ack
// back to src so src clears the commitment. dstClient lives on dst and
// tracks src; srcClient lives on src and tracks dst.
func RelayPacket(src, dst *Chain, srcClient, dstClient string, packet channeltypes.Packet) error {
	ackBz, err := DeliverPacket(src, dst, dstClient, packet)
	if err != nil {
		return err
	}
	return DeliverAcknowledgement(dst, src, srcClient, packet, ackBz)
}

// DeliverPacket delivers packet (committed on src) to dst, signing on
// dst, and returns the success acknowledgement dst wrote.
func DeliverPacket(src, dst *Chain, dstClient string, packet channeltypes.Packet) ([]byte, error) {
	events, err := relayStep(src, dst, dstClient, func(h clienttypes.Height) (sdk.Msg, error) {
		resp, err := channelutils.QueryPacketCommitment(src.ClientCtx.WithHeight(int64(h.RevisionHeight)),
			packet.SourcePort, packet.SourceChannel, packet.Sequence, true)
		if err != nil {
			return nil, fmt.Errorf("%s: proving packet commitment %d: %w", src.Name, packet.Sequence, err)
		}
		return channeltypes.NewMsgRecvPacket(packet, resp.Proof, resp.ProofHeight, dst.FromAddrStr), nil
	})
	if err != nil {
		return nil, fmt.Errorf("MsgRecvPacket: %w", err)
	}

	ackHex, err := EventAttr(events, channeltypes.EventTypeWriteAck, channeltypes.AttributeKeyAckHex)
	if err != nil {
		return nil, err
	}
	ackBz, err := hex.DecodeString(ackHex)
	if err != nil {
		return nil, err
	}
	var ack channeltypes.Acknowledgement
	if err := transfertypes.ModuleCdc.UnmarshalJSON(ackBz, &ack); err != nil {
		return nil, fmt.Errorf("%s: decoding ack %q: %w", dst.Name, ackBz, err)
	}
	if !ack.Success() {
		return nil, fmt.Errorf("%s: packet %d received with error ack: %s", dst.Name, packet.Sequence, ack.GetError())
	}
	return ackBz, nil
}

// DeliverAcknowledgement relays ackBz, which acker wrote on receiving
// packet, back to sender (the chain that sent packet), signing on
// sender, and checks sender cleared the packet's commitment.
// senderClient lives on sender and tracks acker.
func DeliverAcknowledgement(acker, sender *Chain, senderClient string, packet channeltypes.Packet, ackBz []byte) error {
	_, err := relayStep(acker, sender, senderClient, func(h clienttypes.Height) (sdk.Msg, error) {
		resp, err := channelutils.QueryPacketAcknowledgement(acker.ClientCtx.WithHeight(int64(h.RevisionHeight)),
			packet.DestinationPort, packet.DestinationChannel, packet.Sequence, true)
		if err != nil {
			return nil, fmt.Errorf("%s: proving ack %d: %w", acker.Name, packet.Sequence, err)
		}
		return channeltypes.NewMsgAcknowledgement(packet, ackBz, resp.Proof, resp.ProofHeight, sender.FromAddrStr), nil
	})
	if err != nil {
		return fmt.Errorf("MsgAcknowledgement: %w", err)
	}

	// The ack's only visible effect on sender is deleting the commitment.
	cleared, err := CommitmentCleared(sender, packet)
	if err != nil {
		return err
	}
	if !cleared {
		return fmt.Errorf("%s: packet %d commitment still present after ack", sender.Name, packet.Sequence)
	}
	return nil
}

// CommitmentCleared reports whether c, which sent packet, no longer holds
// its commitment: it was acknowledged or timed out.
func CommitmentCleared(c *Chain, packet channeltypes.Packet) (bool, error) {
	_, err := channelutils.QueryPacketCommitment(c.ClientCtx, packet.SourcePort, packet.SourceChannel, packet.Sequence, false)
	if err == nil {
		return false, nil
	}
	if status.Code(err) == codes.NotFound {
		return true, nil
	}
	return false, fmt.Errorf("%s: checking packet %d commitment: %w", c.Name, packet.Sequence, err)
}

// Balance returns addr's balance of denom on c.
func Balance(c *Chain, addr, denom string) (sdk.Coin, error) {
	resp, err := banktypes.NewQueryClient(c.ClientCtx).Balance(context.Background(), &banktypes.QueryBalanceRequest{Address: addr, Denom: denom})
	if err != nil {
		return sdk.Coin{}, fmt.Errorf("%s: balance of %s: %w", c.Name, addr, err)
	}
	return *resp.Balance, nil
}

// FindSentPacket finds the packet c sent from port/channel with sequence
// seq, from the send_packet event of the transaction that sent it.
func FindSentPacket(c *Chain, port, channel string, seq uint64) (channeltypes.Packet, error) {
	ev, err := findEvent(c, channeltypes.EventTypeSendPacket, map[string]string{
		channeltypes.AttributeKeySrcPort:    port,
		channeltypes.AttributeKeySrcChannel: channel,
		channeltypes.AttributeKeySequence:   strconv.FormatUint(seq, 10),
	})
	if err != nil {
		return channeltypes.Packet{}, err
	}
	return packetFromEvent([]abci.Event{ev}, channeltypes.EventTypeSendPacket)
}

// FindAcknowledgement finds the acknowledgement c wrote for packet seq it
// received on port/channel, and that packet, from the
// write_acknowledgement event of the transaction that received it.
func FindAcknowledgement(c *Chain, port, channel string, seq uint64) (channeltypes.Packet, []byte, error) {
	ev, err := findEvent(c, channeltypes.EventTypeWriteAck, map[string]string{
		channeltypes.AttributeKeyDstPort:    port,
		channeltypes.AttributeKeyDstChannel: channel,
		channeltypes.AttributeKeySequence:   strconv.FormatUint(seq, 10),
	})
	if err != nil {
		return channeltypes.Packet{}, nil, err
	}
	events := []abci.Event{ev}
	packet, err := packetFromEvent(events, channeltypes.EventTypeWriteAck)
	if err != nil {
		return channeltypes.Packet{}, nil, err
	}
	ackHex, err := EventAttr(events, channeltypes.EventTypeWriteAck, channeltypes.AttributeKeyAckHex)
	if err != nil {
		return channeltypes.Packet{}, nil, err
	}
	ack, err := hex.DecodeString(ackHex)
	if err != nil {
		return channeltypes.Packet{}, nil, err
	}
	return packet, ack, nil
}

// findEvent tx-searches c for an eventType event carrying every one of
// attrs, and returns that event.
func findEvent(c *Chain, eventType string, attrs map[string]string) (abci.Event, error) {
	node, err := c.ClientCtx.GetNode()
	if err != nil {
		return abci.Event{}, err
	}
	query := ""
	for k, v := range attrs {
		if query != "" {
			query += " AND "
		}
		query += fmt.Sprintf("%s.%s='%s'", eventType, k, v)
	}
	page, perPage := 1, 10
	res, err := node.TxSearch(context.Background(), query, false, &page, &perPage, "desc")
	if err != nil {
		return abci.Event{}, fmt.Errorf("%s: searching %s: %w", c.Name, query, err)
	}
	for _, tx := range res.Txs {
	events:
		for _, ev := range tx.TxResult.Events {
			if ev.Type != eventType {
				continue
			}
			for k, v := range attrs {
				found := false
				for _, a := range ev.Attributes {
					if a.Key == k && a.Value == v {
						found = true
						break
					}
				}
				if !found {
					continue events
				}
			}
			return ev, nil
		}
	}
	return abci.Event{}, fmt.Errorf("%s: no transaction with %s", c.Name, query)
}
