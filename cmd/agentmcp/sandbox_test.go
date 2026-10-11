package main

import (
	"context"
	"crypto/rand"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"cosmossdk.io/math"
	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"

	"github.com/whoyoujoshin/aether/app"
	"github.com/whoyoujoshin/aether/crypto/mldsa"
	"github.com/whoyoujoshin/aether/directory"
	"github.com/whoyoujoshin/aether/paywall"
	"github.com/whoyoujoshin/aether/wallet"
	"github.com/whoyoujoshin/aether/x402"
)

func codeOf(t *testing.T, err error) string {
	t.Helper()
	var ae *agentError
	require.True(t, errors.As(err, &ae), "%v", err)
	return ae.Code
}

func newTestSandbox(t *testing.T) (*sandbox, *fakeChain) {
	t.Helper()
	f := setupAgent(t)
	secret := make([]byte, 32)
	_, _ = rand.Read(secret)
	aead, err := newAEAD(secret)
	require.NoError(t, err)
	reg := codectypes.NewInterfaceRegistry()
	mldsa.RegisterInterfaces(reg)
	fw, err := wallet.NewWallet("funder", "memory", "", codec.NewProtoCodec(reg))
	require.NoError(t, err)
	acc, _, err := fw.CreateAccount("funder")
	require.NoError(t, err)
	s := &sandbox{aead: aead, funder: fw, funderName: "funder", funderAddr: acc.Address, grant: math.NewInt(10_000),
		perIP: 2, perDay: 3, now: time.Now, byIP: map[string][]time.Time{},
		client: directory.SafeHTTPClient(true, 10*time.Second)}
	s.listed = func(context.Context) ([]string, error) { return nil, nil }
	sbx = s
	t.Cleanup(func() { sbx = nil })
	return s, f
}

func fromIP(ip string) *mcp.CallToolRequest {
	return &mcp.CallToolRequest{Extra: &mcp.RequestExtra{Header: http.Header{"X-Forwarded-For": {ip}}}}
}

func TestSandboxTokenIsTheWallet(t *testing.T) {
	s, _ := newTestSandbox(t)
	entropy := make([]byte, 32)
	_, _ = rand.Read(entropy)
	token, err := s.seal(entropy)
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(token, sandboxTokenPrefix))
	_, want, err := walletFromEntropy(entropy)
	require.NoError(t, err)
	_, got, err := s.open(token)
	require.NoError(t, err)
	require.Equal(t, want, got, "the same token opens the same key, every time")

	// Tampered, foreign or malformed tokens open nothing.
	tampered := token[:len(token)-2] + map[bool]string{true: "AA", false: "BB"}[!strings.HasSuffix(token, "AA")]
	other, _ := newTestSandbox(t)
	for _, bad := range []string{tampered, "", "aether-test-wallet-v1.!!", strings.TrimPrefix(token, sandboxTokenPrefix)} {
		_, _, err := s.open(bad)
		require.Equal(t, codeBadToken, codeOf(t, err), bad)
	}
	_, _, err = other.open(token)
	require.Equal(t, codeBadToken, codeOf(t, err), "another server's secret can't open it")
}

func TestSandboxRunsOnlyOnTestChains(t *testing.T) {
	setupAgent(t)
	chainID = "aether-1"
	sandboxSecretFile = t.TempDir() + "/secret"
	_, err := newSandbox()
	require.ErrorContains(t, err, "test or dev chain")
	require.True(t, testChain("aether-testnet-1"))
	require.True(t, testChain("aether-devnet"))
}

