// cmd/explorer/ibc.go
//
// GET /api/ibc: every IBC client, connection and channel this chain
// currently holds, read live from the node on each request -- same
// discipline as the rest of this API, nothing cached or estimated.
// Channel-centric, since a connection with no channel isn't useful to
// show yet and every real client/connection this chain has opened so
// far exists to carry exactly one ICS-20 transfer channel (see
// relayer/ and docs/IBC.md).
package main

import (
	"fmt"
	"net/http"
	"time"

	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	transfertypes "github.com/cosmos/ibc-go/v8/modules/apps/transfer/types"
	clienttypes "github.com/cosmos/ibc-go/v8/modules/core/02-client/types"
	connectiontypes "github.com/cosmos/ibc-go/v8/modules/core/03-connection/types"
	channeltypes "github.com/cosmos/ibc-go/v8/modules/core/04-channel/types"
	ibcexported "github.com/cosmos/ibc-go/v8/modules/core/exported"
	ibctm "github.com/cosmos/ibc-go/v8/modules/light-clients/07-tendermint"
)

type ibcCoinDTO struct {
	Denom  string `json:"denom"`
	Amount string `json:"amount"`
}

type ibcChannelDTO struct {
	PortID              string       `json:"portId"`
	ChannelID           string       `json:"channelId"`
	State               string       `json:"state"` // e.g. "STATE_OPEN"
	Ordering            string       `json:"ordering"`
	Version             string       `json:"version"`
	ConnectionID        string       `json:"connectionId"`
	ClientID            string       `json:"clientId"`
	CounterpartyPortID  string       `json:"counterpartyPortId"`
	CounterpartyChannel string       `json:"counterpartyChannelId"`
	CounterpartyChainID string       `json:"counterpartyChainId"` // "" if the client state couldn't be read
	TrustingPeriodSecs  int64        `json:"trustingPeriodSecs"`
	UnbondingPeriodSecs int64        `json:"unbondingPeriodSecs"`
	PacketsSent         uint64       `json:"packetsSent"`
	PendingPackets      int          `json:"pendingPackets"` // committed, not yet acknowledged or timed out
	EscrowAddress       string       `json:"escrowAddress"`
	EscrowBalances      []ibcCoinDTO `json:"escrowBalances"`
}

type ibcSummaryDTO struct {
	Clients     int             `json:"clients"`
	Connections int             `json:"connections"`
	Channels    []ibcChannelDTO `json:"channels"`
}

// --- GET /api/ibc ---
func handleIBC(w http.ResponseWriter, r *http.Request) {
	conn, err := newGRPC()
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	defer conn.Close()
	ctx := r.Context()

	clientQ := clienttypes.NewQueryClient(conn)
	connQ := connectiontypes.NewQueryClient(conn)
	chanQ := channeltypes.NewQueryClient(conn)

	clientsResp, err := clientQ.ClientStates(ctx, &clienttypes.QueryClientStatesRequest{})
	if err != nil {
		writeError(w, http.StatusBadGateway, fmt.Errorf("querying ibc clients: %w", err))
		return
	}
	connsResp, err := connQ.Connections(ctx, &connectiontypes.QueryConnectionsRequest{})
	if err != nil {
		writeError(w, http.StatusBadGateway, fmt.Errorf("querying ibc connections: %w", err))
		return
	}
	chansResp, err := chanQ.Channels(ctx, &channeltypes.QueryChannelsRequest{})
	if err != nil {
		writeError(w, http.StatusBadGateway, fmt.Errorf("querying ibc channels: %w", err))
		return
	}

	// client_id -> its counterparty chain-id and periods, unpacked from
	// the Any the same way relayer.AetherUnbondingPeriod's neighbors do.
	type clientInfo struct {
		chainID   string
		trusting  time.Duration
		unbonding time.Duration
	}
	// ibc-go registers a "09-localhost" client and "connection-localhost"
	// connection on every chain for same-chain IBC, present whether or
	// not this chain has ever talked to another one -- excluded below so
	// "clients"/"connections" count real counterparties, not bookkeeping.
	clients := map[string]clientInfo{}
	realClients := 0
	for _, ic := range clientsResp.ClientStates {
		if ic.ClientId == ibcexported.LocalhostClientID {
			continue
		}
		realClients++
		var cs ibcexported.ClientState
		if err := encoding().InterfaceRegistry.UnpackAny(ic.ClientState, &cs); err != nil {
			continue
		}
		if tm, ok := cs.(*ibctm.ClientState); ok {
			clients[ic.ClientId] = clientInfo{chainID: tm.ChainId, trusting: tm.TrustingPeriod, unbonding: tm.UnbondingPeriod}
		}
	}

	// connection_id -> its client_id, for resolving a channel's connection_hops[0].
	connClientID := map[string]string{}
	realConnections := 0
	for _, ic := range connsResp.Connections {
		if ic.Id == ibcexported.LocalhostConnectionID {
			continue
		}
		realConnections++
		connClientID[ic.Id] = ic.ClientId
	}

	channels := make([]ibcChannelDTO, 0, len(chansResp.Channels))
	for _, ch := range chansResp.Channels {
		dto := ibcChannelDTO{
			PortID:              ch.PortId,
			ChannelID:           ch.ChannelId,
			State:               ch.State.String(),
			Ordering:            ch.Ordering.String(),
			Version:             ch.Version,
			CounterpartyPortID:  ch.Counterparty.PortId,
			CounterpartyChannel: ch.Counterparty.ChannelId,
		}
		if len(ch.ConnectionHops) > 0 {
			dto.ConnectionID = ch.ConnectionHops[0]
			if cid, ok := connClientID[dto.ConnectionID]; ok {
				dto.ClientID = cid
				if info, ok := clients[cid]; ok {
					dto.CounterpartyChainID = info.chainID
					dto.TrustingPeriodSecs = int64(info.trusting.Seconds())
					dto.UnbondingPeriodSecs = int64(info.unbonding.Seconds())
				}
			}
		}

		if seq, err := chanQ.NextSequenceSend(ctx, &channeltypes.QueryNextSequenceSendRequest{PortId: ch.PortId, ChannelId: ch.ChannelId}); err == nil && seq.NextSequenceSend > 0 {
			dto.PacketsSent = seq.NextSequenceSend - 1
		}
		if commits, err := chanQ.PacketCommitments(ctx, &channeltypes.QueryPacketCommitmentsRequest{PortId: ch.PortId, ChannelId: ch.ChannelId}); err == nil {
			dto.PendingPackets = len(commits.Commitments)
		}

		if ch.PortId == transfertypes.PortID {
			escrow := transfertypes.GetEscrowAddress(ch.PortId, ch.ChannelId)
			dto.EscrowAddress = escrow.String()
			dto.EscrowBalances = []ibcCoinDTO{}
			if bal, err := banktypes.NewQueryClient(conn).AllBalances(ctx, &banktypes.QueryAllBalancesRequest{Address: escrow.String()}); err == nil {
				for _, c := range bal.Balances {
					dto.EscrowBalances = append(dto.EscrowBalances, ibcCoinDTO{Denom: c.Denom, Amount: c.Amount.String()})
				}
			}
		}

		channels = append(channels, dto)
	}

	writeJSON(w, http.StatusOK, ibcSummaryDTO{
		Clients:     realClients,
		Connections: realConnections,
		Channels:    channels,
	})
}
