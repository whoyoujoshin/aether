package relayer

import (
	"fmt"

	abci "github.com/cometbft/cometbft/abci/types"

	sdk "github.com/cosmos/cosmos-sdk/types"

	transfertypes "github.com/cosmos/ibc-go/v8/modules/apps/transfer/types"
	clienttypes "github.com/cosmos/ibc-go/v8/modules/core/02-client/types"
	channelutils "github.com/cosmos/ibc-go/v8/modules/core/04-channel/client/utils"
	channeltypes "github.com/cosmos/ibc-go/v8/modules/core/04-channel/types"
)

// relayStep waits for src to commit past the previous step's tx,
// updates dst's client of src to src's tip H, lets build construct the
// handshake message from proofs taken on src at H, and submits both
// in one tx on dst.
func relayStep(src, dst *Chain, dstClientID string, build func(proofHeight clienttypes.Height) (sdk.Msg, error)) ([]abci.Event, error) {
	if err := src.WaitForNextBlock(); err != nil {
		return nil, err
	}
	upd, h, err := UpdateClientMsg(src, dst, dstClientID)
	if err != nil {
		return nil, err
	}
	msg, err := build(h)
	if err != nil {
		return nil, err
	}
	return dst.SignAndBroadcast(upd, msg)
}

func proveChannel(c *Chain, portID, channelID string, h clienttypes.Height) (*channeltypes.QueryChannelResponse, error) {
	resp, err := channelutils.QueryChannel(c.ClientCtx.WithHeight(int64(h.RevisionHeight)), portID, channelID, true)
	if err != nil {
		return nil, fmt.Errorf("%s: proving channel %s/%s: %w", c.Name, portID, channelID, err)
	}
	if !resp.ProofHeight.EQ(h) {
		return nil, fmt.Errorf("%s: channel proof at %s, want %s", c.Name, resp.ProofHeight, h)
	}
	return resp, nil
}

// OpenTransferChannel runs the four-step channel handshake for an
// unordered ICS-20 channel over the already-open connection connA (on
// a) / connB (on b): INIT on a, TRY on b, ACK on a, CONFIRM on b.
func OpenTransferChannel(a, b *Chain, clientA, clientB, connA, connB string) (chanA, chanB string, err error) {
	port := transfertypes.PortID

	initMsg := channeltypes.NewMsgChannelOpenInit(port, transfertypes.Version, channeltypes.UNORDERED, []string{connA}, port, a.FromAddrStr)
	events, err := a.SignAndBroadcast(initMsg)
	if err != nil {
		return "", "", fmt.Errorf("ChanOpenInit: %w", err)
	}
	if chanA, err = EventAttr(events, channeltypes.EventTypeChannelOpenInit, channeltypes.AttributeKeyChannelID); err != nil {
		return "", "", err
	}

	events, err = relayStep(a, b, clientB, func(h clienttypes.Height) (sdk.Msg, error) {
		p, err := proveChannel(a, port, chanA, h)
		if err != nil {
			return nil, err
		}
		return channeltypes.NewMsgChannelOpenTry(port, transfertypes.Version, channeltypes.UNORDERED, []string{connB},
			port, chanA, p.Channel.Version, p.Proof, p.ProofHeight, b.FromAddrStr), nil
	})
	if err != nil {
		return "", "", fmt.Errorf("ChanOpenTry: %w", err)
	}
	if chanB, err = EventAttr(events, channeltypes.EventTypeChannelOpenTry, channeltypes.AttributeKeyChannelID); err != nil {
		return "", "", err
	}

	if _, err = relayStep(b, a, clientA, func(h clienttypes.Height) (sdk.Msg, error) {
		p, err := proveChannel(b, port, chanB, h)
		if err != nil {
			return nil, err
		}
		return channeltypes.NewMsgChannelOpenAck(port, chanA, chanB, p.Channel.Version, p.Proof, p.ProofHeight, a.FromAddrStr), nil
	}); err != nil {
		return "", "", fmt.Errorf("ChanOpenAck: %w", err)
	}

	if _, err = relayStep(a, b, clientB, func(h clienttypes.Height) (sdk.Msg, error) {
		p, err := proveChannel(a, port, chanA, h)
		if err != nil {
			return nil, err
		}
		return channeltypes.NewMsgChannelOpenConfirm(port, chanB, p.Proof, p.ProofHeight, b.FromAddrStr), nil
	}); err != nil {
		return "", "", fmt.Errorf("ChanOpenConfirm: %w", err)
	}

	return chanA, chanB, nil
}

// ChannelState returns portID/channelID's state on c, e.g. STATE_OPEN.
func ChannelState(c *Chain, portID, channelID string) (channeltypes.State, error) {
	resp, err := channelutils.QueryChannel(c.ClientCtx, portID, channelID, false)
	if err != nil {
		return 0, err
	}
	return resp.Channel.State, nil
}
