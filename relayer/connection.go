package relayer

import (
	"context"
	"fmt"
	"time"

	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	cmttypes "github.com/cometbft/cometbft/types"

	sdk "github.com/cosmos/cosmos-sdk/types"

	clientutils "github.com/cosmos/ibc-go/v8/modules/core/02-client/client/utils"
	clienttypes "github.com/cosmos/ibc-go/v8/modules/core/02-client/types"
	connectionutils "github.com/cosmos/ibc-go/v8/modules/core/03-connection/client/utils"
	connectiontypes "github.com/cosmos/ibc-go/v8/modules/core/03-connection/types"
	commitmenttypes "github.com/cosmos/ibc-go/v8/modules/core/23-commitment/types"
	ibcexported "github.com/cosmos/ibc-go/v8/modules/core/exported"
	ibctm "github.com/cosmos/ibc-go/v8/modules/light-clients/07-tendermint"
)

// WaitForNextBlock blocks until c commits a block past its current tip.
// Every handshake step proves state written by the previous step's tx;
// a proof queried at height H reads state as of H-1 (see
// QueryTendermintProof), so the source must be at least one block past
// the block that included that tx.
func (c *Chain) WaitForNextBlock() error {
	start, err := c.LatestHeight()
	if err != nil {
		return err
	}
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		h, err := c.LatestHeight()
		if err == nil && h > start {
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("%s: no new block past %d within 30s", c.Name, start)
}

// UpdateClientMsg builds a MsgUpdateClient moving dst's client clientID
// (which tracks src) to src's current tip, and returns that tip as the
// height every proof for this step must be queried at.
func UpdateClientMsg(src, dst *Chain, clientID string) (sdk.Msg, clienttypes.Height, error) {
	csResp, err := clientutils.QueryClientState(dst.ClientCtx, clientID, false)
	if err != nil {
		return nil, clienttypes.Height{}, fmt.Errorf("%s: querying client %s: %w", dst.Name, clientID, err)
	}
	// client.Context sends straight over its GRPCClient when one is set,
	// which skips the SDK's usual Any unpacking -- do it explicitly.
	var cs ibcexported.ClientState
	if err := dst.ClientCtx.InterfaceRegistry.UnpackAny(csResp.ClientState, &cs); err != nil {
		return nil, clienttypes.Height{}, err
	}
	tmCS, ok := cs.(*ibctm.ClientState)
	if !ok {
		return nil, clienttypes.Height{}, fmt.Errorf("%s: client %s is %T, not 07-tendermint", dst.Name, clientID, cs)
	}
	trusted := tmCS.LatestHeight

	header, height, err := clientutils.QueryTendermintHeader(src.ClientCtx)
	if err != nil {
		return nil, clienttypes.Height{}, fmt.Errorf("%s: querying header: %w", src.Name, err)
	}
	if uint64(height) <= trusted.RevisionHeight {
		return nil, clienttypes.Height{}, fmt.Errorf("%s: tip %d not past %s's trusted height %s", src.Name, height, clientID, trusted)
	}

	// The trusted validators are the set that signed the block after the
	// trusted height: 07-tendermint checks them against the trusted
	// consensus state's NextValidatorsHash.
	trustedVals, err := validatorsAt(src, int64(trusted.RevisionHeight)+1)
	if err != nil {
		return nil, clienttypes.Height{}, err
	}
	header.TrustedHeight = trusted
	header.TrustedValidators = trustedVals

	msg, err := clienttypes.NewMsgUpdateClient(clientID, &header, dst.FromAddrStr)
	if err != nil {
		return nil, clienttypes.Height{}, err
	}
	return msg, clienttypes.NewHeight(clienttypes.ParseChainID(src.ChainID), uint64(height)), nil
}

func validatorsAt(c *Chain, height int64) (*cmtproto.ValidatorSet, error) {
	node, err := c.ClientCtx.GetNode()
	if err != nil {
		return nil, err
	}
	var vals []*cmttypes.Validator
	for page := 1; ; page++ {
		perPage := 100
		res, err := node.Validators(context.Background(), &height, &page, &perPage)
		if err != nil {
			return nil, fmt.Errorf("%s: validators at %d: %w", c.Name, height, err)
		}
		vals = append(vals, res.Validators...)
		if len(vals) >= res.Total {
			break
		}
	}
	return cmttypes.NewValidatorSet(vals).ToProto()
}

// counterpartyProofs is everything ConnOpenTry/ConnOpenAck need from the
// other chain, all proven at the same height.
type counterpartyProofs struct {
	connection      connectiontypes.ConnectionEnd
	connProof       []byte
	clientState     *ibctm.ClientState
	clientProof     []byte
	consensusProof  []byte
	consensusHeight clienttypes.Height
	proofHeight     clienttypes.Height
}

// queryHandshakeProofs proves, on src at proofHeight, src's connection
// connID, src's client clientID (which tracks the other chain), and
// that client's latest consensus state -- the other chain then checks
// the last two against its own real history (ValidateSelfClient /
// GetSelfConsensusState), which is where a chain's self-consensus
// bookkeeping actually gets exercised.
func queryHandshakeProofs(src *Chain, connID, clientID string, proofHeight clienttypes.Height) (counterpartyProofs, error) {
	ctx := src.ClientCtx.WithHeight(int64(proofHeight.RevisionHeight))

	connResp, err := connectionutils.QueryConnection(ctx, connID, true)
	if err != nil {
		return counterpartyProofs{}, fmt.Errorf("%s: proving connection %s: %w", src.Name, connID, err)
	}
	csResp, err := clientutils.QueryClientStateABCI(ctx, clientID)
	if err != nil {
		return counterpartyProofs{}, fmt.Errorf("%s: proving client %s: %w", src.Name, clientID, err)
	}
	cs, err := clienttypes.UnpackClientState(csResp.ClientState)
	if err != nil {
		return counterpartyProofs{}, err
	}
	tmCS, ok := cs.(*ibctm.ClientState)
	if !ok {
		return counterpartyProofs{}, fmt.Errorf("%s: client %s is %T, not 07-tendermint", src.Name, clientID, cs)
	}
	consResp, err := clientutils.QueryConsensusStateABCI(ctx, clientID, tmCS.LatestHeight)
	if err != nil {
		return counterpartyProofs{}, fmt.Errorf("%s: proving consensus state %s@%s: %w", src.Name, clientID, tmCS.LatestHeight, err)
	}

	for name, h := range map[string]clienttypes.Height{"connection": connResp.ProofHeight, "client": csResp.ProofHeight, "consensus": consResp.ProofHeight} {
		if !h.EQ(proofHeight) {
			return counterpartyProofs{}, fmt.Errorf("%s: %s proof at %s, want %s", src.Name, name, h, proofHeight)
		}
	}

	return counterpartyProofs{
		connection:      *connResp.Connection,
		connProof:       connResp.Proof,
		clientState:     tmCS,
		clientProof:     csResp.Proof,
		consensusProof:  consResp.Proof,
		consensusHeight: tmCS.LatestHeight,
		proofHeight:     proofHeight,
	}, nil
}

var ibcPrefix = commitmenttypes.NewMerklePrefix([]byte(ibcexported.StoreKey))

// OpenConnection runs the full four-step connection handshake:
// INIT on a, TRY on b, ACK on a, CONFIRM on b. clientA lives on a and
// tracks b; clientB lives on b and tracks a.
func OpenConnection(a, b *Chain, clientA, clientB string) (connA, connB string, err error) {
	initMsg := connectiontypes.NewMsgConnectionOpenInit(clientA, clientB, ibcPrefix, nil, 0, a.FromAddrStr)
	events, err := a.SignAndBroadcast(initMsg)
	if err != nil {
		return "", "", fmt.Errorf("ConnOpenInit: %w", err)
	}
	if connA, err = EventAttr(events, connectiontypes.EventTypeConnectionOpenInit, connectiontypes.AttributeKeyConnectionID); err != nil {
		return "", "", err
	}

	events, err = relayStep(a, b, clientB, func(h clienttypes.Height) (sdk.Msg, error) {
		p, err := queryHandshakeProofs(a, connA, clientA, h)
		if err != nil {
			return nil, err
		}
		return connectiontypes.NewMsgConnectionOpenTry(
			clientB, connA, clientA, p.clientState, ibcPrefix, connectiontypes.GetCompatibleVersions(), 0,
			p.connProof, p.clientProof, p.consensusProof, p.proofHeight, p.consensusHeight, b.FromAddrStr,
		), nil
	})
	if err != nil {
		return "", "", fmt.Errorf("ConnOpenTry: %w", err)
	}
	if connB, err = EventAttr(events, connectiontypes.EventTypeConnectionOpenTry, connectiontypes.AttributeKeyConnectionID); err != nil {
		return "", "", err
	}

	if _, err = relayStep(b, a, clientA, func(h clienttypes.Height) (sdk.Msg, error) {
		p, err := queryHandshakeProofs(b, connB, clientB, h)
		if err != nil {
			return nil, err
		}
		return connectiontypes.NewMsgConnectionOpenAck(
			connA, connB, p.clientState, p.connProof, p.clientProof, p.consensusProof,
			p.proofHeight, p.consensusHeight, p.connection.Versions[0], a.FromAddrStr,
		), nil
	}); err != nil {
		return "", "", fmt.Errorf("ConnOpenAck: %w", err)
	}

	if _, err = relayStep(a, b, clientB, func(h clienttypes.Height) (sdk.Msg, error) {
		resp, err := connectionutils.QueryConnection(a.ClientCtx.WithHeight(int64(h.RevisionHeight)), connA, true)
		if err != nil {
			return nil, fmt.Errorf("%s: proving connection %s: %w", a.Name, connA, err)
		}
		return connectiontypes.NewMsgConnectionOpenConfirm(connB, resp.Proof, resp.ProofHeight, b.FromAddrStr), nil
	}); err != nil {
		return "", "", fmt.Errorf("ConnOpenConfirm: %w", err)
	}

	return connA, connB, nil
}

// ConnectionState returns connID's state on c, e.g. STATE_OPEN.
func ConnectionState(c *Chain, connID string) (connectiontypes.State, error) {
	resp, err := connectionutils.QueryConnection(c.ClientCtx, connID, false)
	if err != nil {
		return 0, err
	}
	return resp.Connection.State, nil
}
