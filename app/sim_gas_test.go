package app_test

import (
	"encoding/json"
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
// sent at it ran out of gas. Check both: the first send (no pubkey on
// chain yet) and the second.
func TestSimulateGasCoversMLDSASignature(t *testing.T) {
	registry := codectypes.NewInterfaceRegistry()
	mldsa.RegisterInterfaces(registry)
	w, err := wallet.NewWallet("aetherd", "test", t.TempDir(), codec.NewProtoCodec(registry))
	require.NoError(t, err)
	sender, _, err := w.CreateAccount("sender")
	require.NoError(t, err)
	senderAddr := sdk.MustAccAddressFromBech32(sender.Address)
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

	ten := sdk.NewCoins(sdk.NewInt64Coin("uaeth", 10_000_000))
	require.NoError(t, a.BankKeeper.MintCoins(c.ctx(), pow.ModuleName, ten))
	require.NoError(t, a.BankKeeper.SendCoinsFromModuleToAccount(c.ctx(), pow.ModuleName, senderAddr, ten))

	send := banktypes.NewMsgSend(senderAddr, to, sdk.NewCoins(sdk.NewInt64Coin("uaeth", 1000)))
	for _, round := range []string{"no pubkey on chain", "pubkey on chain"} {
		info := a.AccountKeeper.GetAccount(c.ctx(), senderAddr)
		if round == "pubkey on chain" {
			require.IsType(t, &mldsa.PubKey{}, info.GetPubKey())
		} else {
			require.Nil(t, info.GetPubKey())
		}

		signed, err := w.BuildAndSignMsgTx("sender", send, wallet.TxParams{
			ChainID:       e2eChainID,
			AccountNumber: info.GetAccountNumber(),
			Sequence:      info.GetSequence(),
			GasLimit:      400_000,
			Fees:          sdk.NewCoins(sdk.NewCoin("uaeth", sdkmath.ZeroInt())),
		})
		require.NoError(t, err)

		// What the CLI simulates: the same tx with its signature left empty.
		decoded, err := enc.TxConfig.TxDecoder()(signed.Bytes)
		require.NoError(t, err)
		builder, err := enc.TxConfig.WrapTxBuilder(decoded)
		require.NoError(t, err)
		sigs, err := decoded.(authsigning.SigVerifiableTx).GetSignaturesV2()
		require.NoError(t, err)
		require.Len(t, sigs, 1)
		sigs[0].Data = &txsigning.SingleSignatureData{SignMode: txsigning.SignMode_SIGN_MODE_DIRECT}
		require.NoError(t, builder.SetSignatures(sigs...))
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

		t.Logf("%s: sim %d real %d txbytes %d simbytes %d", round, gasInfo.GasUsed, res.GasUsed, len(signed.Bytes), len(simBytes))
		require.GreaterOrEqual(t, gasInfo.GasUsed, uint64(res.GasUsed), "%s: estimate below real use", round)
		// x/auth's estimate runs over on its own once the pubkey is on
		// chain: it counts the pubkey a second time (about 1,320 bytes,
		// 13k gas) and, only when simulating, reads the account holding
		// it (about 5k). Measured: 2.9k over, then 19.8k.
		require.Less(t, gasInfo.GasUsed-uint64(res.GasUsed), uint64(25_000), "%s: estimate far above real use", round)
	}
}
