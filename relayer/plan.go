package relayer

import (
	"context"
	"fmt"
	"time"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/query"
	clientutils "github.com/cosmos/ibc-go/v8/modules/core/02-client/client/utils"
	clienttypes "github.com/cosmos/ibc-go/v8/modules/core/02-client/types"
	connectiontypes "github.com/cosmos/ibc-go/v8/modules/core/03-connection/types"
	channelutils "github.com/cosmos/ibc-go/v8/modules/core/04-channel/client/utils"
	channeltypes "github.com/cosmos/ibc-go/v8/modules/core/04-channel/types"
	ibcexported "github.com/cosmos/ibc-go/v8/modules/core/exported"
	ibctm "github.com/cosmos/ibc-go/v8/modules/light-clients/07-tendermint"
)

// PlanResult is what Plan found.
type PlanResult struct {
	// DstTip is dst's committed height when Plan read it.
	DstTip int64
	// Msgs are the relay messages, in execution order.
	Msgs []sdk.Msg
	// ProofHeight is the src height every proof is at.
	ProofHeight clienttypes.Height
	// Update is whether Msgs starts with a client update.
	Update                  bool
	Packets, Acks, Timeouts int
}

// Any reports whether there is anything to relay.
func (r *PlanResult) Any() bool { return r.Update || r.Packets+r.Acks+r.Timeouts > 0 }

