package app_test

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"

	"cosmossdk.io/log"
	sdkmath "cosmossdk.io/math"
	abci "github.com/cometbft/cometbft/abci/types"
	dbm "github.com/cosmos/cosmos-db"
	"github.com/cosmos/cosmos-sdk/baseapp"
	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	simtestutil "github.com/cosmos/cosmos-sdk/testutil/sims"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	"github.com/whoyoujoshin/aether/app"
	"github.com/whoyoujoshin/aether/crypto/mldsa"
	"github.com/whoyoujoshin/aether/miner"
	"github.com/whoyoujoshin/aether/wallet"
	"github.com/whoyoujoshin/aether/x/pow"
)

type mapAppOptions map[string]interface{}

func (m mapAppOptions) Get(k string) interface{} { return m[k] }

// newTippingChain is a chain whose node asks minGasPrices and runs the
// mempool aetherd runs (app.TippingMempoolOption).
func newTippingChain(t *testing.T, minGasPrices string, keys ...string) *rotChain {
	t.Helper()
	registry := codectypes.NewInterfaceRegistry()
	mldsa.RegisterInterfaces(registry)
	w, err := wallet.NewWallet("aetherd", "test", t.TempDir(), codec.NewProtoCodec(registry))
	require.NoError(t, err)
	for _, k := range keys {
		_, _, err := w.CreateAccount(k)
		require.NoError(t, err)
	}
	const chainID = "aether-rc-dev"
	a := app.New(log.NewNopLogger(), dbm.NewMemDB(), nil, true, nil, t.TempDir(), 0, noAppOptions{},
		baseapp.SetChainID(chainID), baseapp.SetMinGasPrices(minGasPrices),
		app.TippingMempoolOption(mapAppOptions{"mempool.max-txs": -1})).(*app.App)
	genesis, err := json.Marshal(app.ModuleBasics.DefaultGenesis(app.MakeEncodingConfig().Codec))
	require.NoError(t, err)
	_, err = a.InitChain(&abci.RequestInitChain{
		ChainId: chainID, Time: e2eGenesisTime, InitialHeight: 1,
		AppStateBytes: genesis, ConsensusParams: simtestutil.DefaultConsensusParams,
	})
	require.NoError(t, err)
	c := &rotChain{t: t, app: a, w: w, chainID: chainID}
	c.block()
	return c
}

func (c *rotChain) signWithFee(keyName string, msg sdk.Msg, feeUaeth int64) []byte {
	c.t.Helper()
	account := c.addr(keyName)
	info := c.app.AccountKeeper.GetAccount(c.ctx(), account)
	require.NotNil(c.t, info)
	signed, err := c.w.BuildAndSignMsgTx(keyName, msg, wallet.TxParams{
		ChainID: c.chainID, AccountNumber: info.GetAccountNumber(), Sequence: info.GetSequence(),
		GasLimit: 400_000, Fees: sdk.NewCoins(sdk.NewInt64Coin("uaeth", feeUaeth)),
	})
	require.NoError(c.t, err)
	return signed.Bytes
}

func (c *rotChain) propose(maxTxBytes int64) [][]byte {
	c.t.Helper()
	res, err := c.app.PrepareProposal(&abci.RequestPrepareProposal{
		Height: c.height + 1, Time: e2eGenesisTime, MaxTxBytes: maxTxBytes,
	})
	require.NoError(c.t, err)
	return res.Txs
}

// A tip goes first: with the mempool full of sends paying the minimum,
// the proposer puts a lone mining find first and a send paying ten times
// the minimum next, whatever order they arrived in. Paying under the
// minimum is still refused.
func TestTipping_TipAndMiningGoFirst(t *testing.T) {
	keys := []string{"spam1", "spam2", "spam3", "spam4", "spam5", "tipper", "miner"}
	c := newTippingChain(t, "0.0001uaeth", keys...)
	for _, k := range keys {
		c.fund(k)
	}
	floor := int64(40) // 400,000 gas at 0.0001uaeth

	// Below the minimum is refused, as before.
	res := checkTx(t, c, c.signWithFee("spam1", send(c.addr("spam1"), 1), floor-1))
	require.NotZero(t, res.Code)
	require.Contains(t, res.Log, "insufficient fees")

	// Spam at the minimum arrives first.
	var spam [][]byte
	for _, k := range keys[:5] {
		tx := c.signWithFee(k, send(c.addr(k), 1), floor)
		require.Zero(t, checkTx(t, c, tx).Code)
		spam = append(spam, tx)
	}
	tipped := c.signWithFee("tipper", send(c.addr("tipper"), 1), 10*floor)
	require.Zero(t, checkTx(t, c, tipped).Code)

	// Then a real find, with no fee.
	minerAddr := c.addr("miner")
	c.app.PowKeeper.SetDifficulty(c.ctx(), sdkmath.NewInt(4))
	c.block()
	height := c.height
	prevHash, ok := c.app.PowKeeper.GetRecentHash(c.ctx(), height)
	require.True(t, ok)
	diff, ok := c.app.PowKeeper.GetRecentDifficulty(c.ctx(), height)
	require.True(t, ok)
	h := miner.Header{Height: uint64(height), Timestamp: e2eGenesisTime.Unix(), PrevHash: prevHash, MerkleRoot: miner.MerkleRoot, Difficulty: diff.Uint64(), Miner: minerAddr}
	var n atomic.Uint64
	found, ok := miner.Search(context.Background(), h, 0, 1, 100_000, &n)
	require.True(t, ok)
	find := c.signWithFee("miner", &pow.MsgSubmitPoW{Miner: minerAddr.String(), Submission: &pow.MsgSubmitPoW_Native{Native: &pow.NativeSubmission{
		Height: found.Height, Timestamp: found.Timestamp, PrevHash: found.PrevHash, MerkleRoot: found.MerkleRoot, Nonce: found.Nonce, Difficulty: found.Difficulty,
	}}}, 0)
	require.Zero(t, checkTx(t, c, find).Code)
	require.Equal(t, 7, c.app.Mempool().CountTx())

	// Room for two: the find, then the tip.
	two := int64(len(find) + len(tipped) + 64)
	require.Equal(t, [][]byte{find, tipped}, c.propose(two))

	// Room for all: the find, the tip, then the spam.
	all := c.propose(1 << 20)
	require.Len(t, all, 7)
	require.Equal(t, find, all[0])
	require.Equal(t, tipped, all[1])
	require.ElementsMatch(t, spam, all[2:])

	// The block takes them all.
	out := c.block(all...)
	for i, r := range out {
		require.Zero(t, r.Code, "tx %d: %s", i, r.Log)
	}
}