func TestCreateTestWalletFundsItWithinLimits(t *testing.T) {
	s, f := newTestSandbox(t)
	ctx := context.Background()

	_, a, err := toolCreateTestWallet(ctx, fromIP("203.0.113.5"), struct{}{})
	require.NoError(t, err)
	_, b, err := toolCreateTestWallet(ctx, fromIP("203.0.113.5"), struct{}{})
	require.NoError(t, err)
	require.NotEqual(t, a.Address, b.Address)
	require.Equal(t, "10000", a.Funding.Base)

	// Each funding is a send of the grant from the funder, at successive
	// sequences even before either is in a block.
	require.Len(t, f.broadcasts, 2)
	cfg := app.MakeEncodingConfig()
	for i, want := range []string{a.Address, b.Address} {
		tx, err := cfg.TxConfig.TxDecoder()(f.broadcasts[i])
		require.NoError(t, err)
		send := tx.GetMsgs()[0].(*banktypes.MsgSend)
		require.Equal(t, s.funderAddr, send.FromAddress)
		require.Equal(t, want, send.ToAddress)
		require.Equal(t, "10000uaeth", sdk.Coins(send.Amount).String())
	}
	require.Equal(t, uint64(2), s.nextSeq)

	// The token opens the address it named.
	_, addr, err := s.open(a.WalletToken)
	require.NoError(t, err)
	require.Equal(t, a.Address, addr)

	// Two a day per caller here; three in all.
	_, _, err = toolCreateTestWallet(ctx, fromIP("203.0.113.5"), struct{}{})
	require.Equal(t, codeSandboxLimit, codeOf(t, err))
	_, _, err = toolCreateTestWallet(ctx, fromIP("198.51.100.7"), struct{}{})
	require.NoError(t, err)
	_, _, err = toolCreateTestWallet(ctx, fromIP("198.51.100.8"), struct{}{})
	require.Equal(t, codeSandboxLimit, codeOf(t, err))

	// A day later, there's room again.
	s.now = func() time.Time { return time.Now().Add(25 * time.Hour) }
	_, _, err = toolCreateTestWallet(ctx, fromIP("203.0.113.5"), struct{}{})
	require.NoError(t, err)
}

func TestCreateTestWalletGivesBackTheSlotIfFundingFails(t *testing.T) {
	_, f := newTestSandbox(t)
	f.checkTxCode = 5
	_, _, err := toolCreateTestWallet(context.Background(), fromIP("203.0.113.9"), struct{}{})
	require.Equal(t, codeTxRejected, codeOf(t, err))
	require.Empty(t, sbx.byIP["203.0.113.9"])
	require.Empty(t, sbx.created)
}

func TestClientIPIsWhatTheProxyAppended(t *testing.T) {
	require.Equal(t, "203.0.113.5", clientIP(fromIP("10.0.0.1, 203.0.113.5")))
	req := &mcp.CallToolRequest{Extra: &mcp.RequestExtra{Header: http.Header{"X-Forwarded-For": {"1.2.3.4", "203.0.113.6"}}}}
	require.Equal(t, "203.0.113.6", clientIP(req))
	require.Equal(t, "local", clientIP(nil))
}

// x402Chain adapts the fake node to the facilitator.
type x402Chain struct{ f *fakeChain }

func (c x402Chain) Account(context.Context, string) (uint64, uint64, error) {
	n, s, err := c.f.accountInfo("")
	return n, s, err
}
func (c x402Chain) LatestHeight(context.Context) (int64, error)      { return c.f.latestHeight() }
func (c x402Chain) Simulate(context.Context, []byte) (uint64, error) { return 100_000, nil }
func (c x402Chain) Broadcast(_ context.Context, b []byte) (string, uint32, string, error) {
	r, err := c.f.broadcast(wallet.SignedTx{Bytes: b})
	return r.TxHash, r.Code, r.RawLog, err
}
func (c x402Chain) TxResult(_ context.Context, hash string) (bool, uint32, string, error) {
	d, err := c.f.lookup(hash)
	if err != nil {
		return false, 0, "", nil
	}
	return true, d.Code, "", nil
}

