package main

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/x/authz"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/whoyoujoshin/aether/wallet"
)

func TestParseAmount(t *testing.T) {
	for in, want := range map[string]int64{
		"1.5 AETH":      1_500_000,
		"1.5aeth":       1_500_000,
		" 2 Aeth ":      2_000_000,
		"0.000001 AETH": 1,
		"0.1 AETH":      100_000,
		"1500000uaeth":  1_500_000,
		"1500000 UAETH": 1_500_000,
		"0100000uaeth":  100_000, // base 10, never octal
		"010.5 AETH":    10_500_000,
		"1.230000 AETH": 1_230_000,
	} {
		got, err := parseAmount(in)
		require.NoError(t, err, in)
		require.Equal(t, want, got.Int64(), in)
	}

	for in, wantMsg := range map[string]string{
		"1500000":             "no unit",
		" 42 ":                "no unit",
		"1.0000001 AETH":      "decimal places",
		"1.5 uaeth":           "can't be fractional",
		"0 AETH":              "greater than zero",
		"0.0 uaeth":           "can't be fractional",
		"-1 AETH":             "invalid amount",
		"1.5 BTC":             "unknown unit",
		"1e6 uaeth":           "invalid amount",
		"0x10 uaeth":          "invalid amount",
		"":                    "invalid amount",
		"99999999999999 AETH": "too large",
	} {
		_, err := parseAmount(in)
		ae := requireCode(t, err, codeInvalidAmount)
		require.Contains(t, ae.Message, wantMsg, in)
	}
}

func TestFormatAeth(t *testing.T) {
	for uaeth, want := range map[int64]string{
		0: "0", 1: "0.000001", 100_000: "0.1", 1_000_000: "1", 1_500_000: "1.5", 1_234_567: "1.234567", 20_000_000: "20",
	} {
		require.Equal(t, want, formatAeth(math.NewInt(uaeth)))
	}
}

func TestSendAeth_AmountInEitherUnitIsTheSamePayment(t *testing.T) {
	f := setupAgent(t)
	first, err := send(t, "same", "0.25 AETH", "")
	require.NoError(t, err)
	require.Equal(t, amountDTO{Uaeth: "250000", Aeth: "0.25"}, first.Amount)

	again, err := send(t, "same", "250000uaeth", "")
	require.NoError(t, err)
	require.True(t, again.Replayed, "0.25 AETH and 250000uaeth are one payment")
	require.Equal(t, f.broadcasts[0], f.broadcasts[1])

	_, err = send(t, "bare", "250000", "")
	requireCode(t, err, codeInvalidAmount)
	require.Len(t, f.broadcasts, 2, "a bare number must never reach signing")
}

