package app

import (
	"testing"
	"time"

	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	dbm "github.com/cosmos/cosmos-db"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"
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
		{"a grant for it", msgsTx{grant(sdk.MsgTypeURL(rotate))}, accountauth.ErrRotationNotDelegable, accountauth.ErrRotationNotDelegable},
		{"a grant for it under authz", msgsTx{exec(grant(sdk.MsgTypeURL(&accountauth.MsgSetRecovery{})))}, accountauth.ErrRotationNotDelegable, accountauth.ErrRotationNotDelegable},
		{"a watchtower cancel", msgsTx{&accountauth.MsgWatchtowerCancel{Submitter: other.String(), Account: owner.String()}}, accountauth.ErrRotationNotActive, nil},
		{"a watchtower cancel under authz", msgsTx{exec(&accountauth.MsgWatchtowerCancel{Submitter: other.String(), Account: owner.String()})}, accountauth.ErrRotationNotActive, accountauth.ErrRotationNotDelegable},
		{"a grant for a guardian set change", msgsTx{grant(sdk.MsgTypeURL(&accountauth.MsgGuardianSetRecovery{}))}, accountauth.ErrRotationNotDelegable, accountauth.ErrRotationNotDelegable},
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
