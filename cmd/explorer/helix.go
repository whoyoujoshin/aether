// cmd/explorer/helix.go
//
// GET /api/helix: the two strands the explorer draws as a helix, Aether
// and an IBC chain it's connected to (--ibc-rpc), with the packets
// relayed between them as the rungs. With several chains in --ibc-rpc,
// ?peer= picks which one is the second strand. Everything is read from
// the chains' own CometBFT RPCs on each request. Block results never
// change once a block is committed, so those alone are cached.
package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	abci "github.com/cometbft/cometbft/abci/types"
	cometrpchttp "github.com/cometbft/cometbft/rpc/client/http"
	ctypes "github.com/cometbft/cometbft/rpc/core/types"
	cmttypes "github.com/cometbft/cometbft/types"
	transfertypes "github.com/cosmos/ibc-go/v8/modules/apps/transfer/types"
)

var (
	// ibcRPCEndpoint is the CometBFT RPC of the chain on the other end of
	// Aether's IBC channel, or several, comma-separated. Empty: the helix
	// has one strand.
	ibcRPCEndpoint string
	// ibcName is what the explorer calls each of those chains, comma-
	// separated in the same order; an empty or missing one: its chain ID.
	ibcName string
)

// helixPeer is one chain the helix can draw opposite Aether.
type helixPeer struct{ endpoint, name string }

// helixPeers are the chains in --ibc-rpc, named by --ibc-name.
func helixPeers() []helixPeer {
	var names []string
	for _, n := range strings.Split(ibcName, ",") {
		names = append(names, strings.TrimSpace(n))
	}
	var peers []helixPeer
	for _, e := range strings.Split(ibcRPCEndpoint, ",") {
		if e = strings.TrimSpace(e); e == "" {
			continue
		}
		p := helixPeer{endpoint: e}
		if i := len(peers); i < len(names) {
			p.name = names[i]
		}
		peers = append(peers, p)
	}
	return peers
}

var (
	peerChainIDsMu sync.Mutex
	peerChainIDs   = map[string]string{} // endpoint -> chain ID, once seen
)