// Plan computes the messages that relay everything pending from src onto
// dst, over dst's client clientID (which tracks src), as of src's latest
// block: a client update if needed, then packets src sent that dst hasn't
// received, acknowledgements src wrote for packets dst sent, and timeouts
// for dst's packets src never received -- each with its proof, in
// execution order, at most maxMsgs besides the update. Every message
// names signer. Helicase runs it with Aether as dst and an empty signer
// (the relay transaction sets its own); the outbound relayer with Aether
// as src and its own key's address on the other chain. refreshAfter is
// how stale the client may get on a quiet channel before an update is
// sent anyway (see refreshThreshold); zero or less leaves only the
// trusting-period rule.
func Plan(src, dst *Chain, clientID, signer string, maxMsgs int, refreshAfter time.Duration) (*PlanResult, error) {
	ctx := context.Background()
	var work PlanResult

	dstTip, err := dst.LatestHeight()
	if err != nil {
		return nil, err
	}
	cs, err := clientStateOf(dst, clientID)
	if err != nil {
		return nil, err
	}
	if !cs.FrozenHeight.IsZero() {
		return nil, fmt.Errorf("client %s is frozen at %s", clientID, cs.FrozenHeight)
	}
	src.ChainID = cs.ChainId
	src.ClientCtx = src.ClientCtx.WithChainID(cs.ChainId)

	channels, err := openChannels(ctx, dst, clientID)
	if err != nil {
		return nil, err
	}

	// Update the client to the other chain's tip and prove everything
	// there; if the client is already there, prove at its height.
	var update sdk.Msg
	proofHeight := cs.LatestHeight
	srcTip, err := src.LatestHeight()
	if err != nil {
		return nil, err
	}
	if uint64(srcTip) > cs.LatestHeight.RevisionHeight {
		update, proofHeight, err = UpdateClientMsg(src, dst, clientID)
		if err != nil {
			return nil, err
		}
	}
	work.ProofHeight = proofHeight
	ph := int64(proofHeight.RevisionHeight)
	proofTime, err := blockTime(src, ph)
	if err != nil {
		return nil, err
	}
	dstTime, err := blockTime(dst, dstTip)
	if err != nil {
		return nil, err
	}

	// State as of the proof height: a proof at ph proves the state the
	// block before it left.
	srcState := src.ClientCtx.WithHeight(ph - 1)
	srcProve := src.ClientCtx.WithHeight(ph)
	srcChannels := channeltypes.NewQueryClient(srcState)
	dstChannels := channeltypes.NewQueryClient(dst.ClientCtx)

	var msgs []sdk.Msg
	full := func() bool { return len(msgs) >= maxMsgs }

	for _, ch := range channels {
		srcPort, srcChannel := ch.Counterparty.PortId, ch.Counterparty.ChannelId

		// Packets in.
		sent, err := srcChannels.PacketCommitments(ctx, &channeltypes.QueryPacketCommitmentsRequest{
			PortId: srcPort, ChannelId: srcChannel, Pagination: &query.PageRequest{Limit: 1000},
		})
		if err != nil {
			return nil, fmt.Errorf("%s: packet commitments on %s/%s: %w", src.Name, srcPort, srcChannel, err)
		}
		if seqs := commitmentSeqs(sent.Commitments); len(seqs) > 0 {
			unrec, err := dstChannels.UnreceivedPackets(ctx, &channeltypes.QueryUnreceivedPacketsRequest{
				PortId: ch.PortId, ChannelId: ch.ChannelId, PacketCommitmentSequences: seqs,
			})
			if err != nil {
				return nil, fmt.Errorf("%s: unreceived packets on %s/%s: %w", dst.Name, ch.PortId, ch.ChannelId, err)
			}
			for _, seq := range unrec.Sequences {
				if full() {
					break
				}
				packet, err := FindSentPacket(src, srcPort, srcChannel, seq)
				if err != nil {
					return nil, err
				}
				// Aether would refuse a packet past its timeout; the other
				// chain times it out instead.
				if timedOut(packet, uint64(dstTip), dstTime) {
					continue
				}
				proof, err := channelutils.QueryPacketCommitment(srcProve, srcPort, srcChannel, seq, true)
				if err != nil {
					return nil, fmt.Errorf("%s: proving packet %d: %w", src.Name, seq, err)
				}
				msgs = append(msgs, channeltypes.NewMsgRecvPacket(packet, proof.Proof, proof.ProofHeight, signer))
				work.Packets++
			}
		}

		// Acknowledgements and timeouts for packets Aether sent.
		ours, err := dstChannels.PacketCommitments(ctx, &channeltypes.QueryPacketCommitmentsRequest{
			PortId: ch.PortId, ChannelId: ch.ChannelId, Pagination: &query.PageRequest{Limit: 1000},
		})
		if err != nil {
			return nil, fmt.Errorf("%s: packet commitments on %s/%s: %w", dst.Name, ch.PortId, ch.ChannelId, err)
		}
		seqs := commitmentSeqs(ours.Commitments)
		if len(seqs) == 0 {
			continue
		}
		acks, err := srcChannels.PacketAcknowledgements(ctx, &channeltypes.QueryPacketAcknowledgementsRequest{
			PortId: srcPort, ChannelId: srcChannel, PacketCommitmentSequences: seqs,
		})
		if err != nil {
			return nil, fmt.Errorf("%s: acknowledgements on %s/%s: %w", src.Name, srcPort, srcChannel, err)
		}
		for _, a := range acks.Acknowledgements {
			if full() {
				break
			}
			packet, ack, err := FindAcknowledgement(src, srcPort, srcChannel, a.Sequence)
			if err != nil {
				return nil, err
			}
			proof, err := channelutils.QueryPacketAcknowledgement(srcProve, srcPort, srcChannel, a.Sequence, true)
			if err != nil {
				return nil, fmt.Errorf("%s: proving acknowledgement %d: %w", src.Name, a.Sequence, err)
			}
			msgs = append(msgs, channeltypes.NewMsgAcknowledgement(packet, ack, proof.Proof, proof.ProofHeight, signer))
			work.Acks++
		}

		unrec, err := srcChannels.UnreceivedPackets(ctx, &channeltypes.QueryUnreceivedPacketsRequest{
			PortId: srcPort, ChannelId: srcChannel, PacketCommitmentSequences: seqs,
		})
		if err != nil {
			return nil, fmt.Errorf("%s: unreceived packets on %s/%s: %w", src.Name, srcPort, srcChannel, err)
		}
		for _, seq := range unrec.Sequences {
			if full() {
				break
			}
			packet, err := FindSentPacket(dst, ch.PortId, ch.ChannelId, seq)
			if err != nil {
				return nil, err
			}
			// Timed out as of the proof height's block on the other chain,
			// which is what 07-tendermint checks.
			if !timedOut(packet, proofHeight.RevisionHeight, proofTime) {
				continue
			}
			var (
				proofBz []byte
				nextSeq = seq
				proven  clienttypes.Height
			)
			if ch.Ordering == channeltypes.ORDERED {
				res, err := channelutils.QueryNextSequenceReceive(srcProve, srcPort, srcChannel, true)
				if err != nil {
					return nil, fmt.Errorf("%s: proving next receive sequence: %w", src.Name, err)
				}
				proofBz, nextSeq, proven = res.Proof, res.NextSequenceReceive, res.ProofHeight
			} else {
				res, err := channelutils.QueryPacketReceipt(srcProve, srcPort, srcChannel, seq, true)
				if err != nil {
					return nil, fmt.Errorf("%s: proving packet %d was never received: %w", src.Name, seq, err)
				}
				proofBz, proven = res.Proof, res.ProofHeight
			}
			msgs = append(msgs, channeltypes.NewMsgTimeout(packet, nextSeq, proofBz, proven, signer))
			work.Timeouts++
		}
	}

	if update != nil && (len(msgs) > 0 || needsRefresh(dst, clientID, cs, proofTime, refreshAfter)) {
		msgs = append([]sdk.Msg{update}, msgs...)
		work.Update = true
	}
	work.DstTip, work.Msgs = dstTip, msgs
	return &work, nil
}

// needsRefresh: with no packets to carry it, the client is still updated
// once its latest consensus state is older than refreshThreshold, so it
// never expires on a quiet channel and never lags far behind.
func needsRefresh(dst *Chain, clientID string, cs *ibctm.ClientState, now time.Time, refreshAfter time.Duration) bool {
	res, err := clientutils.QueryConsensusStateABCI(dst.ClientCtx, clientID, cs.LatestHeight)
	if err != nil {
		return true
	}
	var cons ibcexported.ConsensusState
	if err := dst.ClientCtx.InterfaceRegistry.UnpackAny(res.ConsensusState, &cons); err != nil {
		return true
	}
	tmCons, ok := cons.(*ibctm.ConsensusState)
	if !ok {
		return true
	}
	return now.Sub(tmCons.Timestamp) > refreshThreshold(cs.TrustingPeriod, refreshAfter)
}

