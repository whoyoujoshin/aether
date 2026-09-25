package app_test

import (
	"encoding/json"
	"testing"
	"time"

	"cosmossdk.io/log"
	sdkmath "cosmossdk.io/math"
	"cosmossdk.io/x/feegrant"
	abci "github.com/cometbft/cometbft/abci/types"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	dbm "github.com/cosmos/cosmos-db"
	"github.com/cosmos/cosmos-sdk/baseapp"
	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	simtestutil "github.com/cosmos/cosmos-sdk/testutil/sims"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/x/authz"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	"github.com/stretchr/testify/require"

	"github.com/whoyoujoshin/aether/app"
	"github.com/whoyoujoshin/aether/crypto/mldsa"
	"github.com/whoyoujoshin/aether/wallet"
	"github.com/whoyoujoshin/aether/x/pow"
)

const e2eChainID = "agent-grant-e2e"

var e2eGenesisTime = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

type noAppOptions struct{}

func (noAppOptions) Get(string) interface{} { return nil }

type e2eChain struct {
	t      *testing.T
	app    *app.App
	w      *wallet.Wallet
	height int64
}

func (c *e2eChain) ctx() sdk.Context {
	return c.app.NewUncachedContext(false, cmtproto.Header{Height: c.height, Time: c.blockTime(c.height)})
}

func (c *e2eChain) blockTime(h int64) time.Time {
	return e2eGenesisTime.Add(time.Duration(h) * time.Minute)
}

// deliver signs msg as signer, executes it in the next block through
// the real ante handler and message router, and commits.
func (c *e2eChain) deliver(signer string, msg sdk.Msg, feeGranter sdk.AccAddress) *abci.ExecTxResult {
	c.t.Helper()
	acc, err := c.w.GetAccount(signer)
	require.NoError(c.t, err)
	info := c.app.AccountKeeper.GetAccount(c.ctx(), sdk.MustAccAddressFromBech32(acc.Address))
	require.NotNil(c.t, info, "%s has no on-chain account", signer)

	signed, err := c.w.BuildAndSignMsgTx(signer, msg, wallet.TxParams{
		ChainID:       e2eChainID,
		AccountNumber: info.GetAccountNumber(),
		Sequence:      info.GetSequence(),
		GasLimit:      400_000,
		Fees:          sdk.NewCoins(sdk.NewCoin("uaeth", sdkmath.ZeroInt())),
		FeeGranter:    feeGranter,
	})
	require.NoError(c.t, err)

	c.height++
	resp, err := c.app.FinalizeBlock(&abci.RequestFinalizeBlock{
		Height: c.height,
		Time:   c.blockTime(c.height),
		Txs:    [][]byte{signed.Bytes},
	})
	require.NoError(c.t, err)
	_, err = c.app.Commit()
	require.NoError(c.t, err)
	require.Len(c.t, resp.TxResults, 1)
	return resp.TxResults[0]
}

func (c *e2eChain) balance(addr string) int64 {
	return c.app.BankKeeper.GetBalance(c.ctx(), sdk.MustAccAddressFromBech32(addr), "uaeth").Amount.Int64()
}

