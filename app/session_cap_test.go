package app_test

import (
	"sort"
	"testing"
	"time"

	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/x/authz"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	"github.com/stretchr/testify/require"

	"github.com/whoyoujoshin/aether/app"
	"github.com/whoyoujoshin/aether/gates"
	"github.com/whoyoujoshin/aether/x/accountauth"
	"github.com/whoyoujoshin/aether/x/escrow"
	"github.com/whoyoujoshin/aether/x/pow"
)

// GHSA-j2qr-v7fr-w36f: a session key's spend limit counted only the uaeth
// in a MsgSend's amount. These run the reported bypasses through the real
// app on a fresh chain, where the fix is on from block 1.

const sessCap = 100 // uaeth

type sessFixture struct {
	c           *rotChain
	owner, sess sdk.AccAddress
	id          uint64
}

func newSessFixture(t *testing.T, chainID string, allowed ...sdk.Msg) *sessFixture {
	t.Helper()
	c := newRotChain(t, chainID, "owner", "sess")
	c.fund("owner")
	c.fund("sess")
	owner, sess := c.addr("owner"), c.addr("sess")
	usdc := sdk.NewCoins(sdk.NewInt64Coin("ibc/USDC", 1_000_000_000))
	require.NoError(t, c.app.BankKeeper.MintCoins(c.ctx(), pow.ModuleName, usdc))
	require.NoError(t, c.app.BankKeeper.SendCoinsFromModuleToAccount(c.ctx(), pow.ModuleName, owner, usdc))
	require.Zero(t, c.deliver("owner", owner, send(owner, 1)).Code)
	require.Zero(t, c.deliver("sess", sess, send(sess, 1)).Code)
	f := &sessFixture{c: c, owner: owner, sess: sess}
	res := c.deliver("owner", owner, f.registration(allowed...))
	require.Zero(t, res.Code, res.Log)
	f.id = c.app.AccountAuthKeeper.GetAuthenticators(c.ctx(), owner)[0].Id
	return f
}

func (f *sessFixture) registration(allowed ...sdk.Msg) *accountauth.MsgRegisterAuthenticator {
	sk, err := f.c.w.GetAccount("sess")
	require.NoError(f.c.t, err)
	var types []string
	for _, m := range allowed {
		types = append(types, sdk.MsgTypeURL(m))
	}
	return &accountauth.MsgRegisterAuthenticator{Account: f.owner.String(), Authenticator: &accountauth.Authenticator{
		Kind: &accountauth.Authenticator_SessionKey{SessionKey: &accountauth.SessionKey{Pubkey: sk.PubKey, ExpiresAtUnix: e2eGenesisTime.Add(time.Hour).Unix(),
			AllowedMsgTypes: types, SpendLimitUaeth: "100"}}}}
}

func (f *sessFixture) exec(msgs ...sdk.Msg) (uint32, string) {
	f.c.t.Helper()
	anys := make([]*codectypes.Any, len(msgs))
	for i, m := range msgs {
		a, err := codectypes.NewAnyWithValue(m)
		require.NoError(f.c.t, err)
		anys[i] = a
	}
	res := f.c.deliver("sess", f.sess, &accountauth.MsgExecAuthenticated{Signer: f.sess.String(), Account: f.owner.String(), AuthenticatorId: f.id, Msgs: anys})
	return res.Code, res.Log
}

func (f *sessFixture) balance(denom string) int64 {
	return f.c.app.BankKeeper.GetBalance(f.c.ctx(), f.owner, denom).Amount.Int64()
}

func (f *sessFixture) spent() string {
	a, ok := f.c.app.AccountAuthKeeper.GetAuthenticator(f.c.ctx(), f.owner, f.id)
	require.True(f.c.t, ok)
	return a.GetSessionKey().SpentUaeth
}

func requireRefused(t *testing.T, code uint32, log string, want uint32) {
	t.Helper()
	require.Equal(t, want, code, log)
}