// refreshThreshold is the shorter of a third of the trusting period
// (the margin that keeps a client from expiring) and refreshAfter.
//
// The second matters for senders, not for the client's safety: a
// transfer whose timeout is relative (ibc-go's CLI default is 1,000
// blocks) counts from the sending chain's view of the receiving chain,
// which is this client's latest height. On a quiet channel refreshed
// only at a third of an 80 h trusting period, that view was a day
// behind, so a default transfer from Aether to Osmosis (0.7 s blocks:
// 1,000 blocks is ~12 min) timed out before it was sent (4 October
// 2026, packet 2 on channel-1).
func refreshThreshold(trustingPeriod, refreshAfter time.Duration) time.Duration {
	t := trustingPeriod / 3
	if refreshAfter > 0 && refreshAfter < t {
		return refreshAfter
	}
	return t
}

func clientStateOf(dst *Chain, clientID string) (*ibctm.ClientState, error) {
	res, err := clientutils.QueryClientState(dst.ClientCtx, clientID, false)
	if err != nil {
		return nil, fmt.Errorf("%s: client %s: %w", dst.Name, clientID, err)
	}
	var cs ibcexported.ClientState
	if err := dst.ClientCtx.InterfaceRegistry.UnpackAny(res.ClientState, &cs); err != nil {
		return nil, err
	}
	tmCS, ok := cs.(*ibctm.ClientState)
	if !ok {
		return nil, fmt.Errorf("%s: client %s is %T, not 07-tendermint", dst.Name, clientID, cs)
	}
	return tmCS, nil
}

// channels is every open channel on a connection over the client.
func openChannels(ctx context.Context, dst *Chain, clientID string) ([]*channeltypes.IdentifiedChannel, error) {
	conns, err := connectiontypes.NewQueryClient(dst.ClientCtx).ClientConnections(ctx,
		&connectiontypes.QueryClientConnectionsRequest{ClientId: clientID})
	if err != nil {
		return nil, fmt.Errorf("%s: connections on client %s: %w", dst.Name, clientID, err)
	}
	var out []*channeltypes.IdentifiedChannel
	for _, conn := range conns.ConnectionPaths {
		res, err := channeltypes.NewQueryClient(dst.ClientCtx).ConnectionChannels(ctx,
			&channeltypes.QueryConnectionChannelsRequest{Connection: conn, Pagination: &query.PageRequest{Limit: 1000}})
		if err != nil {
			return nil, fmt.Errorf("%s: channels on %s: %w", dst.Name, conn, err)
		}
		for _, ch := range res.Channels {
			if ch.State == channeltypes.OPEN {
				out = append(out, ch)
			}
		}
	}
	return out, nil
}

func commitmentSeqs(states []*channeltypes.PacketState) []uint64 {
	seqs := make([]uint64, 0, len(states))
	for _, s := range states {
		seqs = append(seqs, s.Sequence)
	}
	return seqs
}

// timedOut reports whether packet has timed out on a chain at height
// (revision height) and time.
func timedOut(packet channeltypes.Packet, height uint64, t time.Time) bool {
	if !packet.TimeoutHeight.IsZero() && height >= packet.TimeoutHeight.RevisionHeight {
		return true
	}
	return packet.TimeoutTimestamp != 0 && uint64(t.UnixNano()) >= packet.TimeoutTimestamp
}

func blockTime(c *Chain, height int64) (time.Time, error) {
	node, err := c.ClientCtx.GetNode()
	if err != nil {
		return time.Time{}, err
	}
	res, err := node.Commit(context.Background(), &height)
	if err != nil {
		return time.Time{}, fmt.Errorf("%s: header %d: %w", c.Name, height, err)
	}
	return res.Time, nil
}

// ClientExpiry is when dst's client clientID stops trusting src if it
// isn't updated: its latest consensus state's time plus its trusting
// period.
func ClientExpiry(dst *Chain, clientID string) (time.Time, error) {
	cs, err := clientStateOf(dst, clientID)
	if err != nil {
		return time.Time{}, err
	}
	res, err := clientutils.QueryConsensusStateABCI(dst.ClientCtx, clientID, cs.LatestHeight)
	if err != nil {
		return time.Time{}, fmt.Errorf("%s: consensus state of %s: %w", dst.Name, clientID, err)
	}
	var cons ibcexported.ConsensusState
	if err := dst.ClientCtx.InterfaceRegistry.UnpackAny(res.ConsensusState, &cons); err != nil {
		return time.Time{}, err
	}
	tmCons, ok := cons.(*ibctm.ConsensusState)
	if !ok {
		return time.Time{}, fmt.Errorf("%s: client %s's consensus state is %T", dst.Name, clientID, cons)
	}
	return tmCons.Timestamp.Add(cs.TrustingPeriod), nil
}
