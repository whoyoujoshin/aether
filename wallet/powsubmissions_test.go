package wallet

import (
	"context"
	"testing"

	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	txtypes "github.com/cosmos/cosmos-sdk/types/tx"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

type feePayerSearcher struct {
	query string
	resp  *txtypes.GetTxsEventResponse
}

func (f *feePayerSearcher) GetTxsEvent(_ context.Context, in *txtypes.GetTxsEventRequest, _ ...grpc.CallOption) (*txtypes.GetTxsEventResponse, error) {
	f.query = in.Query
	return f.resp, nil
}

func txWith(typeURLs ...string) *txtypes.Tx {
	body := &txtypes.TxBody{}
	for _, u := range typeURLs {
		body.Messages = append(body.Messages, &codectypes.Any{TypeUrl: u})
	}
	return &txtypes.Tx{Body: body}
}

func TestPoWSubmissions_KeepsFailedOnesAndOnlySubmitPoW(t *testing.T) {
	s := &feePayerSearcher{resp: &txtypes.GetTxsEventResponse{
		TxResponses: []*sdk.TxResponse{
			{TxHash: "SEND", Height: 30},
			{TxHash: "OLD", Height: 10},
			{TxHash: "FAILED", Height: 20, Code: 5, Codespace: "pow", RawLog: "stale block hash"},
			{TxHash: "NEW", Height: 25},
		},
		Txs: []*txtypes.Tx{
			txWith("/cosmos.bank.v1beta1.MsgSend"),
			txWith(MsgSubmitPoWTypeURL),
			txWith(MsgSubmitPoWTypeURL),
			txWith(MsgSubmitPoWTypeURL),
		},
	}}
	subs, err := powSubmissions(context.Background(), s, "aether1miner", 25)
	require.NoError(t, err)
	require.Equal(t, "tx.fee_payer='aether1miner'", s.query, "found by fee payer, so failed ones are included")
	require.Equal(t, []PoWSubmission{
		{Hash: "NEW", Height: 25},
		{Hash: "FAILED", Height: 20, Code: 5, Codespace: "pow", RawLog: "stale block hash"},
		{Hash: "OLD", Height: 10},
	}, subs)
}