// The consensus-key ownership rule rides the same validator swap.
func TestSessionKeyCap_SameHeightAsConsensusKeyOwnership(t *testing.T) {
	require.Equal(t, accountauth.SessionKeyCapHeight, pow.ConsensusKeyOwnershipHeight)
}

func TestSessionKeyCap_MultiSendIsCounted(t *testing.T) {
	f := newSessFixture(t, gates.RehearsalPrefix+"sess", &banktypes.MsgSend{}, &banktypes.MsgMultiSend{})
	multi := func(n int64) sdk.Msg {
		coins := sdk.NewCoins(sdk.NewInt64Coin("uaeth", n))
		return &banktypes.MsgMultiSend{Inputs: []banktypes.Input{{Address: f.owner.String(), Coins: coins}}, Outputs: []banktypes.Output{{Address: f.sess.String(), Coins: coins}}}
	}
	before := f.balance("uaeth")
	code, log := f.exec(multi(5_000_000))
	requireRefused(t, code, log, accountauth.ErrSpendLimitExceeded.ABCICode())
	require.Equal(t, before, f.balance("uaeth"), "nothing left the account")

	code, log = f.exec(multi(60))
	require.Zero(t, code, log)
	require.Equal(t, "60", f.spent())
	code, log = f.exec(send(f.owner, 41)) // 60 + 41 > 100
	requireRefused(t, code, log, accountauth.ErrSpendLimitExceeded.ABCICode())
	code, log = f.exec(send(f.owner, 40))
	require.Zero(t, code, log)
	require.Equal(t, "100", f.spent())
	require.Equal(t, before-100, f.balance("uaeth"))
}

func TestSessionKeyCap_OtherDenomsCantBeSpent(t *testing.T) {
	f := newSessFixture(t, gates.RehearsalPrefix+"sess", &banktypes.MsgSend{})
	usdc := f.balance("ibc/USDC")
	code, log := f.exec(banktypes.NewMsgSend(f.owner, f.sess, sdk.NewCoins(sdk.NewInt64Coin("ibc/USDC", usdc))))
	requireRefused(t, code, log, accountauth.ErrSpendLimitExceeded.ABCICode())
	require.Equal(t, usdc, f.balance("ibc/USDC"))
}

func TestSessionKeyCap_EscrowIsCounted(t *testing.T) {
	f := newSessFixture(t, gates.RehearsalPrefix+"sess", &banktypes.MsgSend{}, &escrow.MsgCreateEscrow{})
	lock := &escrow.MsgCreateEscrow{Payer: f.owner.String(), Payee: f.sess.String(), Amount: sdk.NewCoins(sdk.NewInt64Coin("uaeth", 80)),
		ExpiresAt: e2eGenesisTime.Add(time.Hour).Unix(), OnExpiry: escrow.ON_EXPIRY_REFUND}
	code, log := f.exec(lock)
	require.Zero(t, code, log)
	require.Equal(t, "80", f.spent(), "locking funds in escrow spends them")

	// One exec that goes over reverts as a whole: the first send doesn't land.
	before := f.balance("uaeth")
	code, log = f.exec(send(f.owner, 10), send(f.owner, 11))
	requireRefused(t, code, log, accountauth.ErrSpendLimitExceeded.ABCICode())
	require.Equal(t, before, f.balance("uaeth"))
	require.Equal(t, "80", f.spent())
}

