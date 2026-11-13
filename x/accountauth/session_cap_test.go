package accountauth

import (
	"context"
	"testing"
	"time"

	sdkmath "cosmossdk.io/math"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	"github.com/stretchr/testify/require"
)

// fakeBank is an account's balances, which fakeSpendRouter's MsgSend and
// MsgMultiSend lower as the real bank would.
type fakeBank struct{ coins map[string]sdk.Coins }

func (b *fakeBank) GetAllBalances(_ context.Context, addr sdk.AccAddress) sdk.Coins {
	return b.coins[addr.String()]
}

func (b *fakeBank) debit(addr string, c sdk.Coins) { b.coins[addr] = b.coins[addr].Sub(c...) }

type fakeSpendRouter struct{ bank *fakeBank }

func (r fakeSpendRouter) Handler(msg sdk.Msg) func(sdk.Context, sdk.Msg) (*sdk.Result, error) {
	return func(_ sdk.Context, m sdk.Msg) (*sdk.Result, error) {
		switch m := m.(type) {
		case *banktypes.MsgSend:
			r.bank.debit(m.FromAddress, m.Amount)
		case *banktypes.MsgMultiSend:
			r.bank.debit(m.Inputs[0].Address, m.Inputs[0].Coins)
		}
		return &sdk.Result{}, nil
	}
}

func (r fakeSpendRouter) HandlerByTypeURL(string) func(sdk.Context, sdk.Msg) (*sdk.Result, error) {
	return nil
}

// Below SessionKeyCapHeight the old rule holds exactly (only a MsgSend's
// uaeth counts, a never-delegable type can be registered); from it, the
// fall in the account's balance counts, whatever the message.
func TestSessionKeyCap_SwitchesAtTheHeight(t *testing.T) {
	_, keeper, ctx := setupMsgServer(t)
	bank := &fakeBank{coins: map[string]sdk.Coins{}}
	keeper.router = fakeSpendRouter{bank: bank}
	keeper = keeper.WithBankKeeper(bank)
	ms := NewMsgServerImpl(keeper)
	account := sdk.AccAddress("account_____________")
	bank.coins[account.String()] = sdk.NewCoins(sdk.NewInt64Coin("uaeth", 1_000_000))
	_, pub, signer := genSessionKey(t)
	allowed := []string{sdk.MsgTypeURL(&banktypes.MsgSend{}), sdk.MsgTypeURL(&banktypes.MsgMultiSend{})}
	multi := func(n int64) *codectypes.Any {
		c := sdk.NewCoins(sdk.NewInt64Coin("uaeth", n))
		return anyOf(t, &banktypes.MsgMultiSend{Inputs: []banktypes.Input{{Address: account.String(), Coins: c}}, Outputs: []banktypes.Output{{Address: signer.String(), Coins: c}}})
	}
	exec := func(ctx sdk.Context, id uint64, m *codectypes.Any) error {
		_, err := ms.ExecAuthenticated(ctx, &MsgExecAuthenticated{Signer: signer.String(), Account: account.String(), AuthenticatorId: id, Msgs: []*codectypes.Any{m}})
		return err
	}

	before := ctx.WithBlockHeight(SessionKeyCapHeight - 1)
	id := registerSessionKey(t, ms, before, account, pub, allowed, "100", time.Hour)
	require.NoError(t, exec(before, id, multi(5_000)), "the old rule doesn't count MultiSend")
	registerSessionKey(t, ms, before, account, pub, []string{"/cosmos.authz.v1beta1.MsgGrant"}, "100", time.Hour)

	at := ctx.WithBlockHeight(SessionKeyCapHeight)
	require.ErrorIs(t, exec(at, id, multi(5_000)), ErrSpendLimitExceeded)
	require.NoError(t, exec(at, id, multi(100)))
	stored, _ := keeper.GetAuthenticator(at, account, id)
	require.Equal(t, "100", stored.GetSessionKey().SpentUaeth)
	_, err := ms.RegisterAuthenticator(at, &MsgRegisterAuthenticator{Account: account.String(), Authenticator: &Authenticator{Kind: &Authenticator_SessionKey{SessionKey: &SessionKey{
		Pubkey: pub, ExpiresAtUnix: at.BlockTime().Add(time.Hour).Unix(), AllowedMsgTypes: []string{"/cosmos.authz.v1beta1.MsgGrant"}, SpendLimitUaeth: "100"}}}})
	require.ErrorIs(t, err, ErrNotSessionDelegable)
}

func TestSessionSpend(t *testing.T) {
	c := func(s string) sdk.Coins { cs, err := sdk.ParseCoinsNormalized(s); require.NoError(t, err); return cs }
	got, err := sessionSpend(c("100uaeth,5usdc"), c("40uaeth,5usdc,7uosmo"))
	require.NoError(t, err)
	require.Equal(t, sdkmath.NewInt(60), got)
	got, err = sessionSpend(c("100uaeth"), c("150uaeth"))
	require.NoError(t, err)
	require.True(t, got.IsZero(), "money coming in costs nothing")
	_, err = sessionSpend(c("100uaeth,5usdc"), c("100uaeth,4usdc"))
	require.ErrorIs(t, err, ErrSpendLimitExceeded)
	got, err = sessionSpend(c("100uaeth"), sdk.Coins{})
	require.NoError(t, err)
	require.Equal(t, sdkmath.NewInt(100), got)
}
