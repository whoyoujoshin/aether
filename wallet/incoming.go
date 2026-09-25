package wallet

import (
	"context"
	"errors"
	"fmt"

	abci "github.com/cometbft/cometbft/abci/types"
	"github.com/cosmos/cosmos-sdk/client/grpc/cmtservice"
	sdk "github.com/cosmos/cosmos-sdk/types"
	txtypes "github.com/cosmos/cosmos-sdk/types/tx"
	"google.golang.org/grpc"
)

// IncomingPayment is one transaction that moved funds to an address.
type IncomingPayment struct {
	Hash      string
	Height    int64
	Code      uint32 // 0 = succeeded
	Timestamp string
	Memo      string    // set by the sender; untrusted
	From      string    // sender of the first transfer to the address
	Amount    sdk.Coins // everything this transaction moved to the address
}

// ErrTooMuchHistory means a scan hit its result cap before reaching
// the chain tip; scan from a later height.
var ErrTooMuchHistory = errors.New("too many transactions to scan")

// txSearchPageSize is CometBFT's maximum page size for tx search.
const txSearchPageSize = 100

type txSearcher interface {
	GetTxsEvent(ctx context.Context, in *txtypes.GetTxsEventRequest, opts ...grpc.CallOption) (*txtypes.GetTxsEventResponse, error)
}

// GetIncomingPayments returns every indexed transaction at or above
// sinceHeight that transferred funds to address, oldest first, reading
// all result pages. Unlike a "latest N" query it can't miss a payment
// on a busy address; it stops with ErrTooMuchHistory (returning what
// it has) once it has maxResults.
func (c *Client) GetIncomingPayments(address string, sinceHeight int64, maxResults int) ([]IncomingPayment, error) {
	return incomingPayments(context.Background(), txtypes.NewServiceClient(c.conn), address, sinceHeight, maxResults)
}

func incomingPayments(ctx context.Context, s txSearcher, address string, sinceHeight int64, maxResults int) ([]IncomingPayment, error) {
	// The address goes into a query string: only a valid bech32
	// address may, so nothing can be injected into the query.
	if _, err := sdk.AccAddressFromBech32(address); err != nil {
		return nil, fmt.Errorf("invalid address %q: %w", address, err)
	}
	if sinceHeight < 1 {
		sinceHeight = 1
	}
	query := fmt.Sprintf("transfer.recipient='%s' AND tx.height>=%d", address, sinceHeight)

	var out []IncomingPayment
	for page := uint64(1); ; page++ {
		resp, err := s.GetTxsEvent(ctx, &txtypes.GetTxsEventRequest{
			Query:   query,
			OrderBy: txtypes.OrderBy_ORDER_BY_ASC,
			Page:    page,
			Limit:   txSearchPageSize,
		})
		if err != nil {
			return out, fmt.Errorf("failed to search incoming transactions: %w", err)
		}
		for i, r := range resp.TxResponses {
			if p, ok := incomingFrom(r.Events, address); ok {
				p.Hash, p.Height, p.Code, p.Timestamp, p.Memo = r.TxHash, r.Height, r.Code, r.Timestamp, memoAt(resp.Txs, i)
				out = append(out, p)
			}
		}
		if len(out) >= maxResults {
			return out, ErrTooMuchHistory
		}
		// Oldest first, so transactions landing mid-scan only append:
		// earlier pages never shift.
		if len(resp.TxResponses) < txSearchPageSize || page*txSearchPageSize >= resp.Total {
			return out, nil
		}
	}
}

// incomingFrom totals the transfers to address among a transaction's
// events. A transaction can hold several (e.g. a MsgMultiSend).
func incomingFrom(events []abci.Event, address string) (IncomingPayment, bool) {
	var p IncomingPayment
	found := false
	for _, e := range events {
		if e.Type != "transfer" {
			continue
		}
		var sender, recipient, amount string
		for _, a := range e.Attributes {
			switch a.Key {
			case "sender":
				sender = a.Value
			case "recipient":
				recipient = a.Value
			case "amount":
				amount = a.Value
			}
		}
		if recipient != address {
			continue
		}
		coins, err := sdk.ParseCoinsNormalized(amount)
		if err != nil {
			continue
		}
		if !found {
			p.From = sender
		}
		found = true
		p.Amount = p.Amount.Add(coins...)
	}
	return p, found
}

// GetLatestHeight returns the height of the node's latest block.
func (c *Client) GetLatestHeight() (int64, error) {
	resp, err := cmtservice.NewServiceClient(c.conn).GetLatestBlock(context.Background(), &cmtservice.GetLatestBlockRequest{})
	if err != nil {
		return 0, err
	}
	if resp.SdkBlock != nil {
		return resp.SdkBlock.Header.Height, nil
	}
	if resp.Block != nil {
		return resp.Block.Header.Height, nil
	}
	return 0, errors.New("node returned no latest block")
}
