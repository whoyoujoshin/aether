package wallet

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/cosmos/cosmos-sdk/client/grpc/cmtservice"
	txtypes "github.com/cosmos/cosmos-sdk/types/tx"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/whoyoujoshin/aether/x/escrow"
)

// ErrEscrowNotActive means the node doesn't serve x/escrow: it hasn't
// reached EscrowActivationHeight, or runs an older binary.
var ErrEscrowNotActive = errors.New("x/escrow is not active on this node yet")

// ErrEscrowNotFound means no open escrow has the id, and no settlement
// or creation of it is on chain either.
var ErrEscrowNotFound = errors.New("escrow not found")

func escrowErr(err error) error {
	switch status.Code(err) {
	case codes.Unimplemented:
		return ErrEscrowNotActive
	case codes.NotFound:
		return ErrEscrowNotFound
	}
	return err
}

// Escrow is the open escrow with this id.
func (c *Client) Escrow(ctx context.Context, id uint64) (*escrow.Escrow, error) {
	res, err := escrow.NewQueryClient(c.conn).Escrow(ctx, &escrow.QueryEscrowRequest{Id: id})
	if err != nil {
		return nil, escrowErr(err)
	}
	return &res.Escrow, nil
}

// EscrowsOf is every open escrow address is payer, payee or arbiter of,
// oldest first.
func (c *Client) EscrowsOf(ctx context.Context, address string) ([]escrow.Escrow, error) {
	q := escrow.NewQueryClient(c.conn)
	var out []escrow.Escrow
	var after uint64
	for {
		res, err := q.EscrowsByAddress(ctx, &escrow.QueryEscrowsByAddressRequest{Address: address, AfterId: after})
		if err != nil {
			return nil, escrowErr(err)
		}
		out = append(out, res.Escrows...)
		if res.NextAfterId == 0 {
			return out, nil
		}
		after = res.NextAfterId
	}
}

// EscrowCreatedID is the id of the escrow a confirmed MsgCreateEscrow
// transaction created.
func (c *Client) EscrowCreatedID(ctx context.Context, txHash string) (uint64, error) {
	res, err := txtypes.NewServiceClient(c.conn).GetTx(ctx, &txtypes.GetTxRequest{Hash: txHash})
	if status.Code(err) == codes.NotFound {
		return 0, fmt.Errorf("%w: %s", ErrTransactionNotFound, txHash)
	}
	if err != nil {
		return 0, err
	}
	for _, ev := range res.TxResponse.Events {
		if ev.Type != escrow.EventTypeCreated {
			continue
		}
		for _, a := range ev.Attributes {
			if a.Key == escrow.AttributeID {
				return strconv.ParseUint(a.Value, 10, 64)
			}
		}
	}
	return 0, fmt.Errorf("transaction %s created no escrow", txHash)
}

// EscrowOutcome is how a settled escrow ended.
type EscrowOutcome struct {
	ID       uint64
	Released bool   // to the payee; false: refunded to the payer
	By       string // who settled it, or escrow.ByExpiry
	TxHash   string // the settling transaction; empty when settled at the deadline
	Height   int64  // its block; 0 when settled at the deadline
	Payer    string
	Payee    string
	Amount   string
}

// EscrowOutcome finds how escrow id was settled: by a release or refund
// transaction (tx search on its event), or -- when neither exists -- at
// its deadline, which EndBlock settles outside any transaction; then the
// creation event gives the deadline and the outcome its payer chose, and
// the latest block's time says whether the deadline has passed.
// ErrEscrowNotFound when none of that is on chain (or the escrow is
// still open).
func (c *Client) EscrowOutcome(ctx context.Context, id uint64) (*EscrowOutcome, error) {
	txs := txtypes.NewServiceClient(c.conn)
	search := func(eventType string) (*txtypes.GetTxsEventResponse, error) {
		return txs.GetTxsEvent(ctx, &txtypes.GetTxsEventRequest{
			Query: fmt.Sprintf("%s.%s='%d'", eventType, escrow.AttributeID, id),
			Limit: 1,
		})
	}
	for _, t := range []string{escrow.EventTypeReleased, escrow.EventTypeRefunded} {
		res, err := search(t)
		if err != nil {
			return nil, err
		}
		for _, r := range res.TxResponses {
			for _, ev := range r.Events {
				if ev.Type != t {
					continue
				}
				attrs := map[string]string{}
				for _, a := range ev.Attributes {
					attrs[a.Key] = a.Value
				}
				if attrs[escrow.AttributeID] != strconv.FormatUint(id, 10) {
					continue
				}
				return &EscrowOutcome{
					ID: id, Released: t == escrow.EventTypeReleased, By: attrs[escrow.AttributeBy],
					TxHash: r.TxHash, Height: r.Height,
					Payer: attrs[escrow.AttributePayer], Payee: attrs[escrow.AttributePayee], Amount: attrs[escrow.AttributeAmount],
				}, nil
			}
		}
	}

	created, err := search(escrow.EventTypeCreated)
	if err != nil {
		return nil, err
	}
	for _, r := range created.TxResponses {
		for _, ev := range r.Events {
			if ev.Type != escrow.EventTypeCreated {
				continue
			}
			attrs := map[string]string{}
			for _, a := range ev.Attributes {
				attrs[a.Key] = a.Value
			}
			if attrs[escrow.AttributeID] != strconv.FormatUint(id, 10) {
				continue
			}
			expiresAt, err := strconv.ParseInt(attrs[escrow.AttributeExpiresAt], 10, 64)
			if err != nil {
				return nil, fmt.Errorf("escrow %d's creation event has no deadline: %w", id, err)
			}
			latest, err := cmtservice.NewServiceClient(c.conn).GetLatestBlock(ctx, &cmtservice.GetLatestBlockRequest{})
			if err != nil {
				return nil, err
			}
			if latest.SdkBlock == nil || latest.SdkBlock.Header.Time.Unix() < expiresAt {
				return nil, ErrEscrowNotFound // not due: it's still open, or the query raced its creation
			}
			return &EscrowOutcome{
				ID: id, Released: attrs[escrow.AttributeOnExpiry] == escrow.ON_EXPIRY_RELEASE.String(), By: escrow.ByExpiry,
				Payer: attrs[escrow.AttributePayer], Payee: attrs[escrow.AttributePayee], Amount: attrs[escrow.AttributeAmount],
			}, nil
		}
	}
	return nil, ErrEscrowNotFound
}
