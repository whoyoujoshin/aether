package app

import (
	"math/rand"
	"testing"
	"time"

	abci "github.com/cometbft/cometbft/abci/types"
	"github.com/cosmos/cosmos-sdk/baseapp"
	simtestutil "github.com/cosmos/cosmos-sdk/testutil/sims"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/mempool"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	ibctesting "github.com/cosmos/ibc-go/v8/testing"
	"github.com/stretchr/testify/require"
)

// TestPrepareProposalWithAppMempool: with the app-side mempool on
// (app.toml mempool.max-txs >= 0), PrepareProposal re-encodes each
// pending transaction, which needs the app's tx encoder. Without one it
// panicked, and BaseApp fell back to CometBFT's own list, so the
// proposal ignored the app mempool entirely: here that shows as a
// pending transaction missing from the proposal.
func TestPrepareProposalWithAppMempool(t *testing.T) {
	coord := &ibctesting.Coordinator{T: t, CurrentTime: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	chain := newIBCTestChainWithOptions(t, coord, "aether-test-a", baseapp.SetMempool(mempool.NewSenderNonceMempool(mempool.SenderNonceMaxTxOpt(5000))))
	app := chain.App.(*App)

	sender := chain.SenderAccount.GetAddress().String()
	msg := &banktypes.MsgSend{FromAddress: sender, ToAddress: sender, Amount: sdk.NewCoins(sdk.NewInt64Coin("uaeth", 1))}
	tx, err := simtestutil.GenSignedMockTx(rand.New(rand.NewSource(1)), app.GetTxConfig(), []sdk.Msg{msg},
		sdk.NewCoins(sdk.NewInt64Coin("uaeth", 1000)), 400_000, chain.ChainID,
		[]uint64{chain.SenderAccount.GetAccountNumber()}, []uint64{chain.SenderAccount.GetSequence()}, chain.SenderPrivKey)
	require.NoError(t, err)
	bz, err := app.GetTxConfig().TxEncoder()(tx)
	require.NoError(t, err)

	res, err := app.CheckTx(&abci.RequestCheckTx{Tx: bz, Type: abci.CheckTxType_New})
	require.NoError(t, err)
	require.Zero(t, res.Code, res.Log)
	require.Equal(t, 1, app.Mempool().CountTx())

	prep, err := app.PrepareProposal(&abci.RequestPrepareProposal{
		Height: app.LastBlockHeight() + 1, Time: chain.CurrentHeader.Time, MaxTxBytes: 1 << 20,
		NextValidatorsHash: chain.NextVals.Hash(), ProposerAddress: chain.CurrentHeader.ProposerAddress,
	})
	require.NoError(t, err)
	require.Equal(t, [][]byte{bz}, prep.Txs, "the proposal must come from the app mempool")
}
