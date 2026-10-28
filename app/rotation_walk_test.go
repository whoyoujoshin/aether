package app

import (
	"reflect"
	"testing"
	"time"

	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	dbm "github.com/cosmos/cosmos-db"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	"github.com/cosmos/cosmos-sdk/x/authz"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	"github.com/stretchr/testify/require"
	protov2 "google.golang.org/protobuf/proto"

	"github.com/whoyoujoshin/aether/gates"
	"github.com/whoyoujoshin/aether/x/accountauth"
	"github.com/whoyoujoshin/aether/x/governance"
)

type msgsTx []sdk.Msg

func (t msgsTx) GetMsgs() []sdk.Msg                    { return t }
func (t msgsTx) GetMsgsV2() ([]protov2.Message, error) { return nil, nil }

func TestCheckRotationMsgs(t *testing.T) {
	a := newChainApp(t, dbm.NewMemDB(), "aether-testnet-1")
	owner, other := sdk.AccAddress("rotation_walk_owner_"), sdk.AccAddress("rotation_walk_other_")
	rotate := &accountauth.MsgRotateKey{Account: owner.String()}
	recoverMsg := &accountauth.MsgRecoverKey{Submitter: other.String(), Account: owner.String()}
	bank := banktypes.NewMsgSend(owner, other, sdk.NewCoins(sdk.NewInt64Coin("uaeth", 1)))
	exec := func(msgs ...sdk.Msg) sdk.Msg {
		m := authz.NewMsgExec(other, msgs)
		return &m
	}
	anys := func(msgs ...sdk.Msg) []*codectypes.Any {
		out := make([]*codectypes.Any, len(msgs))
		for i, m := range msgs {
			a, err := codectypes.NewAnyWithValue(m)
			require.NoError(t, err)
			out[i] = a
		}
		return out
	}
	grant := func(typeURL string) sdk.Msg {
		exp := time.Now().Add(time.Hour)
		g, err := authz.NewMsgGrant(owner, other, authz.NewGenericAuthorization(typeURL), &exp)
		require.NoError(t, err)
		return g
	}
	nest := func(depth int, m sdk.Msg) sdk.Msg {
		for i := 0; i < depth; i++ {
			m = exec(m)
		}
		return m
	}
	proposal := func(m sdk.Msg) sdk.Msg {
		return &governance.MsgSubmitParamChangeProposal{Proposer: owner.String(), Deposit: "1", ExecuteMsg: anys(m)[0]}
	}

	cases := []struct {
		name          string
		tx            msgsTx
		before, after error
	}{
		{"a plain send", msgsTx{bank}, nil, nil},
		{"a rotation", msgsTx{rotate}, accountauth.ErrRotationNotActive, nil},
		{"a recovery", msgsTx{bank, recoverMsg}, accountauth.ErrRotationNotActive, nil},
		{"under authz", msgsTx{exec(bank, rotate)}, accountauth.ErrRotationNotActive, accountauth.ErrRotationNotDelegable},
		{"under an authenticator", msgsTx{&accountauth.MsgExecAuthenticated{Signer: other.String(), Account: owner.String(), Msgs: anys(rotate)}}, accountauth.ErrRotationNotActive, accountauth.ErrRotationNotDelegable},
		{"authz under an authenticator", msgsTx{&accountauth.MsgExecAuthenticated{Signer: other.String(), Account: owner.String(), Msgs: anys(exec(recoverMsg))}}, accountauth.ErrRotationNotActive, accountauth.ErrRotationNotDelegable},
		{"in a proposal", msgsTx{proposal(recoverMsg)}, accountauth.ErrRotationNotActive, accountauth.ErrRotationNotDelegable},
		// Grants for rotation messages are authz's to refuse (its router
		// doesn't have them: TestRotationFilteredRouter), not the ante's,
		// so a node on an older binary refuses them the same way.
		{"a grant for it", msgsTx{grant(sdk.MsgTypeURL(rotate))}, nil, nil},
		{"a grant for it under authz", msgsTx{exec(grant(sdk.MsgTypeURL(&accountauth.MsgSetRecovery{})))}, nil, nil},
		{"a watchtower cancel", msgsTx{&accountauth.MsgWatchtowerCancel{Submitter: other.String(), Account: owner.String()}}, accountauth.ErrRotationNotActive, nil},
		{"a watchtower cancel under authz", msgsTx{exec(&accountauth.MsgWatchtowerCancel{Submitter: other.String(), Account: owner.String()})}, accountauth.ErrRotationNotActive, accountauth.ErrRotationNotDelegable},
		{"a grant for a send", msgsTx{grant(sdk.MsgTypeURL(bank))}, nil, nil},
		{"sends under authz", msgsTx{nest(maxRotationWalkDepth, bank)}, nil, nil},
		{"too deep", msgsTx{nest(maxRotationWalkDepth+1, bank)}, nil, sdkerrors.ErrInvalidRequest},
	}
	before := a.NewUncachedContext(false, cmtproto.Header{ChainID: "aether-testnet-1", Height: accountauth.RotationActivationHeight - 1})
	after := before.WithBlockHeight(accountauth.RotationActivationHeight)
	fresh := before.WithChainID(gates.RehearsalPrefix + "walk").WithBlockHeight(1)
	for _, c := range cases {
		for _, run := range []struct {
			ctx  sdk.Context
			want error
		}{{before, c.before}, {after, c.after}, {fresh, c.after}} {
			err := a.checkRotationMsgs(run.ctx, c.tx)
			if run.want == nil {
				require.NoError(t, err, "%s at %s/%d", c.name, run.ctx.ChainID(), run.ctx.BlockHeight())
			} else {
				require.ErrorIs(t, err, run.want, "%s at %s/%d", c.name, run.ctx.ChainID(), run.ctx.BlockHeight())
			}
		}
	}
}

