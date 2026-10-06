package app_test

import (
	"encoding/json"
	"fmt"
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
	txsigning "github.com/cosmos/cosmos-sdk/types/tx/signing"
	authsigning "github.com/cosmos/cosmos-sdk/x/auth/signing"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	"github.com/stretchr/testify/require"

	"github.com/whoyoujoshin/aether/app"
	"github.com/whoyoujoshin/aether/crypto/mldsa"
	"github.com/whoyoujoshin/aether/wallet"
	"github.com/whoyoujoshin/aether/x/pow"
)

// `aetherd tx ... --gas auto` simulates the tx with empty signatures.
// Once the sender's pubkey was on chain, that panicked in x/auth
// ("Cannot encode unregistered concrete type mldsa.PubKey"). And the
// estimate left out most of the 2,420-byte ML-DSA-44 signature, so a tx
// sent at it ran out of gas. With --gas-prices, the estimate also ran
// with a zero fee, so it left out moving the fee, and still came in
// short. Check each: a first send (no pubkey on chain yet) and a later
// one, free and priced.
func TestSimulateGasCoversMLDSASignature(t *testing.T) {
	registry := codectypes.NewInterfaceRegistry()
	mldsa.RegisterInterfaces(registry)
	w, err := wallet.NewWallet("aetherd", "test", t.TempDir(), codec.NewProtoCodec(registry))
	require.NoError(t, err)
	to := sdk.AccAddress("some_recipient______")

	a := app.New(log.NewNopLogger(), dbm.NewMemDB(), nil, true, nil, t.TempDir(), 0, noAppOptions{}, baseapp.SetChainID(e2eChainID)).(*app.App)
	enc := app.MakeEncodingConfig()
	genesis, err := json.Marshal(app.ModuleBasics.DefaultGenesis(enc.Codec))
	require.NoError(t, err)
	_, err = a.InitChain(&abci.RequestInitChain{
		ChainId: e2eChainID, Time: e2eGenesisTime, InitialHeight: 1,
		AppStateBytes: genesis, ConsensusParams: simtestutil.DefaultConsensusParams,
	})
	require.NoError(t, err)
	c := &e2eChain{t: t, app: a, w: w, height: 1}
	_, err = a.FinalizeBlock(&abci.RequestFinalizeBlock{Height: 1, Time: c.blockTime(1)})
	require.NoError(t, err)
	_, err = a.Commit()
	require.NoError(t, err)

	addrs := map[string]sdk.AccAddress{}
	for _, name := range []string{"free", "priced"} {
		acc, _, err := w.CreateAccount(name)
		require.NoError(t, err)
		addrs[name] = sdk.MustAccAddressFromBech32(acc.Address)
		ten := sdk.NewCoins(sdk.NewInt64Coin("uaeth", 10_000_000))
		require.NoError(t, a.BankKeeper.MintCoins(c.ctx(), pow.ModuleName, ten))
		require.NoError(t, a.BankKeeper.SendCoinsFromModuleToAccount(c.ctx(), pow.ModuleName, addrs[name], ten))
	}

	for _, tc := range []struct {
		sender  string
		fee     int64 // what the real tx pays; the estimate runs with 0uaeth, as the CLI's does with --gas-prices
		onChain bool  // whether the sender's pubkey is on chain yet
	}{
		{"free", 0, false},
		{"free", 0, true},
		{"priced", 18, false},
		{"priced", 18, true},
	} {
		round := fmt.Sprintf("%s, pubkey on chain %v", tc.sender, tc.onChain)
		senderAddr := addrs[tc.sender]
		info := a.AccountKeeper.GetAccount(c.ctx(), senderAddr)
		if tc.onChain {
			require.IsType(t, &mldsa.PubKey{}, info.GetPubKey())
		} else {
			require.Nil(t, info.GetPubKey())
		}

		send := banktypes.NewMsgSend(senderAddr, to, sdk.NewCoins(sdk.NewInt64Coin("uaeth", 1000)))
		signed, err := w.BuildAndSignMsgTx(tc.sender, send, wallet.TxParams{
			ChainID:       e2eChainID,
			AccountNumber: info.GetAccountNumber(),
			Sequence:      info.GetSequence(),
			GasLimit:      400_000,
			Fees:          sdk.NewCoins(sdk.NewInt64Coin("uaeth", tc.fee)),
		})
		require.NoError(t, err)

		// What the CLI simulates: the same tx with its signature left
		// empty, and with --gas-prices, gas 0 and so a fee of 0uaeth.
		decoded, err := enc.TxConfig.TxDecoder()(signed.Bytes)
		require.NoError(t, err)
		builder, err := enc.TxConfig.WrapTxBuilder(decoded)
		require.NoError(t, err)
		sigs, err := decoded.(authsigning.SigVerifiableTx).GetSignaturesV2()
		require.NoError(t, err)
		require.Len(t, sigs, 1)
		sigs[0].Data = &txsigning.SingleSignatureData{SignMode: txsigning.SignMode_SIGN_MODE_DIRECT}
		require.NoError(t, builder.SetSignatures(sigs...))
		if tc.fee > 0 {
			builder.SetFeeAmount(sdk.Coins{sdk.NewCoin("uaeth", sdkmath.ZeroInt())})
			builder.SetGasLimit(0)
		}
		simBytes, err := enc.TxConfig.TxEncoder()(builder.GetTx())
		require.NoError(t, err)

		gasInfo, _, err := a.Simulate(simBytes)
		require.NoError(t, err, round)

		c.height++
		resp, err := a.FinalizeBlock(&abci.RequestFinalizeBlock{Height: c.height, Time: c.blockTime(c.height), Txs: [][]byte{signed.Bytes}})
		require.NoError(t, err)
		_, err = a.Commit()
		require.NoError(t, err)
		res := resp.TxResults[0]
		require.Zero(t, res.Code, res.Log)

		t.Logf("%s: sim %d real %d", round, gasInfo.GasUsed, res.GasUsed)
		require.GreaterOrEqual(t, gasInfo.GasUsed, uint64(res.GasUsed), "%s: estimate below real use", round)
		// x/auth's estimate runs over on its own once the pubkey is on
		// chain: it counts the pubkey a second time (about 1,320 bytes,
		// 13k gas) and, only when simulating, reads the account holding
		// it (about 5k). Measured: 2.9k over, then 19.8k.
		require.Less(t, gasInfo.GasUsed-uint64(res.GasUsed), uint64(25_000), "%s: estimate far above real use", round)
	}
}
