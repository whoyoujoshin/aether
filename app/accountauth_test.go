package app

import (
	"testing"
	"time"

	sdkmath "cosmossdk.io/math"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	dbm "github.com/cosmos/cosmos-db"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	"github.com/stretchr/testify/require"

	"github.com/whoyoujoshin/aether/crypto/mldsa"
	"github.com/whoyoujoshin/aether/x/accountauth"
	"github.com/whoyoujoshin/aether/x/pow"
)

func TestPlanAccountAuth(t *testing.T) {
	const activation = 100
	cases := []struct {
		lastCommitted int64
		want          accountAuthPlan
	}{
		{0, accountAuthPlan{}},
		{98, accountAuthPlan{}},
		{99, accountAuthPlan{wire: true, addStores: true}},
		{100, accountAuthPlan{wire: true}},
		{5000, accountAuthPlan{wire: true}},
	}
	for _, c := range cases {
		require.Equal(t, c.want, planAccountAuth(c.lastCommitted, activation), "last committed height %d", c.lastCommitted)
	}

	// A chain launched with the feature on from genesis: fresh DB,
	// nothing to upgrade.
	require.Equal(t, accountAuthPlan{wire: true}, planAccountAuth(0, 0))
	require.Equal(t, accountAuthPlan{wire: true, addStores: true}, planAccountAuth(0, 1))
}

// Drives a real multistore across the activation boundary, exactly like
// TestAuthzFeegrant_StoresAddedExactlyAtActivationHeight and
// TestIBC_StoresAddedExactlyAtActivationHeight: the three restarts a
// live node actually goes through (before, at, and after the one-time
// store upgrade), then a real end-to-end session-key exec dispatching a
// real bank send through the app's own MsgServiceRouter.
func TestAccountAuth_StoresAddedExactlyAtActivationHeight(t *testing.T) {
	const activation = 3
	defer func(orig int64) { accountAuthActivationHeight = orig }(accountAuthActivationHeight)
	accountAuthActivationHeight = activation

	const msgRegisterURL = "/aether.accountauth.v1.MsgRegisterAuthenticator"
	db := dbm.NewMemDB()

	// Before activation: no accountauth store, and the node's registry
	// can't even decode MsgRegisterAuthenticator, same as the previous
	// binary.
	before := newTestApp(t, db)
	require.False(t, before.accountAuthWired)
	_, err := before.interfaceRegistry.Resolve(msgRegisterURL)
	require.Error(t, err, "pre-activation node must not know accountauth message types")

	initTestChain(t, before)
	finalizeAndCommit(t, before, 1)
	finalizeAndCommit(t, before, 2)
	for _, name := range accountAuthStoreKeys {
		require.False(t, committedStoreNames(t, before, 2)[name], "%s must not be in the AppHash before activation", name)
	}

	// Reaching the activation block unwired halts instead of executing
	// it without the store.
	require.ErrorContains(t, finalizeBlock(before, activation), "restart this node")

	// Restart at last committed height activation-1: the store is added
	// via StoreUpgrades and the activation block executes.
	atActivation := newTestApp(t, db)
	require.True(t, atActivation.accountAuthWired)
	_, err = atActivation.interfaceRegistry.Resolve(msgRegisterURL)
	require.NoError(t, err)
	finalizeAndCommit(t, atActivation, activation)
	for _, name := range accountAuthStoreKeys {
		require.True(t, committedStoreNames(t, atActivation, activation)[name], "%s must be in the AppHash from activation on", name)
	}

	// Any later restart loads the store normally -- no repeat upgrade,
	// no "version of store mismatch" error.
	after := newTestApp(t, db)
	require.True(t, after.accountAuthWired)
	finalizeAndCommit(t, after, activation+1)

	// The point of all this: a registered session key can move real
	// funds on the account's behalf, dispatched through the app's own
	// MsgServiceRouter -- not a fake one, exactly like the authz test's
	// real SendAuthorization.Accept exercise.
	ctx := after.NewUncachedContext(false, cmtproto.Header{Height: activation + 2, Time: testGenesisTime.Add(time.Hour)})

	human := sdk.AccAddress("human_principal_____")
	recipient := sdk.AccAddress("some_recipient______")
	funding := sdk.NewCoins(sdk.NewCoin("uaeth", sdkmath.NewInt(1_000_000)))
	require.NoError(t, after.BankKeeper.MintCoins(ctx, pow.ModuleName, funding))
	require.NoError(t, after.BankKeeper.SendCoinsFromModuleToAccount(ctx, pow.ModuleName, human, funding))

	priv, err := mldsa.GenPrivKey()
	require.NoError(t, err)
	pub := priv.PubKey().(*mldsa.PubKey)
	sessionAddr := sdk.AccAddress(pub.Address())

	ms := accountauth.NewMsgServerImpl(after.AccountAuthKeeper)
	reg, err := ms.RegisterAuthenticator(sdk.WrapSDKContext(ctx), &accountauth.MsgRegisterAuthenticator{
		Account: human.String(),
		Authenticator: &accountauth.Authenticator{Kind: &accountauth.Authenticator_SessionKey{SessionKey: &accountauth.SessionKey{
			Pubkey:          pub.Bytes(),
			ExpiresAtUnix:   ctx.BlockTime().Add(time.Hour).Unix(),
			AllowedMsgTypes: []string{sdk.MsgTypeURL(&banktypes.MsgSend{})},
			SpendLimitUaeth: "500000",
		}}},
	})
	require.NoError(t, err)

	send := banktypes.NewMsgSend(human, recipient, sdk.NewCoins(sdk.NewCoin("uaeth", sdkmath.NewInt(300_000))))
	any, err := codectypes.NewAnyWithValue(send)
	require.NoError(t, err)

	_, err = ms.ExecAuthenticated(sdk.WrapSDKContext(ctx), &accountauth.MsgExecAuthenticated{
		Signer:          sessionAddr.String(),
		Account:         human.String(),
		AuthenticatorId: reg.Id,
		Msgs:            []*codectypes.Any{any},
	})
	require.NoError(t, err)

	recipientBalance := after.BankKeeper.GetBalance(ctx, recipient, "uaeth")
	require.Equal(t, sdkmath.NewInt(300_000), recipientBalance.Amount, "the session key's exec must have really moved bank funds")

	humanBalance := after.BankKeeper.GetBalance(ctx, human, "uaeth")
	require.Equal(t, sdkmath.NewInt(700_000), humanBalance.Amount)

	// A second exec past the remaining spend limit is rejected by the
	// chain itself, not merely by an off-chain check.
	over := banktypes.NewMsgSend(human, recipient, sdk.NewCoins(sdk.NewCoin("uaeth", sdkmath.NewInt(300_000))))
	overAny, err := codectypes.NewAnyWithValue(over)
	require.NoError(t, err)
	_, err = ms.ExecAuthenticated(sdk.WrapSDKContext(ctx), &accountauth.MsgExecAuthenticated{
		Signer:          sessionAddr.String(),
		Account:         human.String(),
		AuthenticatorId: reg.Id,
		Msgs:            []*codectypes.Any{overAny},
	})
	require.Error(t, err, "the chain itself must reject spending past the session key's remaining limit")
}