func TestBuyServicePaysAListedServiceWithExact(t *testing.T) {
	s, f := newTestSandbox(t)
	f.autoInclude = true
	ctx := context.Background()
	payee := sdk.AccAddress("sandbox_test_payee__").String()

	fac := x402.NewFacilitator(chainID, x402Chain{f})
	fac.PollInterval = time.Millisecond
	pw, err := paywall.New(paywall.Config{
		PayTo: payee, Price: math.NewInt(1000), Network: chainID, Description: "weather",
		Lookup: f.lookup, Exact: &paywall.ExactConfig{Settler: fac},
	})
	require.NoError(t, err)
	srv := httptest.NewServer(pw.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"weather":"sunny","payer":"` + paywall.Payer(w) + `"}`))
	})))
	defer srv.Close()
	s.listed = func(context.Context) ([]string, error) { return []string{srv.URL + "/svc"}, nil }

	_, created, err := toolCreateTestWallet(ctx, fromIP("203.0.113.5"), struct{}{})
	require.NoError(t, err)
	token := created.WalletToken

	// Only listed services.
	_, _, err = toolBuyService(ctx, nil, buyServiceInput{WalletToken: token, URL: srv.URL + "/other", MaxAmount: "0.01 AETH"})
	require.Equal(t, codeNotListed, codeOf(t, err))
	_, _, err = toolBuyService(ctx, nil, buyServiceInput{WalletToken: token, URL: srv.URL + "/svcx", MaxAmount: "0.01 AETH"})
	require.Equal(t, codeNotListed, codeOf(t, err), "a prefix of the name isn't the service")

	// Never more than maxAmount.
	_, _, err = toolBuyService(ctx, nil, buyServiceInput{WalletToken: token, URL: srv.URL + "/svc/now", MaxAmount: "999uaeth"})
	require.Equal(t, codePriceExceedsMax, codeOf(t, err))

	before := len(f.broadcasts)
	_, out, err := toolBuyService(ctx, nil, buyServiceInput{WalletToken: token, URL: srv.URL + "/svc/now", MaxAmount: "0.01 AETH"})
	require.NoError(t, err)
	require.Equal(t, "paid", out.Status)
	require.Equal(t, 200, out.HTTPStatus)
	require.Contains(t, out.Body, `"payer":"`+created.Address+`"`)
	require.Equal(t, "1000", out.Amount.Base)
	require.Equal(t, payee, out.PayTo)
	require.Len(t, f.broadcasts, before+1, "one payment, broadcast by the seller's facilitator")
	d, err := f.lookup(out.TxHash)
	require.NoError(t, err)
	require.Equal(t, []wallet.Transfer{{From: created.Address, To: payee, Amount: "1000uaeth"}}, d.Transfers)
}

func TestSendFromTestWallet(t *testing.T) {
	_, f := newTestSandbox(t)
	ctx := context.Background()
	_, created, err := toolCreateTestWallet(ctx, fromIP("203.0.113.5"), struct{}{})
	require.NoError(t, err)
	to := sdk.AccAddress("sandbox_send_to_____").String()

	_, _, err = toolSendFromTestWallet(ctx, nil, sendFromTestWalletInput{WalletToken: created.WalletToken, To: to, Amount: "500"})
	require.Equal(t, codeInvalidAmount, codeOf(t, err), "an amount needs its unit")
	_, _, err = toolSendFromTestWallet(ctx, nil, sendFromTestWalletInput{WalletToken: "nope", To: to, Amount: "500uaeth"})
	require.Equal(t, codeBadToken, codeOf(t, err))

	_, out, err := toolSendFromTestWallet(ctx, nil, sendFromTestWalletInput{WalletToken: created.WalletToken, To: to, Amount: "500uaeth"})
	require.NoError(t, err)
	require.NotEmpty(t, out.TxHash)
	tx, err := app.MakeEncodingConfig().TxConfig.TxDecoder()(f.broadcasts[len(f.broadcasts)-1])
	require.NoError(t, err)
	send := tx.GetMsgs()[0].(*banktypes.MsgSend)
	require.Equal(t, created.Address, send.FromAddress)
	require.Equal(t, to, send.ToAddress)
}
