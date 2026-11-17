package app_test

import (
	"testing"
	"time"

	"cosmossdk.io/x/feegrant"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/x/authz"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	"github.com/stretchr/testify/require"

	"github.com/whoyoujoshin/aether/gates"
	"github.com/whoyoujoshin/aether/wallet"
	"github.com/whoyoujoshin/aether/x/accountauth"
)

// rotateOwnerTo rotates owner's key from one wallet key to another and
// applies it, as the EndBlocker would at its height.
func (c *rotChain) rotateOwnerTo(owner sdk.AccAddress, from, to string) {
	c.t.Helper()
	q, err := accountauth.NewQueryServerImpl(c.app.AccountAuthKeeper).Rotation(c.ctx(), &accountauth.QueryRotationRequest{Account: owner.String()})
	require.NoError(c.t, err)
	msg, err := c.w.RotateKeyMsg(c.chainID, owner.String(), to, q.State.GetNonce())
	require.NoError(c.t, err)
	res := c.deliver(from, owner, msg)
	require.Zero(c.t, res.Code, res.Log)
	ctx := c.ctx().WithBlockHeight(c.height + accountauth.RotationDelayBlocks).WithEventManager(sdk.NewEventManager())
	c.app.AccountAuthKeeper.ApplyDueRotations(ctx)
	c.block()
	require.NotZero(c.t, c.deliver(from, owner, send(owner, 1)).Code, "the old key is refused")
}

func (c *rotChain) balance(a sdk.AccAddress) int64 {
	return c.app.BankKeeper.GetBalance(c.ctx(), a, "uaeth").Amount.Int64()
}

func newPlantChain(t *testing.T) (*rotChain, sdk.AccAddress, sdk.AccAddress) {
	c := newRotChain(t, gates.RehearsalPrefix+"keychange", "owner", "next", "thief")
	c.fund("owner")
	c.fund("thief")
	owner, thief := c.addr("owner"), c.addr("thief")
	require.Zero(t, c.deliver("owner", owner, send(owner, 1)).Code)
	require.Zero(t, c.deliver("thief", thief, send(thief, 1)).Code)
	return c, owner, thief
}

// GHSA-cgcr-f6g2-ch5v through the real app: a session key the thief
// registered with the owner's key no longer spends once the owner
// rotates. Before the fix this drained the account.
func TestKeyChange_E2E_PlantedSessionKey(t *testing.T) {
	c, owner, thief := newPlantChain(t)
	thiefAcc, err := c.w.GetAccount("thief")
	require.NoError(t, err)
	reg := &accountauth.MsgRegisterAuthenticator{Account: owner.String(), Authenticator: &accountauth.Authenticator{
		Kind: &accountauth.Authenticator_SessionKey{SessionKey: &accountauth.SessionKey{
			Pubkey: thiefAcc.PubKey, ExpiresAtUnix: e2eGenesisTime.Add(1000 * time.Hour).Unix(),
			AllowedMsgTypes: []string{sdk.MsgTypeURL(&banktypes.MsgSend{})}, SpendLimitUaeth: "1000000000000"}}}}
	res := c.deliver("owner", owner, reg) // signed with the stolen key
	require.Zero(t, res.Code, res.Log)
	var txData sdk.TxMsgData
	require.NoError(t, txData.Unmarshal(res.Data))
	var regResp accountauth.MsgRegisterAuthenticatorResponse
	require.NoError(t, regResp.Unmarshal(txData.MsgResponses[0].Value))

	c.rotateOwnerTo(owner, "owner", "next")

	before := c.balance(owner)
	drain, err := codectypes.NewAnyWithValue(banktypes.NewMsgSend(owner, thief, sdk.NewCoins(sdk.NewInt64Coin("uaeth", before))))
	require.NoError(t, err)
	res = c.deliver("thief", thief, &accountauth.MsgExecAuthenticated{Signer: thief.String(), Account: owner.String(),
		AuthenticatorId: regResp.Id, Msgs: []*codectypes.Any{drain}})
	require.Equal(t, accountauth.ErrDelegationRetired.ABCICode(), res.Code, res.Log)
	require.Equal(t, before, c.balance(owner))
}

// An authz grant made with the stolen key no longer executes after the
// rotation; one the new key makes does.
func TestKeyChange_E2E_PlantedAuthzGrant(t *testing.T) {
	c, owner, thief := newPlantChain(t)
	exp := e2eGenesisTime.Add(1000 * time.Hour)
	grant, err := authz.NewMsgGrant(owner, thief, authz.NewGenericAuthorization(sdk.MsgTypeURL(&banktypes.MsgSend{})), &exp)
	require.NoError(t, err)
	require.Zero(t, c.deliver("owner", owner, grant).Code)

	c.rotateOwnerTo(owner, "owner", "next")

	before := c.balance(owner)
	exec := authz.NewMsgExec(thief, []sdk.Msg{banktypes.NewMsgSend(owner, thief, sdk.NewCoins(sdk.NewInt64Coin("uaeth", before)))})
	res := c.deliver("thief", thief, &exec)
	require.Equal(t, accountauth.ErrDelegationRetired.ABCICode(), res.Code, res.Log)
	require.Equal(t, before, c.balance(owner))

	// The owner, now on "next", grants again: that grant works.
	res = c.deliver("next", owner, grant)
	require.Zero(t, res.Code, res.Log)
	small := authz.NewMsgExec(thief, []sdk.Msg{banktypes.NewMsgSend(owner, thief, sdk.NewCoins(sdk.NewInt64Coin("uaeth", 5)))})
	res = c.deliver("thief", thief, &small)
	require.Zero(t, res.Code, res.Log)
	require.Equal(t, before-5, c.balance(owner))
}

// A fee allowance made with the stolen key pays no fees after the
// rotation.
func TestKeyChange_E2E_PlantedFeeAllowance(t *testing.T) {
	c, owner, thief := newPlantChain(t)
	grant, err := feegrant.NewMsgGrantAllowance(&feegrant.BasicAllowance{}, owner, thief)
	require.NoError(t, err)
	require.Zero(t, c.deliver("owner", owner, grant).Code)

	withFeeFromOwner := func() uint32 {
		info := c.app.AccountKeeper.GetAccount(c.ctx(), thief)
		signed, err := c.w.BuildAndSignMsgTx("thief", send(thief, 1), wallet.TxParams{
			ChainID: c.chainID, AccountNumber: info.GetAccountNumber(), Sequence: info.GetSequence(),
			GasLimit: 400_000, Fees: sdk.NewCoins(sdk.NewInt64Coin("uaeth", 1000)), FeeGranter: owner,
		})
		require.NoError(t, err)
		res := c.block(signed.Bytes)
		require.Len(t, res, 1)
		return res[0].Code
	}
	require.Zero(t, withFeeFromOwner(), "the allowance pays before the change")

	c.rotateOwnerTo(owner, "owner", "next")
	before := c.balance(owner)
	require.Equal(t, accountauth.ErrDelegationRetired.ABCICode(), withFeeFromOwner())
	require.Equal(t, before, c.balance(owner))
}