func TestErrors_AreStructuredJSON(t *testing.T) {
	e := newError(codeDailyLimit, "over")
	e.RetryAfterSeconds = 60
	var wire struct {
		Error struct {
			Code              string `json:"code"`
			Retryable         bool   `json:"retryable"`
			RetryAfterSeconds int64  `json:"retryAfterSeconds"`
			Message           string `json:"message"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal([]byte(e.Error()), &wire))
	require.Equal(t, codeDailyLimit, wire.Error.Code)
	require.False(t, wire.Error.Retryable)
	require.Equal(t, int64(60), wire.Error.RetryAfterSeconds)
	require.Equal(t, "over", wire.Error.Message)

	// Anything a tool returns comes out coded.
	failing := func(err error) error {
		h := coded(func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, struct{}, error) {
			return nil, struct{}{}, err
		})
		_, _, out := h(context.Background(), nil, struct{}{})
		return out
	}
	ae := requireCode(t, failing(status.Error(codes.Unavailable, "connection refused")), codeNodeUnreachable)
	require.True(t, ae.Retryable)
	requireCode(t, failing(errors.New("boom")), codeInternal)
	requireCode(t, failing(wallet.ErrAuthzNotActive), codeGrantNotActive)
	requireCode(t, failing(newError(codePerTxLimit, "x")), codePerTxLimit)
}

func TestChainErrorCode(t *testing.T) {
	for _, c := range []struct {
		codespace string
		code      uint32
		log, want string
	}{
		{"sdk", 5, "spendable balance 0uaeth is smaller than 1uaeth: insufficient funds", codeInsufficientFunds},
		{"sdk", 5, "failed to execute message; message index: 0: requested amount is more than spend limit: insufficient funds", codeGrantLimit},
		{"sdk", 4, "failed to execute message; message index: 0: cannot send to aether1xyz address: unauthorized", codeGrantRecipient},
		{"sdk", 13, "insufficient fee", codeInsufficientFee},
		{"authz", 2, "authorization not found", codeGrantNotFound},
		{"authz", 6, "authorization expired", codeGrantExpired},
		{"feegrant", 2, "fee limit exceeded", codeFeeGrantRejected},
		{"sdk", 11, "out of gas", codeTxFailed},
	} {
		require.Equal(t, c.want, chainErrorCode(c.codespace, c.code, c.log, codeTxFailed), c.log)
	}
}

func TestSendAeth_UnfundedAgentAccount(t *testing.T) {
	f := setupAgent(t)
	f.accountErr = status.Error(codes.NotFound, "account not found")
	_, err := send(t, "k", "1uaeth", "")
	requireCode(t, err, codeAccountNotFound)
	require.Empty(t, f.broadcasts)
}

func TestRetryAfter(t *testing.T) {
	now := time.Now()
	st := &agentState{Events: []spendEvent{
		{Time: now.Add(-23 * time.Hour), Amount: 3},
		{Time: now.Add(-1 * time.Hour), Amount: 5},
	}}
	wait, ok := st.retryAfter(now, 2, 8) // 8 spent; need 2 free: the oldest (3) must age out, in 1h
	require.True(t, ok)
	require.InDelta(t, time.Hour.Seconds(), wait.Seconds(), 1)

	wait, ok = st.retryAfter(now, 6, 8) // needs both to age out: 23h
	require.True(t, ok)
	require.InDelta(t, (23 * time.Hour).Seconds(), wait.Seconds(), 1)

	_, ok = st.retryAfter(now, 9, 8) // bigger than the whole limit: never
	require.False(t, ok)
}

// --- grant mode ---

func granterAddr() string { return sdk.AccAddress("human_principal_____").String() }

func setupGrantMode(t *testing.T) *fakeChain {
	f := setupAgent(t)
	granter = granterAddr()
	perTxLimit, dailyLimit = 10_000_000, 50_000_000
	t.Cleanup(func() { granter, feeGranter = "", "" })
	return f
}

func TestGrantMode_PaysFromGranterViaMsgExec(t *testing.T) {
	f := setupGrantMode(t)
	feeGranter = granterAddr()
	f.grant = &wallet.SendGrant{SpendLimit: sdk.NewCoins(sdk.NewInt64Coin("uaeth", 5_000_000))}

	out, err := send(t, "g1", "2 AETH", "invoice-1")
	require.NoError(t, err)
	require.Equal(t, statusPending, out.Status)
	require.Equal(t, granterAddr(), out.From, "the funds come from the granter")

	tx := decodeTx(t, f.broadcasts[0])
	msgs := tx.GetMsgs()
	require.Len(t, msgs, 1)
	exec, ok := msgs[0].(*authz.MsgExec)
	require.True(t, ok, "grant mode must wrap the payment in MsgExec, got %T", msgs[0])

	w, err := newWallet()
	require.NoError(t, err)
	agent, err := getOrCreateAgentAccount(w)
	require.NoError(t, err)
	require.Equal(t, agent.Address, exec.Grantee, "the agent signs as grantee")

	inner, err := exec.GetMessages()
	require.NoError(t, err)
	require.Len(t, inner, 1)
	msgSend := inner[0].(*banktypes.MsgSend)
	require.Equal(t, granterAddr(), msgSend.FromAddress)
	require.Equal(t, recipient(), msgSend.ToAddress)
	require.Equal(t, "2000000uaeth", sdk.Coins(msgSend.Amount).String())

	require.Equal(t, sdk.MustAccAddressFromBech32(granterAddr()), sdk.AccAddress(tx.(sdk.FeeTx).FeeGranter()), "fees come from the fee grant")
	_, seq := decode(t, f.broadcasts[0])
	require.Equal(t, uint64(0), seq, "sequence is the agent's own")

	// Retry is idempotent in grant mode too.
	again, err := send(t, "g1", "2000000uaeth", "invoice-1")
	require.NoError(t, err)
	require.True(t, again.Replayed)
	require.Equal(t, f.broadcasts[0], f.broadcasts[1])
}

func TestGrantMode_RefusesWhatTheChainWould(t *testing.T) {
	f := setupGrantMode(t)
	past := time.Now().Add(-time.Minute)
	future := time.Now().Add(time.Hour)
	other := sdk.AccAddress("someone_else________").String()

	for _, c := range []struct {
		name  string
		grant *wallet.SendGrant
		err   error
		want  string
	}{
		{"chain below activation", nil, wallet.ErrAuthzNotActive, codeGrantNotActive},
		{"never granted or revoked", nil, nil, codeGrantNotFound},
		{"expired", &wallet.SendGrant{Unlimited: true, Expiration: &past}, nil, codeGrantExpired},
		{"over what's left", &wallet.SendGrant{SpendLimit: sdk.NewCoins(sdk.NewInt64Coin("uaeth", 999_999)), Expiration: &future}, nil, codeGrantLimit},
		{"recipient not allowed", &wallet.SendGrant{Unlimited: true, AllowList: []string{other}}, nil, codeGrantRecipient},
	} {
		f.grant, f.grantErr = c.grant, c.err
		_, err := send(t, "k-"+c.name, "1 AETH", "")
		requireCode(t, err, c.want)
	}
	require.Empty(t, f.broadcasts, "nothing the grant forbids is signed or sent")
	require.Equal(t, int64(0), spent(t))

	f.grant, f.grantErr = &wallet.SendGrant{Unlimited: true, AllowList: []string{recipient()}}, nil
	out, err := send(t, "ok", "1 AETH", "")
	require.NoError(t, err)
	require.Equal(t, statusPending, out.Status)
}

func TestGrantMode_ServerCapsStillApply(t *testing.T) {
	f := setupGrantMode(t)
	perTxLimit = 1_000_000
	f.grant = &wallet.SendGrant{Unlimited: true}
	_, err := send(t, "k", "2 AETH", "")
	requireCode(t, err, codePerTxLimit)
}

func TestGrantMode_SpendingStatusShowsGrant(t *testing.T) {
	f := setupGrantMode(t)
	exp := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	f.grant = &wallet.SendGrant{SpendLimit: sdk.NewCoins(sdk.NewInt64Coin("uaeth", 3_500_000)), Expiration: &exp}
	f.balances = map[string]sdk.Coins{granterAddr(): sdk.NewCoins(sdk.NewInt64Coin("uaeth", 9_000_000))}

	_, out, err := toolGetSpendingStatus(context.Background(), nil, getSpendingStatusInput{})
	require.NoError(t, err)
	require.Equal(t, "grant", out.Mode)
	require.NotNil(t, out.Grant)
	require.Equal(t, "active", out.Grant.Status)
	require.Equal(t, amountDTO{Uaeth: "3500000", Aeth: "3.5"}, *out.Grant.Remaining)
	require.Equal(t, "9", out.Grant.Balance.Aeth)
	require.Equal(t, "2030-01-01T00:00:00Z", out.Grant.Expiration)

	f.grant, f.grantErr = nil, wallet.ErrAuthzNotActive
	_, out, err = toolGetSpendingStatus(context.Background(), nil, getSpendingStatusInput{})
	require.NoError(t, err)
	require.Equal(t, "not_active", out.Grant.Status)

	f.grantErr = status.Error(codes.Unavailable, "connection refused")
	_, out, err = toolGetSpendingStatus(context.Background(), nil, getSpendingStatusInput{})
	require.NoError(t, err, "the server's own caps are reported even when the node isn't")
	require.Equal(t, "unknown", out.Grant.Status)
	require.Equal(t, codeNodeUnreachable, out.Grant.ErrorCode)
	require.Equal(t, "10", out.PerTxLimit.Aeth)

	_, addr, err := toolGetAgentAddress(context.Background(), nil, getAgentAddressInput{})
	require.NoError(t, err)
	require.Equal(t, granterAddr(), addr.SpendsFrom)
	require.Equal(t, "grant", addr.Mode)
}
