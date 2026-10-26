package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"math/big"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	query "github.com/cosmos/cosmos-sdk/types/query"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	"github.com/cosmos/gogoproto/proto"
	transfertypes "github.com/cosmos/ibc-go/v8/modules/apps/transfer/types"
	"google.golang.org/grpc"
)

const (
	// clientExpiringWindow is how close a client's trusting period has to
	// be before the explorer calls it expiring. A day is enough time for
	// a relayer to submit a header, and short enough to notice.
	clientExpiringWindow = 24 * time.Hour

	ibcActivityTTL = 20 * time.Second
)

// clientAttention is "" for a client that can still be used, "expiring"
// inside the warning window, "expired" once the trusting period has
// ended (or the chain already says so), and "frozen" when the client is
// frozen. status is the chain's own word ("Active", "Expired", "Frozen").
func clientAttention(status string, expires, now time.Time) string {
	switch strings.ToLower(status) {
	case "expired":
		return "expired"
	case "frozen":
		return "frozen"
	}
	if expires.IsZero() {
		return ""
	}
	if !now.Before(expires) {
		return "expired"
	}
	if expires.Sub(now) <= clientExpiringWindow {
		return "expiring"
	}
	return ""
}

// pickBridgeChannel is the open transfer channel to chainID the helix
// draws. Several can be open to one chain (Osmosis was reopened as
// channel-3 while channel-1 stayed open); the one that has carried the
// most packets is the live path, and a tie goes to the higher channel
// number.
func pickBridgeChannel(channels []ibcChannelDTO, chainID string) (ibcChannelDTO, bool) {
	var best ibcChannelDTO
	found := false
	for _, ch := range channels {
		if ch.PortID != transfertypes.PortID || ch.State != "STATE_OPEN" || ch.CounterpartyChainID != chainID || chainID == "" {
			continue
		}
		if !found || ch.PacketsSent > best.PacketsSent || (ch.PacketsSent == best.PacketsSent && channelNumber(ch.ChannelID) > channelNumber(best.ChannelID)) {
			best = ch
			found = true
		}
	}
	return best, found
}

func channelNumber(id string) int {
	n, err := strconv.Atoi(strings.TrimPrefix(id, "channel-"))
	if err != nil {
		return -1
	}
	return n
}

type ibcDenomDTO struct {
	Denom    string `json:"denom"`
	Base     string `json:"baseDenom"`
	Path     string `json:"path"`
	Amount   string `json:"amount"` // total supply on Aether, base units
	Symbol   string `json:"symbol,omitempty"`
	Decimals int    `json:"decimals,omitempty"`
	Origin   string `json:"origin,omitempty"`
}

// denomHoldings is every IBC denom with a non-zero supply, joined to its
// trace when Aether has one. A trace with nothing held is left out: the
// page is tokens on Aether, not every path that was ever used.
func denomHoldings(traces []transfertypes.DenomTrace, supply map[string]string) []ibcDenomDTO {
	byDenom := map[string]ibcDenomDTO{}
	for _, tr := range traces {
		denom := tr.IBCDenom()
		amt := supply[denom]
		if !amountPositive(amt) {
			continue
		}
		byDenom[denom] = ibcDenomDTO{Denom: denom, Base: tr.BaseDenom, Path: tr.Path, Amount: amt}
	}
	for denom, amt := range supply {
		if _, ok := byDenom[denom]; ok || !strings.HasPrefix(denom, "ibc/") || !amountPositive(amt) {
			continue
		}
		byDenom[denom] = ibcDenomDTO{Denom: denom, Amount: amt}
	}
	out := make([]ibcDenomDTO, 0, len(byDenom))
	for _, row := range byDenom {
		if a, ok := assets.ByDenom(row.Denom); ok {
			row.Symbol = a.Symbol
			row.Decimals = a.Decimals
			row.Origin = a.Origin
		}
		out = append(out, row)
	}
	sort.Slice(out, func(i, j int) bool {
		c := amountCmp(out[i].Amount, out[j].Amount)
		if c != 0 {
			return c > 0
		}
		return out[i].Denom < out[j].Denom
	})
	return out
}