// The agent flow agentmcp's grant mode relies on, through the real
// app: ML-DSA signatures, ante handler, x/authz and x/feegrant. The
// agent account never holds a single uaeth.
func TestAgentPaysUnderGrant_EndToEnd(t *testing.T) {
	defer app.SetAuthzFeegrantActivationHeight(0)()

	registry := codectypes.NewInterfaceRegistry()
	mldsa.RegisterInterfaces(registry)
	w, err := wallet.NewWallet("aetherd", "test", t.TempDir(), codec.NewProtoCodec(registry))
	require.NoError(t, err)
	human, _, err := w.CreateAccount("human")
	require.NoError(t, err)
	agent, _, err := w.CreateAccount("agent")
	require.NoError(t, err)
	shop := sdk.AccAddress("some_shop_recipient_").String()

	a := app.New(log.NewNopLogger(), dbm.NewMemDB(), nil, true, nil, t.TempDir(), 0, noAppOptions{}, baseapp.SetChainID(e2eChainID)).(*app.App)
	genesis, err := json.Marshal(app.ModuleBasics.DefaultGenesis(app.MakeEncodingConfig().Codec))
	require.NoError(t, err)
	_, err = a.InitChain(&abci.RequestInitChain{
		ChainId: e2eChainID, Time: e2eGenesisTime, InitialHeight: 1,
		AppStateBytes: genesis, ConsensusParams: simtestutil.DefaultConsensusParams,
	})
	require.NoError(t, err)
	c := &e2eChain{t: t, app: a, w: w}
	c.height = 1
	_, err = a.FinalizeBlock(&abci.RequestFinalizeBlock{Height: 1, Time: c.blockTime(1)})
	require.NoError(t, err)
	_, err = a.Commit()
	require.NoError(t, err)

	// Fund the human only.
	ten := sdk.NewCoins(sdk.NewInt64Coin("uaeth", 10_000_000))
	require.NoError(t, a.BankKeeper.MintCoins(c.ctx(), pow.ModuleName, ten))
	require.NoError(t, a.BankKeeper.SendCoinsFromModuleToAccount(c.ctx(), pow.ModuleName, sdk.MustAccAddressFromBech32(human.Address), ten))
	humanAddr, agentAddr := sdk.MustAccAddressFromBech32(human.Address), sdk.MustAccAddressFromBech32(agent.Address)

	// 1. The human grants the agent: at most 3 AETH, for a day.
	expires := c.blockTime(1).Add(24 * time.Hour)
	grant, err := authz.NewMsgGrant(humanAddr, agentAddr, banktypes.NewSendAuthorization(sdk.NewCoins(sdk.NewInt64Coin("uaeth", 3_000_000)), nil), &expires)
	require.NoError(t, err)
	res := c.deliver("human", grant, nil)
	require.Zero(t, res.Code, res.Log)

	// 2. ...and covers its fees. This also creates the agent's account.
	allowance, err := feegrant.NewMsgGrantAllowance(&feegrant.BasicAllowance{Expiration: &expires}, humanAddr, agentAddr)
	require.NoError(t, err)
	res = c.deliver("human", allowance, nil)
	require.Zero(t, res.Code, res.Log)
	require.Zero(t, c.balance(agent.Address), "the agent holds nothing")

	pay := func(uaeth int64) *abci.ExecTxResult {
		send := banktypes.NewMsgSend(humanAddr, sdk.MustAccAddressFromBech32(shop), sdk.NewCoins(sdk.NewInt64Coin("uaeth", uaeth)))
		exec := authz.NewMsgExec(agentAddr, []sdk.Msg{send})
		return c.deliver("agent", &exec, humanAddr)
	}

	// 3. The agent pays 2 AETH of the human's money.
	res = pay(2_000_000)
	require.Zero(t, res.Code, res.Log)
	require.Equal(t, int64(2_000_000), c.balance(shop))
	require.Equal(t, int64(8_000_000), c.balance(human.Address))

	// 4. Another 2 AETH would exceed the 1 AETH left: the chain refuses,
	// with the code and log agentmcp maps to GRANT_LIMIT_EXCEEDED.
	res = pay(2_000_000)
	require.Equal(t, "sdk", res.Codespace)
	require.Equal(t, uint32(5), res.Code)
	require.Contains(t, res.Log, "spend limit")
	require.Equal(t, int64(2_000_000), c.balance(shop), "nothing moved")

	// The wallet's grant query sees what's left.
	q, err := a.AuthzKeeper.Grants(c.ctx(), &authz.QueryGrantsRequest{
		Granter: human.Address, Grantee: agent.Address, MsgTypeUrl: sdk.MsgTypeURL(&banktypes.MsgSend{}),
	})
	require.NoError(t, err)
	var left banktypes.SendAuthorization
	require.NoError(t, left.Unmarshal(q.Grants[0].Authorization.Value))
	require.Equal(t, "1000000uaeth", left.SpendLimit.String())

	// 5. The human revokes; the agent can no longer spend at all.
	revoke := authz.NewMsgRevoke(humanAddr, agentAddr, sdk.MsgTypeURL(&banktypes.MsgSend{}))
	res = c.deliver("human", &revoke, nil)
	require.Zero(t, res.Code, res.Log)
	res = pay(1)
	require.NotZero(t, res.Code)
	require.Equal(t, "authz", res.Codespace, res.Log)
	require.Equal(t, uint32(2), res.Code, "authz ErrNoAuthorizationFound, mapped to GRANT_NOT_FOUND")

	// wallet.GetSendGrant recognizes the query's not-found error by
	// this text when it arrives over gRPC.
	_, err = a.AuthzKeeper.Grants(c.ctx(), &authz.QueryGrantsRequest{
		Granter: human.Address, Grantee: agent.Address, MsgTypeUrl: sdk.MsgTypeURL(&banktypes.MsgSend{}),
	})
	require.ErrorContains(t, err, authz.ErrNoAuthorizationFound.Error())
}
