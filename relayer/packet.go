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
	timeout := uint64(time.Now().Add(10 * time.Minute).UnixNano())
	msg := transfertypes.NewMsgTransfer(transfertypes.PortID, srcChannel, coin, src.FromAddrStr, receiver, clienttypes.ZeroHeight(), timeout, "")
	events, err := src.SignAndBroadcast(msg)
	if err != nil {
		return channeltypes.Packet{}, fmt.Errorf("%s: MsgTransfer: %w", src.Name, err)
	}
	return packetFromEvents(events)
}

func packetFromEvents(events []abci.Event) (channeltypes.Packet, error) {
	get := func(key string) (string, error) {
		return EventAttr(events, channeltypes.EventTypeSendPacket, key)
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
	events, err := relayStep(src, dst, dstClient, func(h clienttypes.Height) (sdk.Msg, error) {
		resp, err := channelutils.QueryPacketCommitment(src.ClientCtx.WithHeight(int64(h.RevisionHeight)),
			packet.SourcePort, packet.SourceChannel, packet.Sequence, true)
		if err != nil {
			return nil, fmt.Errorf("%s: proving packet commitment %d: %w", src.Name, packet.Sequence, err)
		}
		return channeltypes.NewMsgRecvPacket(packet, resp.Proof, resp.ProofHeight, dst.FromAddrStr), nil
	})
	if err != nil {
		return fmt.Errorf("MsgRecvPacket: %w", err)
	}

	ackHex, err := EventAttr(events, channeltypes.EventTypeWriteAck, channeltypes.AttributeKeyAckHex)
	if err != nil {
		return err
	}
	ackBz, err := hex.DecodeString(ackHex)
	if err != nil {
		return err
	}
	var ack channeltypes.Acknowledgement
	if err := transfertypes.ModuleCdc.UnmarshalJSON(ackBz, &ack); err != nil {
		return fmt.Errorf("%s: decoding ack %q: %w", dst.Name, ackBz, err)
	}
	if !ack.Success() {
		return fmt.Errorf("%s: packet %d received with error ack: %s", dst.Name, packet.Sequence, ack.GetError())
	}

	_, err = relayStep(dst, src, srcClient, func(h clienttypes.Height) (sdk.Msg, error) {
		resp, err := channelutils.QueryPacketAcknowledgement(dst.ClientCtx.WithHeight(int64(h.RevisionHeight)),
			packet.DestinationPort, packet.DestinationChannel, packet.Sequence, true)
		if err != nil {
			return nil, fmt.Errorf("%s: proving ack %d: %w", dst.Name, packet.Sequence, err)
		}
		return channeltypes.NewMsgAcknowledgement(packet, ackBz, resp.Proof, resp.ProofHeight, src.FromAddrStr), nil
	})
	if err != nil {
		return fmt.Errorf("MsgAcknowledgement: %w", err)
	}

	// The ack's only visible effect on src is deleting the commitment.
	_, err = channelutils.QueryPacketCommitment(src.ClientCtx, packet.SourcePort, packet.SourceChannel, packet.Sequence, false)
	if err == nil {
		return fmt.Errorf("%s: packet %d commitment still present after ack", src.Name, packet.Sequence)
	}
	if status.Code(err) != codes.NotFound {
		return fmt.Errorf("%s: checking packet %d commitment: %w", src.Name, packet.Sequence, err)
	}
	return nil
}

// Balance returns addr's balance of denom on c.
func Balance(c *Chain, addr, denom string) (sdk.Coin, error) {
	resp, err := banktypes.NewQueryClient(c.ClientCtx).Balance(context.Background(), &banktypes.QueryBalanceRequest{Address: addr, Denom: denom})
	if err != nil {
		return sdk.Coin{}, fmt.Errorf("%s: balance of %s: %w", c.Name, addr, err)
	}
	return *resp.Balance, nil
}
