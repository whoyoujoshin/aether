package wallet

import (
	"context"
	"fmt"
	"strings"
	"testing"

	abci "github.com/cometbft/cometbft/abci/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	txtypes "github.com/cosmos/cosmos-sdk/types/tx"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

func transfer(from, to, amount string) abci.Event {
	return abci.Event{Type: "transfer", Attributes: []abci.EventAttribute{
		{Key: "recipient", Value: to}, {Key: "sender", Value: from}, {Key: "amount", Value: amount},
	}}
}

// fakeSearcher serves a fixed, height-ordered list the way CometBFT's
// tx search pages it.
type fakeSearcher struct {
	all     []*sdk.TxResponse
	queries []string
}

func (f *fakeSearcher) GetTxsEvent(_ context.Context, in *txtypes.GetTxsEventRequest, _ ...grpc.CallOption) (*txtypes.GetTxsEventResponse, error) {
	f.queries = append(f.queries, in.Query)
	if in.OrderBy != txtypes.OrderBy_ORDER_BY_ASC {
		return nil, fmt.Errorf("want ascending order")
	}
	start := int((in.Page - 1) * in.Limit)
	end := min(start+int(in.Limit), len(f.all))
	resp := &txtypes.GetTxsEventResponse{Total: uint64(len(f.all))}
	if start < len(f.all) {
		resp.TxResponses = f.all[start:end]
		for _, r := range resp.TxResponses {
			resp.Txs = append(resp.Txs, &txtypes.Tx{Body: &txtypes.TxBody{Memo: "memo-" + r.TxHash}})
		}
	}
	return resp, nil
}

func TestIncomingPayments_ReadsEveryPage(t *testing.T) {
	me := sdk.AccAddress("me__________________").String()
	other := sdk.AccAddress("other_______________").String()
	payer := sdk.AccAddress("payer_______________").String()

	f := &fakeSearcher{}
	for i := 0; i < 250; i++ {
		f.all = append(f.all, &sdk.TxResponse{
			TxHash: fmt.Sprintf("TX%03d", i), Height: int64(100 + i),
			Events: []abci.Event{transfer(payer, me, "1uaeth")},
		})
	}
	// The one that matters is the 250th -- past any "latest 100" window
	// of a busy address.
	f.all[249].Events = []abci.Event{
		transfer(payer, other, "7uaeth"), // not to me
		transfer(payer, me, "500uaeth"),
		transfer(payer, me, "250uaeth"),
	}

	got, err := incomingPayments(context.Background(), f, me, 100, 10_000)
	require.NoError(t, err)
	require.Len(t, got, 250)
	require.Len(t, f.queries, 3, "250 results = 3 pages of 100")
	require.Equal(t, fmt.Sprintf("transfer.recipient='%s' AND tx.height>=100", me), f.queries[0])

	last := got[249]
	require.Equal(t, "TX249", last.Hash)
	require.Equal(t, "750uaeth", last.Amount.String(), "all transfers to me summed, none to others")
	require.Equal(t, payer, last.From)
	require.Equal(t, "memo-TX249", last.Memo)
	require.Equal(t, int64(349), last.Height)

	capped, err := incomingPayments(context.Background(), f, me, 1, 150)
	require.ErrorIs(t, err, ErrTooMuchHistory)
	require.GreaterOrEqual(t, len(capped), 150)
}

func TestIncomingPayments_RejectsQueryInjection(t *testing.T) {
	f := &fakeSearcher{}
	_, err := incomingPayments(context.Background(), f, "x' OR tx.height>0 OR '", 1, 10)
	require.Error(t, err)
	require.Empty(t, f.queries)
	require.False(t, strings.Contains(strings.Join(f.queries, ""), "OR"))
}
