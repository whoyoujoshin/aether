// Package helicase is the node-side half of Helicase (see app/helicase.go):
// a worker inside aetherd that watches another chain and prepares the
// relay transactions this node puts in its blocks when it proposes.
//
// Each cycle it reads Aether's light client of the other chain, finds
// every open channel over it, and gathers, as of the other chain's
// latest block:
//
//   - packets the other chain sent that Aether hasn't received;
//   - acknowledgements the other chain wrote for packets Aether sent,
//     which Aether hasn't processed yet;
//   - packets Aether sent that the other chain never received before
//     their timeout, so Aether can refund them;
//
// each with its proof, behind one client update to that block. Each
// message becomes its own relay transaction, so one that fails (say, a
// packet another proposer delivered first) doesn't take the others with
// it. The worker signs nothing and holds no key: it reads the other chain
// over RPC, and the light client checks everything it proves.
package helicase

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"cosmossdk.io/log"
	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/codec"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/query"
	clientutils "github.com/cosmos/ibc-go/v8/modules/core/02-client/client/utils"
	clienttypes "github.com/cosmos/ibc-go/v8/modules/core/02-client/types"
	connectiontypes "github.com/cosmos/ibc-go/v8/modules/core/03-connection/types"
	channelutils "github.com/cosmos/ibc-go/v8/modules/core/04-channel/client/utils"
	channeltypes "github.com/cosmos/ibc-go/v8/modules/core/04-channel/types"
	ibcexported "github.com/cosmos/ibc-go/v8/modules/core/exported"
	ibctm "github.com/cosmos/ibc-go/v8/modules/light-clients/07-tendermint"

	"github.com/whoyoujoshin/aether/relayer"
)

// Config is one path Helicase relays in from.
type Config struct {
	// CounterpartyRPC is the other chain's CometBFT RPC. It must serve
	// tx_search (a transaction index) and proofs.
	CounterpartyRPC string
	// ClientID is Aether's 07-tendermint client of the other chain;
	// every open channel over a connection on it is relayed.
	ClientID string
	// AetherRPC is this node's own RPC.
	AetherRPC string
	// Interval is how often to look for work.
	Interval time.Duration
}

// maxMsgsPerCycle leaves room under the chain's cap on relay
// transactions per block (helicaseMaxTxsPerBlock) for the client update.
const maxMsgsPerCycle = 60

// staleAfter: a batch computed more than this many blocks before the one
// being proposed isn't proposed; the next cycle replaces it.
const staleAfter = 2

// Worker implements app.HelicaseSource.
type Worker struct {
	cfg      Config
	txConfig client.TxConfig
	encode   func(client.TxConfig, ...sdk.Msg) ([]byte, error)
	logger   log.Logger

	aether *relayer.Chain
	cparty *relayer.Chain

	mu        sync.Mutex
	batch     [][]byte
	batchTip  int64 // Aether's committed height when batch was computed
	lastError string
}

// New connects to both chains. encode builds a relay transaction
// (app.EncodeHelicaseTx; passed in so this package doesn't import app).
func New(cfg Config, cdc codec.Codec, txConfig client.TxConfig, encode func(client.TxConfig, ...sdk.Msg) ([]byte, error), logger log.Logger) (*Worker, error) {
	if cfg.CounterpartyRPC == "" || cfg.ClientID == "" {
		return nil, errors.New("helicase needs a counterparty RPC and a client ID")
	}
	if cfg.AetherRPC == "" {
		cfg.AetherRPC = "http://127.0.0.1:26657"
	}
	if cfg.Interval <= 0 {
		cfg.Interval = 2 * time.Second
	}
	aether, err := relayer.NewReadOnlyChain("aether", cfg.AetherRPC, "", cdc, txConfig)
	if err != nil {
		return nil, err
	}
	// The chain ID, which proofs' heights carry the revision of, comes
	// from the client once it can be read.
	cparty, err := relayer.NewReadOnlyChain("counterparty", cfg.CounterpartyRPC, "", cdc, txConfig)
	if err != nil {
		return nil, err
	}
	return &Worker{
		cfg: cfg, txConfig: txConfig, encode: encode,
		logger: logger.With("module", "helicase", "client", cfg.ClientID),
		aether: aether, cparty: cparty,
	}, nil
}