// The keepers that dispatch inner messages get a router without the
// rotation messages: called directly, past every ante check, authz and
// governance still can't reach them, and authz can't grant them.
func TestRotationFilteredRouter(t *testing.T) {
	const chainID = gates.RehearsalPrefix + "router"
	a := newChainApp(t, dbm.NewMemDB(), chainID)
	genesisOn(t, a, chainID)
	ctx := a.NewUncachedContext(false, cmtproto.Header{ChainID: chainID, Height: 2, Time: testGenesisTime})
	owner, other := sdk.AccAddress("router_test_owner___"), sdk.AccAddress("router_test_other___")

	r := rotationFilteredRouter{a.MsgServiceRouter()}
	for _, m := range []sdk.Msg{&accountauth.MsgRotateKey{}, &accountauth.MsgCancelPendingRotation{}, &accountauth.MsgSetRecovery{},
		&accountauth.MsgRecoverKey{}, &accountauth.MsgGuardianCancel{}, &accountauth.MsgGuardianSetRecovery{}, &accountauth.MsgWatchtowerCancel{}} {
		require.NotNil(t, a.MsgServiceRouter().Handler(m), "the top level still routes %T", m)
		require.Nil(t, r.Handler(m))
		require.Nil(t, r.HandlerByTypeURL(sdk.MsgTypeURL(m)))
	}
	require.NotNil(t, r.Handler(&banktypes.MsgSend{}))

	// authz: executing one, and granting one.
	_, err := a.AuthzKeeper.DispatchActions(ctx, owner, []sdk.Msg{&accountauth.MsgRotateKey{Account: owner.String()}})
	require.ErrorContains(t, err, "unrecognized message route")
	exp := testGenesisTime.Add(time.Hour)
	_, err = a.AuthzKeeper.Grant(ctx, mustGrant(t, owner, other, sdk.MsgTypeURL(&accountauth.MsgRotateKey{}), exp))
	require.ErrorContains(t, err, "doesn't exist")

	// governance: a proposal can't name one.
	gov := authtypes.NewModuleAddress(governance.ModuleName)
	execMsg, err := codectypes.NewAnyWithValue(&accountauth.MsgRecoverKey{Submitter: gov.String(), Account: owner.String()})
	require.NoError(t, err)
	_, err = governance.NewMsgServerImpl(a.GovernanceKeeper).SubmitParamChangeProposal(ctx, &governance.MsgSubmitParamChangeProposal{
		Proposer: owner.String(), Deposit: "0", ExecuteMsg: execMsg})
	require.ErrorIs(t, err, governance.ErrUnroutableProposalMsg)
}

func mustGrant(t *testing.T, granter, grantee sdk.AccAddress, typeURL string, exp time.Time) *authz.MsgGrant {
	g, err := authz.NewMsgGrant(granter, grantee, authz.NewGenericAuthorization(typeURL), &exp)
	require.NoError(t, err)
	return g
}

// The interchain-accounts keepers, and x/accountauth's (whose
// ExecAuthenticated also refuses rotation messages itself), hold the
// filtered router too. Driving a
// real ICA packet needs a channel and a registered account, so this reads
// the router they were given.
func TestRotationFilteredRouter_ICA(t *testing.T) {
	const chainID = gates.RehearsalPrefix + "router"
	a := newChainApp(t, dbm.NewMemDB(), chainID)
	for name, k := range map[string]struct {
		keeper any
		field  string
	}{"ica host": {a.ICAHostKeeper, "msgRouter"}, "ica controller": {a.ICAControllerKeeper, "msgRouter"}, "accountauth": {a.AccountAuthKeeper, "router"}} {
		keeper := k.keeper
		f := reflect.ValueOf(keeper).FieldByName(k.field)
		require.True(t, f.IsValid(), "%s keeper has no msgRouter field: check ibc-go's struct", name)
		require.Equal(t, reflect.TypeOf(rotationFilteredRouter{}), f.Elem().Type(), name)
	}
}