// peerChainID is the chain ID behind endpoint, asked once and remembered:
// a chain's ID doesn't change. Empty if the chain can't be reached.
func peerChainID(ctx context.Context, endpoint string) string {
	peerChainIDsMu.Lock()
	id, ok := peerChainIDs[endpoint]
	peerChainIDsMu.Unlock()
	if ok {
		return id
	}
	rpc, err := cometrpchttp.New(endpoint, "/websocket")
	if err != nil {
		return ""
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	st, err := rpc.Status(ctx)
	if err != nil {
		return ""
	}
	rememberPeerChainID(endpoint, st.NodeInfo.Network)
	return st.NodeInfo.Network
}

func rememberPeerChainID(endpoint, chainID string) {
	if chainID == "" {
		return
	}
	peerChainIDsMu.Lock()
	peerChainIDs[endpoint] = chainID
	peerChainIDsMu.Unlock()
}

// pickPeer is the peer named by want (a chain ID or display name, any
// case), or the first one when want is empty or matches none.
func pickPeer(peers []helixPeerDTO, want string) int {
	for i, p := range peers {
		if want != "" && (strings.EqualFold(p.ChainID, want) || strings.EqualFold(p.Name, want)) {
			return i
		}
	}
	return 0
}

const (
	helixDefaultSeconds = 96
	helixMaxSeconds     = 600
	helixMaxMinBlocks   = 50
	helixMaxBlocks      = 120 // per strand, whatever the window
)

type helixBlockDTO struct {
	Height   int64  `json:"height"`
	Hash     string `json:"hash"`
	Time     string `json:"time"`
	NumTxs   int    `json:"numTxs"`
	Proposer string `json:"proposer"` // Aether: the proposer's miner account; the IBC chain: its consensus address (hex)
	Packets  int    `json:"packets"`  // IBC packet events in this block: sends, receipts, acknowledgements, timeouts
}

type helixStrandDTO struct {
	ChainID       string          `json:"chainId"`
	Name          string          `json:"name"`
	Height        int64           `json:"height"`
	BlockTimeSecs float64         `json:"blockTimeSecs"` // mean over the blocks returned; 0 with fewer than two
	Validators    int             `json:"validators"`
	Blocks        []helixBlockDTO `json:"blocks"` // newest first
}

type packetStepDTO struct {
	Chain  string `json:"chain"` // "aether" or "ibc"
	Height int64  `json:"height"`
	Time   string `json:"time"`
	TxHash string `json:"txHash,omitempty"` // Aether steps only
}

type helixPacketDTO struct {
	Direction  string         `json:"direction"` // "out": Aether to the IBC chain; "in": the other way
	Sequence   uint64         `json:"sequence"`
	SrcPort    string         `json:"srcPort"`
	SrcChannel string         `json:"srcChannel"`
	DstPort    string         `json:"dstPort"`
	DstChannel string         `json:"dstChannel"`
	Denom      string         `json:"denom,omitempty"` // ICS-20 transfers only, as the packet names it
	Amount     string         `json:"amount,omitempty"`
	Sender     string         `json:"sender,omitempty"`
	Receiver   string         `json:"receiver,omitempty"`
	Sent       *packetStepDTO `json:"sent,omitempty"`
	Received   *packetStepDTO `json:"received,omitempty"`
	Acked      *packetStepDTO `json:"acked,omitempty"`
	TimedOut   *packetStepDTO `json:"timedOut,omitempty"`
	Status     string         `json:"status"` // "in-flight", "received", "acked" or "timed-out", as far as the window shows
}

type helixBridgeDTO struct {
	PortID                string  `json:"portId"`
	ChannelID             string  `json:"channelId"`
	CounterpartyChannelID string  `json:"counterpartyChannelId"`
	PacketsSent           uint64  `json:"packetsSent"`   // from Aether on this channel, ever
	InFlight              int     `json:"inFlight"`      // sent from Aether, not yet acknowledged or timed out
	EscrowedUaeth         string  `json:"escrowedUaeth"` // AETH locked on Aether for this channel, in uaeth
	AvgRelaySecs          float64 `json:"avgRelaySecs"`  // mean send-to-receipt among packets in the window; 0 if none
}

// helixPeerDTO is one chain the helix can draw opposite Aether.
type helixPeerDTO struct {
	ChainID string `json:"chainId"` // empty while that chain can't be reached
	Name    string `json:"name"`
}

type helixDTO struct {
	WindowSecs int               `json:"windowSecs"`
	Now        string            `json:"now"`
	Peers      []helixPeerDTO    `json:"peers"` // every chain in --ibc-rpc; ?peer= (a chain ID or name) picks the one drawn as ibc, the first by default
	Aether     helixStrandDTO    `json:"aether"`
	IBC        *helixStrandDTO   `json:"ibc"`     // null when the explorer has no --ibc-rpc
	Bridge     *helixBridgeDTO   `json:"bridge"`  // null without an ICS-20 channel to that chain
	Packets    []helixPacketDTO  `json:"packets"` // newest first
	Errors     map[string]string `json:"errors,omitempty"`
}

// packetEvent is one IBC packet event as a chain emitted it.
type packetEvent struct {
	chain                                    string
	kind                                     string // send_packet, recv_packet, acknowledge_packet, timeout_packet
	height                                   int64
	time                                     time.Time
	txHash                                   string
	seq                                      uint64
	srcPort, srcChannel, dstPort, dstChannel string
	data                                     []byte
}

var packetEventKinds = map[string]bool{
	"send_packet":             true,
	"recv_packet":             true,
	"acknowledge_packet":      true,
	"timeout_packet":          true,
	"timeout_on_close_packet": true,
}

func attrMap(e abci.Event) map[string]string {
	m := make(map[string]string, len(e.Attributes))
	for _, a := range e.Attributes {
		m[a.Key] = a.Value
	}
	return m
}

// packetEventsOf pulls the packet events out of one transaction's events.
func packetEventsOf(events []abci.Event) []packetEvent {
	var out []packetEvent
	for _, e := range events {
		if !packetEventKinds[e.Type] {
			continue
		}
		a := attrMap(e)
		seq, err := strconv.ParseUint(a["packet_sequence"], 10, 64)
		if err != nil {
			continue
		}
		pe := packetEvent{
			kind: e.Type, seq: seq,
			srcPort: a["packet_src_port"], srcChannel: a["packet_src_channel"],
			dstPort: a["packet_dst_port"], dstChannel: a["packet_dst_channel"],
		}
		if pe.kind == "timeout_on_close_packet" {
			pe.kind = "timeout_packet"
		}
		if d, ok := a["packet_data"]; ok {
			pe.data = []byte(d)
		} else if h, ok := a["packet_data_hex"]; ok {
			pe.data, _ = hex.DecodeString(h)
		}
		out = append(out, pe)
	}
	return out
}

// --- caches of immutable per-height results ---

var (
	helixCacheMu      sync.Mutex
	blockResultsCache = map[string]*ctypes.ResultBlockResults{}
	blockTxHashCache  = map[string][]string{}
)

const helixCacheMax = 4096

func cachedBlockResults(ctx context.Context, rpc *cometrpchttp.HTTP, endpoint string, h int64) (*ctypes.ResultBlockResults, error) {
	key := endpoint + "#" + strconv.FormatInt(h, 10)
	helixCacheMu.Lock()
	r, ok := blockResultsCache[key]
	helixCacheMu.Unlock()
	if ok {
		return r, nil
	}
	r, err := rpc.BlockResults(ctx, &h)
	if err != nil {
		return nil, err
	}
	helixCacheMu.Lock()
	if len(blockResultsCache) >= helixCacheMax {
		blockResultsCache = map[string]*ctypes.ResultBlockResults{}
	}
	blockResultsCache[key] = r
	helixCacheMu.Unlock()
	return r, nil
}

func cachedTxHashes(ctx context.Context, rpc *cometrpchttp.HTTP, endpoint string, h int64) ([]string, error) {
	key := endpoint + "#" + strconv.FormatInt(h, 10)
	helixCacheMu.Lock()
	hs, ok := blockTxHashCache[key]
	helixCacheMu.Unlock()
	if ok {
		return hs, nil
	}
	b, err := rpc.Block(ctx, &h)
	if err != nil {
		return nil, err
	}
	hs = make([]string, 0, len(b.Block.Txs))
	for _, tx := range b.Block.Txs {
		hs = append(hs, strings.ToUpper(hex.EncodeToString(tx.Hash())))
	}
	helixCacheMu.Lock()
	if len(blockTxHashCache) >= helixCacheMax {
		blockTxHashCache = map[string][]string{}
	}
	blockTxHashCache[key] = hs
	helixCacheMu.Unlock()
	return hs, nil
}

// readStrand reads one chain's blocks back to cutoff (and at least
// minBlocks of them), with the packet events in each.
func readStrand(ctx context.Context, endpoint, chain string, cutoff time.Time, minBlocks int, proposer func(hexAddr string) string) (helixStrandDTO, []packetEvent, error) {
	rpc, err := cometrpchttp.New(endpoint, "/websocket")
	if err != nil {
		return helixStrandDTO{}, nil, err
	}
	st, err := rpc.Status(ctx)
	if err != nil {
		return helixStrandDTO{}, nil, fmt.Errorf("status: %w", err)
	}
	strand := helixStrandDTO{ChainID: st.NodeInfo.Network, Name: st.NodeInfo.Network, Height: st.SyncInfo.LatestBlockHeight, Blocks: []helixBlockDTO{}}
	if chain == "ibc" {
		rememberPeerChainID(endpoint, strand.ChainID)
	}
	one := 1
	if vals, err := rpc.Validators(ctx, nil, &one, &one); err == nil {
		strand.Validators = vals.Total
	}

	var metas []*cmttypes.BlockMeta
	for max := strand.Height; max >= 1 && len(metas) < helixMaxBlocks; {
		min := max - 19
		if min < 1 {
			min = 1
		}
		info, err := rpc.BlockchainInfo(ctx, min, max)
		if err != nil {
			return strand, nil, fmt.Errorf("blocks %d-%d: %w", min, max, err)
		}
		done := false
		for _, m := range info.BlockMetas { // newest first
			if m.Header.Time.Before(cutoff) && len(metas) >= minBlocks {
				done = true
				break
			}
			metas = append(metas, m)
		}
		if done || min == 1 {
			break
		}
		max = min - 1
	}

	var events []packetEvent
	for _, m := range metas {
		b := helixBlockDTO{
			Height: m.Header.Height,
			Hash:   m.BlockID.Hash.String(),
			Time:   m.Header.Time.UTC().Format(time.RFC3339Nano),
			NumTxs: m.NumTxs,
		}
		if proposer != nil {
			b.Proposer = proposer(strings.ToUpper(hex.EncodeToString(m.Header.ProposerAddress)))
		} else {
			b.Proposer = strings.ToUpper(hex.EncodeToString(m.Header.ProposerAddress))
		}
		if m.NumTxs > 0 {
			res, err := cachedBlockResults(ctx, rpc, endpoint, b.Height)
			if err == nil {
				var hashes []string
				for i, tr := range res.TxsResults {
					if tr.Code != 0 {
						continue
					}
					found := packetEventsOf(tr.Events)
					if len(found) == 0 {
						continue
					}
					if chain == "aether" && hashes == nil {
						hashes, _ = cachedTxHashes(ctx, rpc, endpoint, b.Height)
					}
					for _, pe := range found {
						pe.chain, pe.height, pe.time = chain, b.Height, m.Header.Time
						if i < len(hashes) {
							pe.txHash = hashes[i]
						}
						events = append(events, pe)
						b.Packets++
					}
				}
			}
		}
		strand.Blocks = append(strand.Blocks, b)
	}
	if n := len(metas); n >= 2 {
		span := metas[0].Header.Time.Sub(metas[n-1].Header.Time).Seconds()
		if span > 0 {
			strand.BlockTimeSecs = span / float64(n-1)
		}
	}
	return strand, events, nil
}

// joinPackets folds both chains' packet events into one entry per packet,
// keyed by its direction, source port, channel and sequence: both ends of
// a channel often have the same ID (channel-0 on each side), so the
// source channel alone doesn't say which chain sent it. aetherChannel and
// ibcChannel, when set, keep only packets on that one channel pair.
func joinPackets(events []packetEvent, aetherChannel, ibcChannel string) []helixPacketDTO {
	type key struct {
		fromAether    bool
		port, channel string
		seq           uint64
	}
	byKey := map[key]*helixPacketDTO{}
	var order []key
	for _, e := range events {
		// Which way the packet goes: the chain that sends it, or the one
		// that doesn't receive it.
		fromAether := (e.chain == "aether") == (e.kind != "recv_packet")
		aetherSide, ibcSide := e.srcChannel, e.dstChannel
		if !fromAether {
			aetherSide, ibcSide = e.dstChannel, e.srcChannel
		}
		if (aetherChannel != "" && aetherSide != aetherChannel) || (ibcChannel != "" && ibcSide != ibcChannel) {
			continue
		}
		k := key{fromAether, e.srcPort, e.srcChannel, e.seq}
		p, ok := byKey[k]
		if !ok {
			p = &helixPacketDTO{Sequence: e.seq, SrcPort: e.srcPort, SrcChannel: e.srcChannel, DstPort: e.dstPort, DstChannel: e.dstChannel, Direction: "in"}
			if fromAether {
				p.Direction = "out"
			}
			byKey[k] = p
			order = append(order, k)
		}
		step := &packetStepDTO{Chain: e.chain, Height: e.height, Time: e.time.UTC().Format(time.RFC3339Nano), TxHash: e.txHash}
		switch e.kind {
		case "send_packet":
			p.Sent = step
		case "recv_packet":
			p.Received = step
		case "acknowledge_packet":
			p.Acked = step
		case "timeout_packet":
			p.TimedOut = step
		}
		if p.Denom == "" && len(e.data) > 0 && e.srcPort == transfertypes.PortID {
			var d transfertypes.FungibleTokenPacketData
			if json.Unmarshal(e.data, &d) == nil {
				p.Denom, p.Amount, p.Sender, p.Receiver = d.Denom, d.Amount, d.Sender, d.Receiver
			}
		}
	}
	out := make([]helixPacketDTO, 0, len(order))
	for _, k := range order {
		p := byKey[k]
		switch {
		case p.TimedOut != nil:
			p.Status = "timed-out"
		case p.Acked != nil:
			p.Status = "acked"
		case p.Received != nil:
			p.Status = "received"
		default:
			p.Status = "in-flight"
		}
		out = append(out, *p)
	}
	sort.SliceStable(out, func(i, j int) bool { return packetTime(out[i]) > packetTime(out[j]) })
	return out
}

// packetTime is when a packet first shows up: sent if seen, else received.
func packetTime(p helixPacketDTO) string {
	for _, s := range []*packetStepDTO{p.Sent, p.Received, p.Acked, p.TimedOut} {
		if s != nil {
			return s.Time
		}
	}
	return ""
}

// avgRelaySecs is the mean time from send to receipt among packets whose
// both ends fall in the window.
func avgRelaySecs(packets []helixPacketDTO) float64 {
	var total float64
	n := 0
	for _, p := range packets {
		if p.Sent == nil || p.Received == nil {
			continue
		}
		s, err1 := time.Parse(time.RFC3339Nano, p.Sent.Time)
		r, err2 := time.Parse(time.RFC3339Nano, p.Received.Time)
		if err1 != nil || err2 != nil || r.Before(s) {
			continue
		}
		total += r.Sub(s).Seconds()
		n++
	}
	if n == 0 {
		return 0
	}
	return total / float64(n)
}

// intParam is a query value as an int within lo..hi, or def if unset or
// not a number.
func intParam(raw string, def, lo, hi int) int {
	v, err := strconv.Atoi(raw)
	if err != nil {
		return def
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// --- GET /api/helix?seconds=&min=&peer= ---
func handleHelix(w http.ResponseWriter, r *http.Request) {
	seconds := intParam(r.URL.Query().Get("seconds"), helixDefaultSeconds, 10, helixMaxSeconds)
	minBlocks := intParam(r.URL.Query().Get("min"), 0, 0, helixMaxMinBlocks)
	ctx := r.Context()
	now := time.Now().UTC()
	cutoff := now.Add(-time.Duration(seconds) * time.Second)
	out := helixDTO{WindowSecs: seconds, Now: now.Format(time.RFC3339Nano), Peers: []helixPeerDTO{}, Packets: []helixPacketDTO{}}
	errs := map[string]string{}

	// The chains that can be the second strand, and the one that is.
	peers := helixPeers()
	out.Peers = make([]helixPeerDTO, len(peers))
	var pwg sync.WaitGroup
	for i, p := range peers {
		pwg.Add(1)
		go func() {
			defer pwg.Done()
			id := peerChainID(ctx, p.endpoint)
			name := p.name
			if name == "" {
				name = id
			}
			out.Peers[i] = helixPeerDTO{ChainID: id, Name: name}
		}()
	}
	pwg.Wait()
	var peer *helixPeer
	if len(peers) > 0 {
		peer = &peers[pickPeer(out.Peers, r.URL.Query().Get("peer"))]
	}
	// Aether's proposers by miner account, as the block page names them.
	var miners map[string]string
	if rpc, err := newRPC(); err == nil {
		miners, _ = consensusMiners(ctx, rpc)
	}
	aetherProposer := func(h string) string { a, _ := minerFor(miners, h); return a }

	var (
		wg                sync.WaitGroup
		aether, ibc       helixStrandDTO
		aetherEv, ibcEv   []packetEvent
		aetherErr, ibcErr error
	)
	wg.Add(1)
	go func() {
		defer wg.Done()
		aether, aetherEv, aetherErr = readStrand(ctx, rpcEndpoint, "aether", cutoff, minBlocks, aetherProposer)
	}()
	if peer != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ibc, ibcEv, ibcErr = readStrand(ctx, peer.endpoint, "ibc", cutoff, minBlocks, nil)
		}()
	}
	wg.Wait()
	if aetherErr != nil {
		writeError(w, http.StatusBadGateway, fmt.Errorf("reading Aether: %w", aetherErr))
		return
	}
	aether.Name = "Aether"
	out.Aether = aether
	if peer != nil {
		if ibcErr != nil {
			errs["ibc"] = ibcErr.Error()
		}
		if peer.name != "" {
			ibc.Name = peer.name
		}
		if ibc.Blocks == nil {
			ibc.Blocks = []helixBlockDTO{}
		}
		out.IBC = &ibc
	}

	// The transfer channel to that chain, from Aether's own IBC state.
	aetherChannel, ibcChannel := "", ""
	if out.IBC != nil && out.IBC.ChainID != "" {
		if conn, err := newGRPC(); err == nil {
			if summary, err := readIBC(ctx, conn); err == nil {
				for _, ch := range summary.Channels {
					if ch.PortID != transfertypes.PortID || ch.CounterpartyChainID != out.IBC.ChainID || ch.State != "STATE_OPEN" {
						continue
					}
					b := &helixBridgeDTO{PortID: ch.PortID, ChannelID: ch.ChannelID, CounterpartyChannelID: ch.CounterpartyChannel,
						PacketsSent: ch.PacketsSent, InFlight: ch.PendingPackets, EscrowedUaeth: "0"}
					for _, c := range ch.EscrowBalances {
						if c.Denom == "uaeth" {
							b.EscrowedUaeth = c.Amount
						}
					}
					out.Bridge = b
					aetherChannel, ibcChannel = ch.ChannelID, ch.CounterpartyChannel
					break
				}
			} else {
				errs["bridge"] = err.Error()
			}
			conn.Close()
		}
	}

	out.Packets = joinPackets(append(aetherEv, ibcEv...), aetherChannel, ibcChannel)
	if out.Bridge != nil {
		out.Bridge.AvgRelaySecs = avgRelaySecs(out.Packets)
	}
	if len(errs) > 0 {
		out.Errors = errs
	}
	writeJSON(w, http.StatusOK, out)
}
