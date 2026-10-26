// cmd/explorer/ibc.go
//
// GET /api/ibc: every IBC client, connection and channel this chain
// currently holds, plus relayer activity, recent transfers and the IBC
// denoms Aether holds. Client status and trusting-period expiry are
// read live. The relayer and transfer scan is a tx search, cached for
// a few seconds so a refreshing page doesn't repeat it.
package main

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	transfertypes "github.com/cosmos/ibc-go/v8/modules/apps/transfer/types"
	clienttypes "github.com/cosmos/ibc-go/v8/modules/core/02-client/types"
	connectiontypes "github.com/cosmos/ibc-go/v8/modules/core/03-connection/types"
	channeltypes "github.com/cosmos/ibc-go/v8/modules/core/04-channel/types"
	ibcexported "github.com/cosmos/ibc-go/v8/modules/core/exported"
	ibctm "github.com/cosmos/ibc-go/v8/modules/light-clients/07-tendermint"
	"google.golang.org/grpc"
)

type ibcCoinDTO struct {
	Denom  string `json:"denom"`
	Amount string `json:"amount"`
}

type ibcClientDTO struct {
	ClientID            string `json:"clientId"`
	ChainID             string `json:"chainId"`
	Status              string `json:"status"`              // Active, Expired, Frozen, Unknown
	Attention           string `json:"attention,omitempty"` // "", "expiring", "expired", "frozen"
	TrustingPeriodSecs  int64  `json:"trustingPeriodSecs"`
	UnbondingPeriodSecs int64  `json:"unbondingPeriodSecs"`
	LatestRevision      uint64 `json:"latestRevision"`
	LatestHeight        uint64 `json:"latestHeight"`
	ConsensusTime       string `json:"consensusTime,omitempty"`
	ExpiresAt           string `json:"expiresAt,omitempty"` // consensus time plus the trusting period
}

type ibcConnectionDTO struct {
	ID                     string `json:"id"`
	State                  string `json:"state"`
	ClientID               string `json:"clientId"`
	CounterpartyClientID   string `json:"counterpartyClientId"`
	CounterpartyConnection string `json:"counterpartyConnectionId"`
	DelayPeriodNs          uint64 `json:"delayPeriodNs"`
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
	ClientStatus        string       `json:"clientStatus,omitempty"`
	ClientAttention     string       `json:"clientAttention,omitempty"`
	ClientExpiresAt     string       `json:"clientExpiresAt,omitempty"`
	PacketsSent         uint64       `json:"packetsSent"`
	PendingPackets      int          `json:"pendingPackets"` // committed, not yet acknowledged or timed out
	EscrowAddress       string       `json:"escrowAddress"`
	EscrowBalances      []ibcCoinDTO `json:"escrowBalances"`
}

type ibcSummaryDTO struct {
	Clients          int                `json:"clients"`
	Connections      int                `json:"connections"`
	Channels         []ibcChannelDTO    `json:"channels"`
	ClientStates     []ibcClientDTO     `json:"clientStates"`
	ConnectionStates []ibcConnectionDTO `json:"connectionStates"`
	Relayers         []ibcRelayerDTO    `json:"relayers"`
	Transfers        []ibcTransferDTO   `json:"transfers"`
	Denoms           []ibcDenomDTO      `json:"denoms"`
	ActivityNote     string             `json:"activityNote,omitempty"`
}

// --- GET /api/ibc ---
func handleIBC(w http.ResponseWriter, r *http.Request) {
	conn, err := newGRPC()
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	defer conn.Close()
	summary, err := cachedReadIBC(r.Context(), conn)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	relayers, transfers, note := cachedIBCActivity(r.Context())
	summary.Relayers = relayers
	summary.Transfers = transfers
	if note != "" {
		if summary.ActivityNote != "" {
			summary.ActivityNote += " " + note
		} else {
			summary.ActivityNote = note
		}
	}
	if summary.Relayers == nil {
		summary.Relayers = []ibcRelayerDTO{}
	}
	if summary.Transfers == nil {
		summary.Transfers = []ibcTransferDTO{}
	}
	writeJSON(w, http.StatusOK, summary)
}

const ibcStateTTL = 10 * time.Second

var (
	ibcStateMu sync.Mutex
	ibcStateAt time.Time
	ibcState   ibcSummaryDTO
)

// cachedReadIBC shares one read between the IBC page and the helix,
// which polls every few seconds. Expiry moves on a scale of hours, so
// ten seconds is still live, and the seed isn't asked twice for the
// same clients.
func cachedReadIBC(ctx context.Context, conn *grpc.ClientConn) (ibcSummaryDTO, error) {
	ibcStateMu.Lock()
	if !ibcStateAt.IsZero() && time.Since(ibcStateAt) < ibcStateTTL {
		s := ibcState
		ibcStateMu.Unlock()
		return s, nil
	}
	ibcStateMu.Unlock()
	s, err := readIBC(ctx, conn)
	if err != nil {
		return s, err
	}
	ibcStateMu.Lock()
	ibcState, ibcStateAt = s, time.Now()
	ibcStateMu.Unlock()
	return s, nil
}

