package wallet

import (
	"context"
	"fmt"
	"testing"

	abci "github.com/cometbft/cometbft/abci/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	"github.com/whoyoujoshin/aether/x/escrow"
)

func created(id int, terms string) abci.Event {
	return abci.Event{Type: escrow.EventTypeCreated, Attributes: []abci.EventAttribute{
		{Key: escrow.AttributeID, Value: fmt.Sprint(id)}, {Key: escrow.AttributePayer, Value: "payer"}, {Key: escrow.AttributePayee, Value: "payee"},
		{Key: escrow.AttributeAmount, Value: "5uaeth"}, {Key: escrow.AttributeExpiresAt, Value: "1792640645"},
		{Key: escrow.AttributeOnExpiry, Value: "ON_EXPIRY_RELEASE"}, {Key: escrow.AttributeTerms, Value: terms},
	}}
}

func TestEscrowsCreatedWithTerms(t *testing.T) {
	f := &fakeSearcher{}
	for i := 0; i < 150; i++ {
		f.all = append(f.all, &sdk.TxResponse{TxHash: fmt.Sprintf("TX%03d", i), Height: int64(10 + i), Events: []abci.Event{created(i, "other")}})
	}
	f.all[3].Events = []abci.Event{created(3, "job:00000000000000a1")}
	f.all[140].Events = []abci.Event{created(140, "job:00000000000000b2")}
	f.all[141].Code = 5 // failed: no escrow
	f.all[141].Events = []abci.Event{created(141, "job:00000000000000c3")}

	got, err := escrowsCreatedWithTerms(context.Background(), f, "job:", 1, 1000)
	require.NoError(t, err)
	require.Len(t, got, 2, "only matching terms, on every page, from transactions that succeeded")
	require.Equal(t, EscrowCreated{ID: 3, Payer: "payer", Payee: "payee", Amount: "5uaeth", ExpiresAt: 1792640645, OnExpiry: "ON_EXPIRY_RELEASE",
		Terms: "job:00000000000000a1", TxHash: "TX003", Height: 13}, got[0])
	require.Equal(t, uint64(140), got[1].ID)
	require.Equal(t, "escrow_created.terms CONTAINS 'job:' AND tx.height>=1", f.queries[0])

	_, err = escrowsCreatedWithTerms(context.Background(), f, "job:' OR tx.height>0 OR 'a", 1, 1000)
	require.Error(t, err, "nothing can be injected into the query")
}
