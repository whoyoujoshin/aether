package wallet

import (
	"context"
	"fmt"
	"sort"

	txtypes "github.com/cosmos/cosmos-sdk/types/tx"
)

// MsgSubmitPoWTypeURL is the message a miner's PoW submission carries.
const MsgSubmitPoWTypeURL = "/aether.pow.v1.MsgSubmitPoW"

// PoWSubmission is one of an address's own MsgSubmitPoW transactions
// that made it into a block. Code is 0 when it was accepted; otherwise
// it failed in the block (stale block hash, too little work, ...) and
// RawLog says why.
type PoWSubmission struct {
	Hash      string `json:"txHash"`
	Height    int64  `json:"height"`
	Code      uint32 `json:"code"`
	Codespace string `json:"codespace,omitempty"`
	RawLog    string `json:"rawLog,omitempty"`
	Timestamp string `json:"timestamp,omitempty"`
}

// PoWSubmissions returns address's most recent PoW submissions, newest
// first, failed ones included. A failed transaction keeps only the
// events its fees were charged under, so they're found by fee payer
// (tx.fee_payer, emitted even for a zero fee) rather than by
// message.sender, which only a successful one records.
func (c *Client) PoWSubmissions(ctx context.Context, address string, limit uint64) ([]PoWSubmission, error) {
	return powSubmissions(ctx, txtypes.NewServiceClient(c.conn), address, limit)
}

func powSubmissions(ctx context.Context, s txSearcher, address string, limit uint64) ([]PoWSubmission, error) {
	resp, err := s.GetTxsEvent(ctx, &txtypes.GetTxsEventRequest{
		Query:   fmt.Sprintf("tx.fee_payer='%s'", address),
		OrderBy: txtypes.OrderBy_ORDER_BY_DESC,
		Limit:   limit,
	})
	if err != nil {
		return nil, fmt.Errorf("pow submissions of %s: %w", address, err)
	}
	var out []PoWSubmission
	for i, r := range resp.TxResponses {
		if !carriesMsg(resp.Txs, i, MsgSubmitPoWTypeURL) {
			continue
		}
		out = append(out, PoWSubmission{
			Hash:      r.TxHash,
			Height:    r.Height,
			Code:      r.Code,
			Codespace: r.Codespace,
			RawLog:    r.RawLog,
			Timestamp: r.Timestamp,
		})
	}
	sort.SliceStable(out, func(a, b int) bool { return out[a].Height > out[b].Height })
	return out, nil
}

func carriesMsg(txs []*txtypes.Tx, i int, typeURL string) bool {
	if i >= len(txs) || txs[i] == nil || txs[i].Body == nil {
		return false
	}
	for _, m := range txs[i].Body.Messages {
		if m.TypeUrl == typeURL {
			return true
		}
	}
	return false
}