// readIBC reads every IBC client, connection and channel this chain holds,
// and the IBC denoms it currently holds.
func readIBC(ctx context.Context, conn *grpc.ClientConn) (ibcSummaryDTO, error) {
	clientQ := clienttypes.NewQueryClient(conn)
	connQ := connectiontypes.NewQueryClient(conn)
	chanQ := channeltypes.NewQueryClient(conn)

	clientsResp, err := clientQ.ClientStates(ctx, &clienttypes.QueryClientStatesRequest{})
	if err != nil {
		return ibcSummaryDTO{}, fmt.Errorf("querying ibc clients: %w", err)
	}
	connsResp, err := connQ.Connections(ctx, &connectiontypes.QueryConnectionsRequest{})
	if err != nil {
		return ibcSummaryDTO{}, fmt.Errorf("querying ibc connections: %w", err)
	}
	chansResp, err := chanQ.Channels(ctx, &channeltypes.QueryChannelsRequest{})
	if err != nil {
		return ibcSummaryDTO{}, fmt.Errorf("querying ibc channels: %w", err)
	}

	now := time.Now().UTC()
	clients := map[string]ibcClientDTO{}
	clientStates := make([]ibcClientDTO, 0, len(clientsResp.ClientStates))
	for _, ic := range clientsResp.ClientStates {
		if ic.ClientId == ibcexported.LocalhostClientID {
			continue
		}
		dto := readIBCClient(ctx, clientQ, ic.ClientId, ic.ClientState, now)
		clients[ic.ClientId] = dto
		clientStates = append(clientStates, dto)
	}

	connStates := make([]ibcConnectionDTO, 0, len(connsResp.Connections))
	connClientID := map[string]string{}
	for _, ic := range connsResp.Connections {
		if ic.Id == ibcexported.LocalhostConnectionID {
			continue
		}
		connClientID[ic.Id] = ic.ClientId
		connStates = append(connStates, ibcConnectionDTO{
			ID:                     ic.Id,
			State:                  ic.State.String(),
			ClientID:               ic.ClientId,
			CounterpartyClientID:   ic.Counterparty.ClientId,
			CounterpartyConnection: ic.Counterparty.ConnectionId,
			DelayPeriodNs:          ic.DelayPeriod,
		})
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
					dto.CounterpartyChainID = info.ChainID
					dto.TrustingPeriodSecs = info.TrustingPeriodSecs
					dto.UnbondingPeriodSecs = info.UnbondingPeriodSecs
					dto.ClientStatus = info.Status
					dto.ClientAttention = info.Attention
					dto.ClientExpiresAt = info.ExpiresAt
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

	summary := ibcSummaryDTO{
		Clients:          len(clientStates),
		Connections:      len(connStates),
		Channels:         channels,
		ClientStates:     clientStates,
		ConnectionStates: connStates,
		Relayers:         []ibcRelayerDTO{},
		Transfers:        []ibcTransferDTO{},
		Denoms:           []ibcDenomDTO{},
	}
	denoms, err := readDenomHoldings(ctx, conn)
	if err != nil {
		summary.ActivityNote = "Denom traces couldn't be read: " + clipErr(err, 160)
	} else {
		summary.Denoms = denoms
	}
	return summary, nil
}

// readIBCClient is one light client: the chain's status, and when its
// trusting period ends if the latest consensus state is still stored.
func readIBCClient(ctx context.Context, q clienttypes.QueryClient, id string, stateAny *codectypes.Any, now time.Time) ibcClientDTO {
	dto := ibcClientDTO{ClientID: id, Status: "Unknown"}
	var expires time.Time
	var cs ibcexported.ClientState
	if stateAny != nil && encoding().InterfaceRegistry.UnpackAny(stateAny, &cs) == nil {
		if tm, ok := cs.(*ibctm.ClientState); ok {
			dto.ChainID = tm.ChainId
			dto.TrustingPeriodSecs = int64(tm.TrustingPeriod.Seconds())
			dto.UnbondingPeriodSecs = int64(tm.UnbondingPeriod.Seconds())
			dto.LatestRevision = tm.LatestHeight.GetRevisionNumber()
			dto.LatestHeight = tm.LatestHeight.GetRevisionHeight()
			if !tm.FrozenHeight.IsZero() {
				dto.Status = "Frozen"
			}
			if ts, ok := clientConsensusTime(ctx, q, id, tm.LatestHeight); ok {
				dto.ConsensusTime = ts.UTC().Format(time.RFC3339Nano)
				if tm.TrustingPeriod > 0 {
					expires = ts.Add(tm.TrustingPeriod)
					dto.ExpiresAt = expires.UTC().Format(time.RFC3339Nano)
				}
			}
		}
	}
	if resp, err := q.ClientStatus(ctx, &clienttypes.QueryClientStatusRequest{ClientId: id}); err == nil && resp != nil && resp.Status != "" {
		dto.Status = resp.Status
	}
	dto.Attention = clientAttention(dto.Status, expires, now)
	return dto
}

func clientConsensusTime(ctx context.Context, q clienttypes.QueryClient, id string, height clienttypes.Height) (time.Time, bool) {
	if height.IsZero() {
		return time.Time{}, false
	}
	resp, err := q.ConsensusState(ctx, &clienttypes.QueryConsensusStateRequest{
		ClientId:       id,
		RevisionNumber: height.GetRevisionNumber(),
		RevisionHeight: height.GetRevisionHeight(),
	})
	if err != nil || resp == nil || resp.ConsensusState == nil {
		return time.Time{}, false
	}
	var cs ibcexported.ConsensusState
	if encoding().InterfaceRegistry.UnpackAny(resp.ConsensusState, &cs) != nil {
		return time.Time{}, false
	}
	tm, ok := cs.(*ibctm.ConsensusState)
	if !ok || tm.Timestamp.IsZero() {
		return time.Time{}, false
	}
	return tm.Timestamp, true
}