// The two delegation bypasses: a session key can't be registered allowing
// what hands the account's authority on, and one registered before the
// height (planted here straight in the store) can't run it.
func TestSessionKeyCap_NoDelegation(t *testing.T) {
	for _, never := range []sdk.Msg{&authz.MsgGrant{}, &accountauth.MsgRegisterAuthenticator{}, &escrow.MsgReleaseEscrow{}, &pow.MsgRegisterValidatorPubkey{}} {
		f := newSessFixture(t, gates.RehearsalPrefix+"sess", &banktypes.MsgSend{})
		res := f.c.deliver("owner", f.owner, f.registration(&banktypes.MsgSend{}, never))
		require.Equal(t, accountauth.ErrNotSessionDelegable.ABCICode(), res.Code, "%s: %s", sdk.MsgTypeURL(never), res.Log)
	}

	f := newSessFixture(t, gates.RehearsalPrefix+"sess", &banktypes.MsgSend{})
	a, _ := f.c.app.AccountAuthKeeper.GetAuthenticator(f.c.ctx(), f.owner, f.id)
	sk := a.GetSessionKey()
	sk.AllowedMsgTypes = append(sk.AllowedMsgTypes, sdk.MsgTypeURL(&authz.MsgGrant{}), sdk.MsgTypeURL(&accountauth.MsgRegisterAuthenticator{}))
	f.c.app.AccountAuthKeeper.SetAuthenticator(f.c.ctx(), f.owner, a)

	exp := e2eGenesisTime.Add(time.Hour)
	grant, err := authz.NewMsgGrant(f.owner, f.sess, authz.NewGenericAuthorization(sdk.MsgTypeURL(&banktypes.MsgSend{})), &exp)
	require.NoError(t, err)
	code, log := f.exec(grant)
	requireRefused(t, code, log, accountauth.ErrNotSessionDelegable.ABCICode())

	bigger := f.registration(&banktypes.MsgSend{})
	bigger.Authenticator.GetSessionKey().SpendLimitUaeth = "1000000000000"
	code, log = f.exec(bigger)
	requireRefused(t, code, log, accountauth.ErrNotSessionDelegable.ABCICode())
	require.Len(t, f.c.app.AccountAuthKeeper.GetAuthenticators(f.c.ctx(), f.owner), 1)
}

// Every message type the app routes is either one a session key never
// runs or one whose cost to the account shows in its balance, so a new
// message type has to be placed before this passes.
func TestSessionKeyCap_EveryMessageClassified(t *testing.T) {
	metered := map[string]bool{
		// Spending shows in the account's balance.
		"/cosmos.bank.v1beta1.MsgSend": true, "/cosmos.bank.v1beta1.MsgMultiSend": true,
		"/ibc.applications.transfer.v1.MsgTransfer": true,
		"/aether.escrow.v1.MsgCreateEscrow":         true,
		"/aether.governance.v1.MsgDeposit":          true, "/aether.governance.v1.MsgSubmitProposal": true,
		"/aether.governance.v1.MsgSubmitParamChangeProposal": true,
		// Move nothing out of the account, or only take authority away.
		"/aether.escrow.v1.MsgRefundEscrow": true, "/aether.governance.v1.MsgVote": true,
		"/aether.pow.v1.MsgSubmitPoW":     true,
		"/cosmos.authz.v1beta1.MsgRevoke": true, "/cosmos.feegrant.v1beta1.MsgRevokeAllowance": true,
		"/cosmos.feegrant.v1beta1.MsgPruneAllowances":                      true,
		"/ibc.applications.interchain_accounts.host.v1.MsgModuleQuerySafe": true,
	}
	var unplaced []string
	for _, u := range app.MakeEncodingConfig().InterfaceRegistry.ListImplementations("cosmos.base.v1beta1.Msg") {
		if _, never := accountauth.SessionNeverDelegable(u); never || metered[u] || authorityOnly(u) || ibcRelay(u) {
			continue
		}
		unplaced = append(unplaced, u)
	}
	sort.Strings(unplaced)
	require.Empty(t, unplaced, "place each in sessionNeverDelegable (x/accountauth/session_cap.go) or in this test's metered list")
}

// authorityOnly is a message only a module authority (governance) signs.
func authorityOnly(u string) bool {
	for _, s := range []string{"MsgUpdateParams", "MsgSetSendEnabled", "MsgIBCSoftwareUpgrade", "MsgRecoverClient"} {
		if len(u) >= len(s) && u[len(u)-len(s):] == s {
			return true
		}
	}
	return false
}

// ibcRelay is a core IBC message: relaying moves nothing out of the
// relayer's account.
func ibcRelay(u string) bool {
	for _, p := range []string{"/ibc.core.channel.", "/ibc.core.client.", "/ibc.core.connection."} {
		if len(u) >= len(p) && u[:len(p)] == p {
			return true
		}
	}
	return false
}
