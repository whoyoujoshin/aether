package app_test

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"cosmossdk.io/log"
	sdkmath "cosmossdk.io/math"
	abci "github.com/cometbft/cometbft/abci/types"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	dbm "github.com/cosmos/cosmos-db"
	"github.com/cosmos/cosmos-sdk/baseapp"
	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	simtestutil "github.com/cosmos/cosmos-sdk/testutil/sims"
	sdk "github.com/cosmos/cosmos-sdk/types"
	sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"
	txtypes "github.com/cosmos/cosmos-sdk/types/tx"
	"github.com/cosmos/cosmos-sdk/x/authz"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	"github.com/stretchr/testify/require"

	"github.com/whoyoujoshin/aether/app"
	"github.com/whoyoujoshin/aether/crypto/mldsa"
	"github.com/whoyoujoshin/aether/gates"
	"github.com/whoyoujoshin/aether/wallet"
	"github.com/whoyoujoshin/aether/x/accountauth"
	"github.com/whoyoujoshin/aether/x/pow"
)

type rotChain struct {
	t       *testing.T
	app     *app.App
	w       *wallet.Wallet
	chainID string
	height  int64
}

func newRotChain(t *testing.T, chainID string, keys ...string) *rotChain {
	t.Helper()
	registry := codectypes.NewInterfaceRegistry()
	mldsa.RegisterInterfaces(registry)
	w, err := wallet.NewWallet("aetherd", "test", t.TempDir(), codec.NewProtoCodec(registry))
	require.NoError(t, err)
	for _, k := range keys {
		_, _, err := w.CreateAccount(k)
		require.NoError(t, err)
	}
	a := app.New(log.NewNopLogger(), dbm.NewMemDB(), nil, true, nil, t.TempDir(), 0, noAppOptions{}, baseapp.SetChainID(chainID)).(*app.App)
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

func (c *rotChain) ctx() sdk.Context {
	return c.app.NewUncachedContext(false, cmtproto.Header{ChainID: c.chainID, Height: c.height, Time: e2eGenesisTime})
}

func (c *rotChain) block(txs ...[]byte) []*abci.ExecTxResult {
	c.t.Helper()
	c.height++
	resp, err := c.app.FinalizeBlock(&abci.RequestFinalizeBlock{Height: c.height, Time: e2eGenesisTime, Txs: txs})
	require.NoError(c.t, err)
	_, err = c.app.Commit()
	require.NoError(c.t, err)
	return resp.TxResults
}

func (c *rotChain) addr(name string) sdk.AccAddress {
	acc, err := c.w.GetAccount(name)
	require.NoError(c.t, err)
	return sdk.MustAccAddressFromBech32(acc.Address)
}

func (c *rotChain) fund(name string) {
	coins := sdk.NewCoins(sdk.NewInt64Coin("uaeth", 10_000_000))
	require.NoError(c.t, c.app.BankKeeper.MintCoins(c.ctx(), pow.ModuleName, coins))
	require.NoError(c.t, c.app.BankKeeper.SendCoinsFromModuleToAccount(c.ctx(), pow.ModuleName, c.addr(name), coins))
}

// signAs signs msg with keyName's key for account's number and sequence:
// after a rotation the key and the account differ.
func (c *rotChain) signAs(keyName string, account sdk.AccAddress, msg sdk.Msg) []byte {
	c.t.Helper()
	info := c.app.AccountKeeper.GetAccount(c.ctx(), account)
	require.NotNil(c.t, info)
	signed, err := c.w.BuildAndSignMsgTx(keyName, msg, wallet.TxParams{
		ChainID: c.chainID, AccountNumber: info.GetAccountNumber(), Sequence: info.GetSequence(),
		GasLimit: 400_000, Fees: sdk.NewCoins(sdk.NewCoin("uaeth", sdkmath.ZeroInt())),
	})
	require.NoError(c.t, err)
	return signed.Bytes
}

func (c *rotChain) deliver(keyName string, account sdk.AccAddress, msg sdk.Msg) *abci.ExecTxResult {
	c.t.Helper()
	res := c.block(c.signAs(keyName, account, msg))
	require.Len(c.t, res, 1)
	return res[0]
}

func send(from sdk.AccAddress, uaeth int64) sdk.Msg {
	return banktypes.NewMsgSend(from, sdk.AccAddress("rotation_e2e_payee__"), sdk.NewCoins(sdk.NewInt64Coin("uaeth", uaeth)))
}

func hasAttr(events []abci.Event, typ, key string) bool {
	for _, e := range events {
		if e.Type != typ {
			continue
		}
		for _, a := range e.Attributes {
			if a.Key == key {
				return true
			}
		}
	}
	return false
}

// The whole rotation through the real app: the current key schedules
// it, it applies at its height, and from then the new key, and only the
// new key, signs for the account, whose address and balance stay.
func TestKeyRotation_EndToEnd(t *testing.T) {
	c := newRotChain(t, gates.RehearsalPrefix+"rot", "owner", "next", "stranger")
	c.fund("owner")
	owner := c.addr("owner")

	// The owner's first transaction puts its key on the account.
	res := c.deliver("owner", owner, send(owner, 1))
	require.Zero(t, res.Code, res.Log)

	q, err := accountauth.NewQueryServerImpl(c.app.AccountAuthKeeper).Rotation(c.ctx(), &accountauth.QueryRotationRequest{Account: owner.String()})
	require.NoError(t, err)
	require.True(t, q.Active)
	msg, err := c.w.RotateKeyMsg(c.chainID, owner.String(), "next", q.State.GetNonce())
	require.NoError(t, err)
	res = c.deliver("owner", owner, msg)
	require.Zero(t, res.Code, res.Log)
	var scheduled accountauth.MsgRotateKeyResponse
	var txData sdk.TxMsgData
	require.NoError(t, txData.Unmarshal(res.Data))
	require.NoError(t, scheduled.Unmarshal(txData.MsgResponses[0].Value))
	require.Equal(t, c.height+accountauth.RotationDelayBlocks, scheduled.ApplyHeight)

	// Pending: the new key can't sign yet.
	res = c.deliver("next", owner, send(owner, 1))
	require.NotZero(t, res.Code, "not before it applies")

	// The block at its height applies it (EndBlocker, called here at
	// that height rather than finalizing 15,300 blocks).
	ctx := c.ctx().WithBlockHeight(scheduled.ApplyHeight).WithEventManager(sdk.NewEventManager())
	c.app.AccountAuthKeeper.ApplyDueRotations(ctx)
	c.block()
	nextAcc, err := c.w.GetAccount("next")
	require.NoError(t, err)
	require.Equal(t, nextAcc.PubKey, c.app.AccountKeeper.GetAccount(c.ctx(), owner).GetPubKey().Bytes())

	// Simulation (--gas auto) works for the rotated account.
	_, _, err = c.app.Simulate(c.signAs("next", owner, send(owner, 1)))
	require.NoError(t, err)

	before := c.app.BankKeeper.GetBalance(c.ctx(), owner, "uaeth").Amount.Int64()
	res = c.deliver("next", owner, send(owner, 5))
	require.Zero(t, res.Code, res.Log)
	require.True(t, hasAttr(res.Events, sdk.EventTypeTx, sdk.AttributeKeyAccountSequence), "the SDK's own events")
	require.True(t, hasAttr(res.Events, sdk.EventTypeTx, sdk.AttributeKeySignature))
	require.Equal(t, before-5, c.app.BankKeeper.GetBalance(c.ctx(), owner, "uaeth").Amount.Int64())

	res = c.deliver("owner", owner, send(owner, 1))
	require.NotZero(t, res.Code, "the old key no longer signs")
	res = c.deliver("stranger", owner, send(owner, 1))
	require.NotZero(t, res.Code, "nor does an unrelated key")
	require.Contains(t, res.Log, "pubKey does not match signer address")

	// The account's key is public: a transaction that names it but is
	// signed by another key must fail its signature check. (The rotation
	// path turns the address check off; it must not turn off this one.)
	forged := c.signAs("stranger", owner, send(owner, 1))
	var raw txtypes.TxRaw
	require.NoError(t, raw.Unmarshal(forged))
	var auth txtypes.AuthInfo
	require.NoError(t, auth.Unmarshal(raw.AuthInfoBytes))
	nextPub, err := codectypes.NewAnyWithValue(&mldsa.PubKey{Key: nextAcc.PubKey})
	require.NoError(t, err)
	auth.SignerInfos[0].PublicKey = nextPub
	raw.AuthInfoBytes, err = auth.Marshal()
	require.NoError(t, err)
	forged, err = raw.Marshal()
	require.NoError(t, err)
	balance := c.app.BankKeeper.GetBalance(c.ctx(), owner, "uaeth").Amount.Int64()
	results := c.block(forged)
	require.Len(t, results, 1)
	require.NotZero(t, results[0].Code, "a forged signature on the rotated account's key")
	require.Contains(t, results[0].Log, "signature verification failed")
	require.Equal(t, balance, c.app.BankKeeper.GetBalance(c.ctx(), owner, "uaeth").Amount.Int64())

	// An authz grant for a rotation message is refused: a grantee could
	// move the address to its own key. authz's router doesn't have the
	// type (rotationFilteredRouter), as on an older binary.
	exp := e2eGenesisTime.Add(time.Hour)
	grant, err := authz.NewMsgGrant(owner, c.addr("stranger"), authz.NewGenericAuthorization(sdk.MsgTypeURL(&accountauth.MsgRotateKey{})), &exp)
	require.NoError(t, err)
	res = c.deliver("next", owner, grant)
	require.Equal(t, sdkerrors.ErrInvalidType.ABCICode(), res.Code, res.Log)
	require.Contains(t, res.Log, "MsgRotateKey doesn't exist")

	// The wallet, told the new key signs for the account, sends from it.
	require.NoError(t, c.w.SignFor("next", owner.String()))
	acc, err := c.w.GetAccount("next")
	require.NoError(t, err)
	require.Equal(t, owner.String(), acc.Address)
	require.Equal(t, nextAcc.Address, acc.KeyAddress)
}

// Before RotationActivationHeight on the testnet a key whose address
// isn't the signer's is refused as it always was, even one stored on the
// account.
func TestKeyRotation_BeforeTheGate(t *testing.T) {
	c := newRotChain(t, "aether-testnet-rot", "owner", "next")
	c.fund("owner")
	owner := c.addr("owner")
	res := c.deliver("owner", owner, send(owner, 1))
	require.Zero(t, res.Code, res.Log)

	nextAcc, err := c.w.GetAccount("next")
	require.NoError(t, err)
	acc := c.app.AccountKeeper.GetAccount(c.ctx(), owner)
	require.NoError(t, acc.SetPubKey(&mldsa.PubKey{Key: nextAcc.PubKey}))
	c.app.AccountKeeper.SetAccount(c.ctx(), acc)

	res = c.deliver("next", owner, send(owner, 1))
	require.NotZero(t, res.Code)
	require.Contains(t, res.Log, "pubKey does not match signer address")

	msg, err := c.w.RotateKeyMsg(c.chainID, owner.String(), "next", 0)
	require.NoError(t, err)
	res = c.deliver("owner", owner, msg)
	require.NotZero(t, res.Code, "rotation messages are refused before the gate")
}

// newAnteChain copies the SDK's NewAnteHandler decorator list. If the SDK
// moves, read the new x/auth/ante/ante.go, carry its changes over, and
// bump this. (A test binary carries no dependency build info, so this
// reads go.mod.)
func TestAnteChain_SDKVersion(t *testing.T) {
	bz, err := os.ReadFile("../go.mod")
	require.NoError(t, err)
	var version string
	for _, line := range strings.Split(string(bz), "\n") {
		f := strings.Fields(line)
		if len(f) >= 2 && f[0] == "github.com/cosmos/cosmos-sdk" {
			version = f[1]
		}
		if len(f) >= 1 && (f[0] == "replace" || f[0] == "github.com/cosmos/cosmos-sdk") && strings.Contains(line, "=>") {
			require.NotContains(t, line, "github.com/cosmos/cosmos-sdk", "a replaced SDK: check newAnteChain against it")
		}
	}
	require.Equal(t, "v0.50.14", version, "app/rotation_ante.go's newAnteChain copies v0.50.14's NewAnteHandler")
}