func amountPositive(s string) bool {
	n, ok := new(big.Int).SetString(s, 10)
	return ok && n.Sign() > 0
}

func amountCmp(a, b string) int {
	ai, oka := new(big.Int).SetString(a, 10)
	bi, okb := new(big.Int).SetString(b, 10)
	switch {
	case !oka && !okb:
		return 0
	case !oka:
		return -1
	case !okb:
		return 1
	default:
		return ai.Cmp(bi)
	}
}

// readDenomHoldings reads ICS-20 traces and the bank supply and keeps
// the IBC denoms Aether actually holds.
func readDenomHoldings(ctx context.Context, conn *grpc.ClientConn) ([]ibcDenomDTO, error) {
	tq := transfertypes.NewQueryClient(conn)
	var traces []transfertypes.DenomTrace
	var pageKey []byte
	for pages := 0; pages < 10; pages++ {
		resp, err := tq.DenomTraces(ctx, &transfertypes.QueryDenomTracesRequest{
			Pagination: &query.PageRequest{Key: pageKey, Limit: 200},
		})
		if err != nil {
			return nil, err
		}
		traces = append(traces, resp.DenomTraces...)
		if resp.Pagination == nil || len(resp.Pagination.NextKey) == 0 {
			break
		}
		pageKey = resp.Pagination.NextKey
	}

	bq := banktypes.NewQueryClient(conn)
	supply := map[string]string{}
	pageKey = nil
	for pages := 0; pages < 20; pages++ {
		resp, err := bq.TotalSupply(ctx, &banktypes.QueryTotalSupplyRequest{
			Pagination: &query.PageRequest{Key: pageKey, Limit: 500},
		})
		if err != nil {
			return nil, err
		}
		for _, c := range resp.Supply {
			if strings.HasPrefix(c.Denom, "ibc/") && !c.Amount.IsNil() && c.Amount.IsPositive() {
				supply[c.Denom] = c.Amount.String()
			}
		}
		if resp.Pagination == nil || len(resp.Pagination.NextKey) == 0 {
			break
		}
		pageKey = resp.Pagination.NextKey
	}
	rows := denomHoldings(traces, supply)
	if rows == nil {
		rows = []ibcDenomDTO{}
	}
	return rows, nil
}

type relayHit struct {
	address, action, channel string
	height                   int64
	when                     time.Time
	hash                     string
}

type ibcRelayerDTO struct {
	Address    string   `json:"address"`
	Packets    int      `json:"packets"`
	LastHeight int64    `json:"lastHeight"`
	LastTime   string   `json:"lastTime,omitempty"`
	LastAction string   `json:"lastAction"`
	Channels   []string `json:"channels"`
}

type ibcTransferDTO struct {
	Hash      string `json:"hash"`
	Height    int64  `json:"height"`
	Time      string `json:"time,omitempty"`
	Direction string `json:"direction"` // "out" of Aether, or "in"
	Sender    string `json:"sender"`
	Receiver  string `json:"receiver"`
	Amount    string `json:"amount"`
	Denom     string `json:"denom"`
	Channel   string `json:"channel"` // Aether's channel
	Sequence  uint64 `json:"sequence,omitempty"`
	Status    string `json:"status"` // "sent", "received" or "failed"
}