// RelayTxs returns the latest batch, unless it's too old to be worth
// proposing at height.
func (w *Worker) RelayTxs(height int64) [][]byte {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.batchTip < height-staleAfter {
		return nil
	}
	return w.batch
}

// Run cycles until ctx ends.
func (w *Worker) Run(ctx context.Context) {
	w.logger.Info("helicase started", "counterparty", w.cfg.CounterpartyRPC)
	ticker := time.NewTicker(w.cfg.Interval)
	defer ticker.Stop()
	for {
		w.runCycle()
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (w *Worker) runCycle() {
	tip, msgs, work, err := w.Cycle()
	if err != nil {
		w.mu.Lock()
		repeat := err.Error() == w.lastError
		w.lastError = err.Error()
		w.batch = nil
		w.mu.Unlock()
		if !repeat {
			w.logger.Error("helicase cycle failed", "err", err)
		}
		return
	}
	var batch [][]byte
	for _, msg := range msgs {
		bz, err := w.encode(w.txConfig, msg)
		if err != nil {
			w.logger.Error("helicase: encoding a relay transaction", "msg", sdk.MsgTypeURL(msg), "err", err)
			return
		}
		batch = append(batch, bz)
	}
	w.mu.Lock()
	w.batch, w.batchTip, w.lastError = batch, tip, ""
	w.mu.Unlock()
	if work.any() {
		w.logger.Info("helicase: relay transactions ready", "aether_tip", tip, "proof_height", work.proofHeight,
			"packets", work.packets, "acks", work.acks, "timeouts", work.timeouts)
	}
}

// Work counts what a cycle found.
type Work struct {
	proofHeight             clienttypes.Height
	update                  bool
	packets, acks, timeouts int
}

func (w Work) any() bool { return w.update || w.packets+w.acks+w.timeouts > 0 }

// Cycle computes the relay messages for Aether's next block: Aether's
// committed height it read, then the messages in execution order.
func (w *Worker) Cycle() (int64, []sdk.Msg, Work, error) {
	ctx := context.Background()
	var work Work

	aetherTip, err := w.aether.LatestHeight()
	if err != nil {
		return 0, nil, work, err
	}
	cs, err := w.clientState()
	if err != nil {
		return 0, nil, work, err
	}
	if !cs.FrozenHeight.IsZero() {
		return 0, nil, work, fmt.Errorf("client %s is frozen at %s", w.cfg.ClientID, cs.FrozenHeight)
	}
	w.cparty.ChainID = cs.ChainId
	w.cparty.ClientCtx = w.cparty.ClientCtx.WithChainID(cs.ChainId)

	channels, err := w.channels(ctx)
	if err != nil {
		return 0, nil, work, err
	}

	// Update the client to the other chain's tip and prove everything
	// there; if the client is already there, prove at its height.
	var update sdk.Msg
	proofHeight := cs.LatestHeight
	cpTip, err := w.cparty.LatestHeight()
	if err != nil {
		return 0, nil, work, err
	}
	if uint64(cpTip) > cs.LatestHeight.RevisionHeight {
		update, proofHeight, err = relayer.UpdateClientMsg(w.cparty, w.aether, w.cfg.ClientID)
		if err != nil {
			return 0, nil, work, err
		}
	}
	work.proofHeight = proofHeight
	ph := int64(proofHeight.RevisionHeight)
	proofTime, err := blockTime(w.cparty, ph)
	if err != nil {
		return 0, nil, work, err
	}
	aetherTime, err := blockTime(w.aether, aetherTip)
	if err != nil {
		return 0, nil, work, err
	}

	// State as of the proof height: a proof at ph proves the state the
	// block before it left.
	cpState := w.cparty.ClientCtx.WithHeight(ph - 1)
	cpProve := w.cparty.ClientCtx.WithHeight(ph)
	cpChannels := channeltypes.NewQueryClient(cpState)
	aetherChannels := channeltypes.NewQueryClient(w.aether.ClientCtx)

	var msgs []sdk.Msg
	full := func() bool { return len(msgs) >= maxMsgsPerCycle }

	for _, ch := range channels {
		cpPort, cpChannel := ch.Counterparty.PortId, ch.Counterparty.ChannelId

		// Packets in.
		sent, err := cpChannels.PacketCommitments(ctx, &channeltypes.QueryPacketCommitmentsRequest{
			PortId: cpPort, ChannelId: cpChannel, Pagination: &query.PageRequest{Limit: 1000},
		})
		if err != nil {
			return 0, nil, work, fmt.Errorf("counterparty: packet commitments on %s/%s: %w", cpPort, cpChannel, err)
		}
		if seqs := commitmentSeqs(sent.Commitments); len(seqs) > 0 {
			unrec, err := aetherChannels.UnreceivedPackets(ctx, &channeltypes.QueryUnreceivedPacketsRequest{
				PortId: ch.PortId, ChannelId: ch.ChannelId, PacketCommitmentSequences: seqs,
			})
			if err != nil {
				return 0, nil, work, fmt.Errorf("aether: unreceived packets on %s/%s: %w", ch.PortId, ch.ChannelId, err)
			}
			for _, seq := range unrec.Sequences {
				if full() {
					break
				}
				packet, err := relayer.FindSentPacket(w.cparty, cpPort, cpChannel, seq)
				if err != nil {
					return 0, nil, work, err
				}
				// Aether would refuse a packet past its timeout; the other
				// chain times it out instead.
				if timedOut(packet, uint64(aetherTip), aetherTime) {
					continue
				}
				proof, err := channelutils.QueryPacketCommitment(cpProve, cpPort, cpChannel, seq, true)
				if err != nil {
					return 0, nil, work, fmt.Errorf("counterparty: proving packet %d: %w", seq, err)
				}
				msgs = append(msgs, channeltypes.NewMsgRecvPacket(packet, proof.Proof, proof.ProofHeight, ""))
				work.packets++
			}
		}

		// Acknowledgements and timeouts for packets Aether sent.
		ours, err := aetherChannels.PacketCommitments(ctx, &channeltypes.QueryPacketCommitmentsRequest{
			PortId: ch.PortId, ChannelId: ch.ChannelId, Pagination: &query.PageRequest{Limit: 1000},
		})
		if err != nil {
			return 0, nil, work, fmt.Errorf("aether: packet commitments on %s/%s: %w", ch.PortId, ch.ChannelId, err)
		}
		seqs := commitmentSeqs(ours.Commitments)
		if len(seqs) == 0 {
			continue
		}
		acks, err := cpChannels.PacketAcknowledgements(ctx, &channeltypes.QueryPacketAcknowledgementsRequest{
			PortId: cpPort, ChannelId: cpChannel, PacketCommitmentSequences: seqs,
		})
		if err != nil {
			return 0, nil, work, fmt.Errorf("counterparty: acknowledgements on %s/%s: %w", cpPort, cpChannel, err)
		}
		for _, a := range acks.Acknowledgements {
			if full() {
				break
			}
			packet, ack, err := relayer.FindAcknowledgement(w.cparty, cpPort, cpChannel, a.Sequence)
			if err != nil {
				return 0, nil, work, err
			}
			proof, err := channelutils.QueryPacketAcknowledgement(cpProve, cpPort, cpChannel, a.Sequence, true)
			if err != nil {
				return 0, nil, work, fmt.Errorf("counterparty: proving acknowledgement %d: %w", a.Sequence, err)
			}
			msgs = append(msgs, channeltypes.NewMsgAcknowledgement(packet, ack, proof.Proof, proof.ProofHeight, ""))
			work.acks++
		}

		unrec, err := cpChannels.UnreceivedPackets(ctx, &channeltypes.QueryUnreceivedPacketsRequest{
			PortId: cpPort, ChannelId: cpChannel, PacketCommitmentSequences: seqs,
		})
		if err != nil {
			return 0, nil, work, fmt.Errorf("counterparty: unreceived packets on %s/%s: %w", cpPort, cpChannel, err)
		}
		for _, seq := range unrec.Sequences {
			if full() {
				break
			}
			packet, err := relayer.FindSentPacket(w.aether, ch.PortId, ch.ChannelId, seq)
			if err != nil {
				return 0, nil, work, err
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
				res, err := channelutils.QueryNextSequenceReceive(cpProve, cpPort, cpChannel, true)
				if err != nil {
					return 0, nil, work, fmt.Errorf("counterparty: proving next receive sequence: %w", err)
				}
				proofBz, nextSeq, proven = res.Proof, res.NextSequenceReceive, res.ProofHeight
			} else {
				res, err := channelutils.QueryPacketReceipt(cpProve, cpPort, cpChannel, seq, true)
				if err != nil {
					return 0, nil, work, fmt.Errorf("counterparty: proving packet %d was never received: %w", seq, err)
				}
				proofBz, proven = res.Proof, res.ProofHeight
			}
			msgs = append(msgs, channeltypes.NewMsgTimeout(packet, nextSeq, proofBz, proven, ""))
			work.timeouts++
		}
	}

	if update != nil && (len(msgs) > 0 || w.needsRefresh(cs, proofTime)) {
		msgs = append([]sdk.Msg{update}, msgs...)
		work.update = true
	}
	return aetherTip, msgs, work, nil
}

// needsRefresh: with no packets to carry it, the client is still updated
// once its latest consensus state is a third of the way through its
// trusting period, so it never expires on a quiet channel.
func (w *Worker) needsRefresh(cs *ibctm.ClientState, now time.Time) bool {
	res, err := clientutils.QueryConsensusStateABCI(w.aether.ClientCtx, w.cfg.ClientID, cs.LatestHeight)
	if err != nil {
		return true
	}
	var cons ibcexported.ConsensusState
	if err := w.aether.ClientCtx.InterfaceRegistry.UnpackAny(res.ConsensusState, &cons); err != nil {
		return true
	}
	tmCons, ok := cons.(*ibctm.ConsensusState)
	if !ok {
		return true
	}
	return now.Sub(tmCons.Timestamp) > cs.TrustingPeriod/3
}

func (w *Worker) clientState() (*ibctm.ClientState, error) {
	res, err := clientutils.QueryClientState(w.aether.ClientCtx, w.cfg.ClientID, false)
	if err != nil {
		return nil, fmt.Errorf("aether: client %s: %w", w.cfg.ClientID, err)
	}
	var cs ibcexported.ClientState
	if err := w.aether.ClientCtx.InterfaceRegistry.UnpackAny(res.ClientState, &cs); err != nil {
		return nil, err
	}
	tmCS, ok := cs.(*ibctm.ClientState)
	if !ok {
		return nil, fmt.Errorf("aether: client %s is %T, not 07-tendermint", w.cfg.ClientID, cs)
	}
	return tmCS, nil
}

// channels is every open channel on a connection over the client.
func (w *Worker) channels(ctx context.Context) ([]*channeltypes.IdentifiedChannel, error) {
	conns, err := connectiontypes.NewQueryClient(w.aether.ClientCtx).ClientConnections(ctx,
		&connectiontypes.QueryClientConnectionsRequest{ClientId: w.cfg.ClientID})
	if err != nil {
		return nil, fmt.Errorf("aether: connections on client %s: %w", w.cfg.ClientID, err)
	}
	var out []*channeltypes.IdentifiedChannel
	for _, conn := range conns.ConnectionPaths {
		res, err := channeltypes.NewQueryClient(w.aether.ClientCtx).ConnectionChannels(ctx,
			&channeltypes.QueryConnectionChannelsRequest{Connection: conn, Pagination: &query.PageRequest{Limit: 1000}})
		if err != nil {
			return nil, fmt.Errorf("aether: channels on %s: %w", conn, err)
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

func blockTime(c *relayer.Chain, height int64) (time.Time, error) {
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
