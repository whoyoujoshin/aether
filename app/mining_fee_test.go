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

// A chain whose nodes ask a minimum gas price: what aether-1 is planned
// to do (docs/MAINNET.md).
func newPricedChain(t *testing.T, minGasPrices string, keys ...string) *rotChain {
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
		baseapp.SetChainID(chainID), baseapp.SetMinGasPrices(minGasPrices)).(*app.App)
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

func checkTx(t *testing.T, c *rotChain, txBytes []byte) *abci.ResponseCheckTx {
	t.Helper()
	res, err := c.app.CheckTx(&abci.RequestCheckTx{Tx: txBytes, Type: abci.CheckTxType_New})
	require.NoError(t, err)
	return res
}

// A node asking a minimum gas price admits a lone native mining
// submission with no fee, but only one whose proof-of-work checks out;
// everything else still pays the price. The block then accepts the
// fee-free find.
func TestMiningFee_LoneNativeSubmissionNeedsNoFee(t *testing.T) {
	c := newPricedChain(t, "0.0005uaeth", "miner")
	c.fund("miner")
	miner_ := c.addr("miner")

	// A low difficulty, so the test finds a nonce quickly; recorded for
	// the next block.
	c.app.PowKeeper.SetDifficulty(c.ctx(), sdkmath.NewInt(4))
	c.block()
	height := c.height
	prevHash, ok := c.app.PowKeeper.GetRecentHash(c.ctx(), height)
	require.True(t, ok)
	diff, ok := c.app.PowKeeper.GetRecentDifficulty(c.ctx(), height)
	require.True(t, ok)

	h := miner.Header{Height: uint64(height), Timestamp: e2eGenesisTime.Unix(), PrevHash: prevHash, MerkleRoot: miner.MerkleRoot, Difficulty: diff.Uint64(), Miner: miner_}
	var n atomic.Uint64
	found, ok := miner.Search(context.Background(), h, 0, 1, 100_000, &n)
	require.True(t, ok)
	submission := func(nonce uint64) sdk.Msg {
		return &pow.MsgSubmitPoW{Miner: miner_.String(), Submission: &pow.MsgSubmitPoW_Native{Native: &pow.NativeSubmission{
			Height: found.Height, Timestamp: found.Timestamp, PrevHash: found.PrevHash, MerkleRoot: found.MerkleRoot, Nonce: nonce, Difficulty: found.Difficulty,
		}}}
	}

	// Anything else pays the price: a send with no fee is refused.
	res := checkTx(t, c, c.signAs("miner", miner_, send(miner_, 1)))
	require.NotZero(t, res.Code)
	require.Contains(t, res.Log, "insufficient fees")

	// A fake find (a nonce whose hash misses the target) gets no free pass.
	bad := found.Nonce + 1
	for miner.Meets(miner.Hash(miner.Header{Height: found.Height, Timestamp: found.Timestamp, PrevHash: found.PrevHash, MerkleRoot: found.MerkleRoot, Nonce: bad, Difficulty: found.Difficulty, Miner: miner_}.Bytes()), found.Difficulty) {
		bad++
	}
	res = checkTx(t, c, c.signAs("miner", miner_, submission(bad)))
	require.NotZero(t, res.Code)
	require.Contains(t, res.Log, "proof of work verification failed")

	// A real find with no fee is admitted, and the block takes it.
	good := c.signAs("miner", miner_, submission(found.Nonce))
	res = checkTx(t, c, good)
	require.Zero(t, res.Code, res.Log)
	out := c.block(good)
	require.Len(t, out, 1)
	require.Zero(t, out[0].Code, out[0].Log)
}