// summarizeRelayers folds packet-relay hits into one row per signer.
// The signer of MsgRecvPacket, MsgAcknowledgement and MsgTimeout is the
// relayer; the account that sent the transfer is not.
func summarizeRelayers(hits []relayHit) []ibcRelayerDTO {
	type acc struct {
		dto  ibcRelayerDTO
		seen map[string]bool
	}
	by := map[string]*acc{}
	var order []string
	for _, h := range hits {
		if h.address == "" {
			continue
		}
		a := by[h.address]
		if a == nil {
			a = &acc{dto: ibcRelayerDTO{Address: h.address, Channels: []string{}}, seen: map[string]bool{}}
			by[h.address] = a
			order = append(order, h.address)
		}
		a.dto.Packets++
		if h.height > a.dto.LastHeight {
			a.dto.LastHeight = h.height
			a.dto.LastAction = h.action
			if !h.when.IsZero() {
				a.dto.LastTime = h.when.UTC().Format(time.RFC3339Nano)
			}
		}
		if h.channel != "" && !a.seen[h.channel] {
			a.seen[h.channel] = true
			a.dto.Channels = append(a.dto.Channels, h.channel)
		}
	}
	out := make([]ibcRelayerDTO, 0, len(order))
	for _, addr := range order {
		sort.Strings(by[addr].dto.Channels)
		out = append(out, by[addr].dto)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Packets != out[j].Packets {
			return out[i].Packets > out[j].Packets
		}
		return out[i].Address < out[j].Address
	})
	return out
}

var relayMsgTypes = []string{
	"/ibc.core.channel.v1.MsgRecvPacket",
	"/ibc.core.channel.v1.MsgAcknowledgement",
	"/ibc.core.channel.v1.MsgTimeout",
	"/ibc.core.channel.v1.MsgTimeoutOnClose",
}

const transferMsgType = "/ibc.applications.transfer.v1.MsgTransfer"

func relaysOf(tx searchedTx) []relayHit {
	sender := firstAttr(tx.events, "message", "sender")
	if sender == "" || tx.code != 0 {
		return nil
	}
	var out []relayHit
	for _, e := range tx.events {
		switch e.Type {
		case "recv_packet", "acknowledge_packet", "timeout_packet", "timeout_on_close_packet":
		default:
			continue
		}
		a := attrMap(e)
		ch := a["packet_src_channel"]
		if e.Type == "recv_packet" {
			ch = a["packet_dst_channel"]
		}
		out = append(out, relayHit{
			address: sender, action: e.Type, channel: ch,
			height: tx.height, when: tx.when, hash: tx.hash,
		})
	}
	return out
}

func outboundTransfers(tx searchedTx) []ibcTransferDTO {
	d := decodeTx(tx.raw)
	if !d.ok {
		return nil
	}
	seq, _ := strconv.ParseUint(firstAttr(tx.events, "send_packet", "packet_sequence"), 10, 64)
	var out []ibcTransferDTO
	for _, m := range d.body.Messages {
		if m.TypeUrl != transferMsgType {
			continue
		}
		var msg transfertypes.MsgTransfer
		if proto.Unmarshal(m.Value, &msg) != nil || msg.Token.Amount.IsNil() {
			continue
		}
		status := "sent"
		if tx.code != 0 {
			status = "failed"
		}
		out = append(out, ibcTransferDTO{
			Hash: tx.hash, Height: tx.height, Time: formatTime(tx.when), Direction: "out",
			Sender: msg.Sender, Receiver: msg.Receiver,
			Amount: msg.Token.Amount.String(), Denom: msg.Token.Denom,
			Channel: msg.SourceChannel, Sequence: seq, Status: status,
		})
	}
	return out
}

func inboundTransfers(tx searchedTx) []ibcTransferDTO {
	if tx.code != 0 {
		return nil
	}
	var out []ibcTransferDTO
	for _, e := range tx.events {
		if e.Type != "recv_packet" {
			continue
		}
		a := attrMap(e)
		if a["packet_dst_port"] != transfertypes.PortID {
			continue
		}
		raw := a["packet_data"]
		if raw == "" && a["packet_data_hex"] != "" {
			if b, err := hex.DecodeString(a["packet_data_hex"]); err == nil {
				raw = string(b)
			}
		}
		var data transfertypes.FungibleTokenPacketData
		if json.Unmarshal([]byte(raw), &data) != nil || data.Amount == "" {
			continue
		}
		seq, _ := strconv.ParseUint(a["packet_sequence"], 10, 64)
		out = append(out, ibcTransferDTO{
			Hash: tx.hash, Height: tx.height, Time: formatTime(tx.when), Direction: "in",
			Sender: data.Sender, Receiver: data.Receiver,
			Amount: data.Amount, Denom: data.Denom,
			Channel: a["packet_dst_channel"], Sequence: seq, Status: "received",
		})
	}
	return out
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func sortTransfers(in []ibcTransferDTO) []ibcTransferDTO {
	sort.Slice(in, func(i, j int) bool {
		if in[i].Height != in[j].Height {
			return in[i].Height > in[j].Height
		}
		return in[i].Sequence > in[j].Sequence
	})
	if len(in) > 25 {
		in = in[:25]
	}
	return in
}

var (
	ibcActivityMu    sync.Mutex
	ibcActivityCache struct {
		at        time.Time
		relayers  []ibcRelayerDTO
		transfers []ibcTransferDTO
		note      string
	}
)

// cachedIBCActivity is the relayer and transfer scan. Tx search is the
// expensive part, and a relayer's last few packets don't need to be
// newer than 20 seconds, so a burst of page loads shares one scan.
// Client status lives in the ten-second IBC state cache instead.
func cachedIBCActivity(ctx context.Context) ([]ibcRelayerDTO, []ibcTransferDTO, string) {
	ibcActivityMu.Lock()
	if !ibcActivityCache.at.IsZero() && time.Since(ibcActivityCache.at) < ibcActivityTTL {
		a := ibcActivityCache
		ibcActivityMu.Unlock()
		return a.relayers, a.transfers, a.note
	}
	ibcActivityMu.Unlock()

	relayers, transfers, note := readIBCActivity(ctx)
	if ctx.Err() != nil {
		return relayers, transfers, note
	}
	ibcActivityMu.Lock()
	ibcActivityCache.at = time.Now()
	ibcActivityCache.relayers = relayers
	ibcActivityCache.transfers = transfers
	ibcActivityCache.note = note
	ibcActivityMu.Unlock()
	return relayers, transfers, note
}

func readIBCActivity(ctx context.Context) ([]ibcRelayerDTO, []ibcTransferDTO, string) {
	emptyR, emptyT := []ibcRelayerDTO{}, []ibcTransferDTO{}
	rpc, err := newRPC()
	if err != nil {
		return emptyR, emptyT, "Relayer activity couldn't be read: " + clipErr(err, 160)
	}
	var notes []string
	var hits []relayHit
	var transfers []ibcTransferDTO
	for _, typeURL := range relayMsgTypes {
		txs, err := recentActionTxs(ctx, rpc, typeURL, 20)
		if err != nil {
			notes = append(notes, clipErr(err, 120))
			continue
		}
		for _, tx := range txs {
			hits = append(hits, relaysOf(tx)...)
			if typeURL == "/ibc.core.channel.v1.MsgRecvPacket" {
				transfers = append(transfers, inboundTransfers(tx)...)
			}
		}
	}
	txs, err := recentActionTxs(ctx, rpc, transferMsgType, 25)
	if err != nil {
		notes = append(notes, clipErr(err, 120))
	} else {
		for _, tx := range txs {
			transfers = append(transfers, outboundTransfers(tx)...)
		}
	}
	relayers := summarizeRelayers(hits)
	if relayers == nil {
		relayers = emptyR
	}
	transfers = sortTransfers(transfers)
	if transfers == nil {
		transfers = emptyT
	}
	note := ""
	if len(notes) > 0 {
		note = "Some relayer or transfer history couldn't be read: " + strings.Join(notes, "; ")
	}
	return relayers, transfers, note
}
