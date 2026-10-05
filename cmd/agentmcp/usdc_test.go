package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	"github.com/cosmos/gogoproto/proto"
	"github.com/stretchr/testify/require"

	"github.com/whoyoujoshin/aether/crypto/mldsa"
	"github.com/whoyoujoshin/aether/paywall"
	"github.com/whoyoujoshin/aether/wallet"
)

// useUSDC turns USDC on (over channel-3) with the given limits, and back
// off after the test.
func useUSDC(t *testing.T, perTx, daily, threshold string) wallet.Asset {
	t.Helper()
	require.NoError(t, configureUSDC(wallet.USDCSetting{Channel: "channel-3"}, perTx, daily, threshold))
	t.Cleanup(func() {
		assets, _ = wallet.NewAssets("")
		usdcPerTxLimit, usdcDailyLimit, usdcApprovalThreshold = 0, 0, math.Int{}
	})
	usdc, ok := assets.BySymbol("USDC")
	require.True(t, ok)
	return usdc
}

// sentCoins is what the last broadcast transaction's MsgSend moves.
func sentCoins(t *testing.T, f *fakeChain) sdk.Coins {
	t.Helper()
	f.mu.Lock()
	bz := f.broadcasts[len(f.broadcasts)-1]
	f.mu.Unlock()
	msgs := decodeTx(t, bz).GetMsgs()
	require.Len(t, msgs, 1)
	return msgs[0].(*banktypes.MsgSend).Amount
}

func TestUSDC_OffUntilTheOwnerEnablesIt(t *testing.T) {
	setupAgent(t)

	// No channel: USDC is an unknown unit, never guessed at.
	_, err := send(t, "k1", "5 USDC", "")
	requireCode(t, err, codeInvalidAmount)

	// A channel but no limits: known, but not spendable.
	useUSDC(t, "", "", "")
	_, err = send(t, "k2", "5 USDC", "")
	requireCode(t, err, codeAssetNotEnabled)

	// AETH is unaffected either way.
	_, err = send(t, "k3", "0.5 AETH", "")
	require.NoError(t, err)
}

func TestUSDC_SendsTheRightDenomUnderItsOwnLimits(t *testing.T) {
	f := setupAgent(t)
	usdc := useUSDC(t, "5 USDC", "12 USDC", "")

	_, err := send(t, "too-big", "6 USDC", "")
	requireCode(t, err, codePerTxLimit)

	out, err := send(t, "u1", "5 USDC", "invoice 1")
	require.NoError(t, err)
	require.Equal(t, amountDTO{Asset: "USDC", Amount: "5", Base: "5000000", Denom: usdc.Denom}, out.Amount)
	require.Equal(t, sdk.NewCoins(sdk.NewInt64Coin(usdc.Denom, 5_000_000)), sentCoins(t, f), "a USDC payment moves USDC, never uaeth")

	_, err = send(t, "u2", "5000000uusdc", "")
	require.NoError(t, err)
	_, err = send(t, "u3", "3 USDC", "")
	ae := requireCode(t, err, codeDailyLimit)
	require.Contains(t, ae.Message, "12 USDC")

	// AETH has its own, untouched budget: all five 1 AETH payments fit.
	for i := 0; i < 5; i++ {
		_, err = send(t, fmt.Sprintf("a%d", i), "1 AETH", "")
		require.NoError(t, err)
	}
	require.Equal(t, sdk.NewCoins(sdk.NewInt64Coin("uaeth", 1_000_000)), sentCoins(t, f))

	_, status, err := toolGetSpendingStatus(context.Background(), nil, getSpendingStatusInput{})
	require.NoError(t, err)
	require.Equal(t, "5", status.SpentLast24h.Aeth, "the AETH fields stay AETH-only")
	require.Len(t, status.Assets, 2)
	u := status.Assets[1]
	require.Equal(t, "USDC", u.Asset)
	require.True(t, u.Enabled)
	require.Equal(t, "10", u.SpentLast24h.Amount)
	require.Equal(t, "2", u.Remaining.Amount)
}

func TestUSDC_ReplayMustMatchTheAsset(t *testing.T) {
	setupAgent(t)
	useUSDC(t, "5 USDC", "12 USDC", "")

	_, err := send(t, "k", "1 USDC", "")
	require.NoError(t, err)
	// Same key, same number of base units, other asset: a different payment.
	_, err = send(t, "k", "1000000uaeth", "")
	requireCode(t, err, codeIdempotencyConflict)
	out, err := send(t, "k", "1000000uusdc", "")
	require.NoError(t, err)
	require.True(t, out.Replayed)
}

func TestUSDC_ApprovalsHaveTheirOwnThreshold(t *testing.T) {
	setupAgent(t)
	o := setupApprovals(t, 5_000_000) // AETH above 5 AETH needs approval
	useUSDC(t, "50 USDC", "100 USDC", "10 USDC")

	out, err := send(t, "u-big", "20 USDC", "")
	require.NoError(t, err)
	require.Equal(t, statusPendingApproval, out.Status)
	require.Contains(t, out.Message, "20 USDC")
	require.Contains(t, out.Message, "10 USDC")

	// Below the USDC threshold goes straight through; so does AETH below its own.
	out, err = send(t, "u-small", "8 USDC", "")
	require.NoError(t, err)
	require.NotEqual(t, statusPendingApproval, out.Status)
	out, err = send(t, "a", "0.5 AETH", "")
	require.NoError(t, err)
	require.NotEqual(t, statusPendingApproval, out.Status)

	// The owner's decision is bound to the asset.
	st, err := loadState()
	require.NoError(t, err)
	req := st.Approvals["u-big"]
	usdc, _ := assets.BySymbol("USDC")
	require.Equal(t, usdc.Denom, req.Denom)
	o.decide(t, "approve", req.ID)
	out, err = send(t, "u-big", "20 USDC", "")
	require.NoError(t, err)
	require.Equal(t, statusPending, out.Status)
}

func TestUSDC_InvoicesMatchOnlyTheirAsset(t *testing.T) {
	f := setupAgent(t)
	usdc := useUSDC(t, "", "", "") // receiving needs no spending limits
	f.payments = []wallet.IncomingPayment{
		{Hash: "AETH", Height: 101, Memo: "inv-1", Amount: sdk.NewCoins(sdk.NewInt64Coin("uaeth", 9_000_000))},
		{Hash: "USDC", Height: 102, Memo: "inv-1", Amount: sdk.NewCoins(sdk.NewInt64Coin(usdc.Denom, 2_000_000))},
	}
	found, err := findPayment(f, "addr", "inv-1", usdc, math.NewInt(2_000_000), 100)
	require.NoError(t, err)
	require.NotNil(t, found)
	require.Equal(t, "USDC", found.TxHash, "9 AETH doesn't pay a 2 USDC invoice")
	require.Equal(t, "2", found.Amount.Amount)
	require.Equal(t, "USDC", found.Amount.Asset)

	found, err = findPayment(f, "addr", "inv-1", usdc, math.NewInt(3_000_000), 100)
	require.NoError(t, err)
	require.Nil(t, found)
}

func TestConfigureUSDC_RefusesMistakes(t *testing.T) {
	t.Cleanup(func() {
		assets, _ = wallet.NewAssets("")
		usdcPerTxLimit, usdcDailyLimit, usdcApprovalThreshold = 0, 0, math.Int{}
	})
	ch3 := wallet.USDCSetting{Channel: "channel-3"}
	for name, c := range map[string]struct {
		usdc                    wallet.USDCSetting
		perTx, daily, threshold string
	}{
		"limits without a USDC":   {wallet.USDCSetting{}, "5 USDC", "10 USDC", ""},
		"limit in AETH":           {ch3, "5 AETH", "10 USDC", ""},
		"only one limit":          {ch3, "5 USDC", "", ""},
		"bad channel":             {wallet.USDCSetting{Channel: "3"}, "", "", ""},
		"bad path":                {wallet.USDCSetting{Path: "channel-1"}, "", "", ""},
		"channel and path":        {wallet.USDCSetting{Channel: "channel-3", Path: "transfer/channel-3"}, "", "", ""},
		"threshold with no owner": {ch3, "5 USDC", "10 USDC", "4 USDC"},
	} {
		usdcPerTxLimit, usdcDailyLimit = 0, 0
		require.Error(t, configureUSDC(c.usdc, c.perTx, c.daily, c.threshold), name)
	}
}

// usdcSeller is a paid API priced at 0.05 USDC, signing receipts.
func usdcSeller(t *testing.T, f *fakeChain, usdc wallet.Asset) (*httptest.Server, string) {
	t.Helper()
	sk, err := mldsa.GenPrivKey()
	require.NoError(t, err)
	payTo := sdk.AccAddress(sk.PubKey().Address()).String()
	pw, err := paywall.New(paywall.Config{
		PayTo: payTo, Price: math.NewInt(50_000), Asset: usdc, Network: chainID, Lookup: f.lookup,
		Receipts: &paywall.ReceiptConfig{Sign: func(msg []byte) ([]byte, []byte, error) {
			sig, err := sk.Sign(msg)
			return sig, sk.PubKey().Bytes(), err
		}},
	})
	require.NoError(t, err)
	srv := httptest.NewServer(pw.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "paid in dollars") })))
	t.Cleanup(srv.Close)
	return srv, payTo
}

func TestUSDC_FetchPaidBuysFromAUSDCPricedService(t *testing.T) {
	f := setupAgent(t)
	f.autoInclude = true
	usdc := useUSDC(t, "1 USDC", "2 USDC", "")
	srv, payTo := usdcSeller(t, f, usdc)

	// A maxAmount in AETH says nothing about a USDC price: nothing is paid.
	_, err := fetch(t, srv.URL, "1 AETH", "wrong-asset", 5)
	requireCode(t, err, codeAssetMismatch)
	_, err = fetch(t, srv.URL, "0.04 USDC", "too-cheap", 5)
	requireCode(t, err, codePriceExceedsMax)
	require.Empty(t, f.broadcasts)

	out, err := fetch(t, srv.URL, "0.10 USDC", "buy", 5)
	require.NoError(t, err)
	require.Equal(t, "paid", out.Status)
	require.Equal(t, "paid in dollars", out.Body)
	require.Equal(t, amountDTO{Asset: "USDC", Amount: "0.05", Base: "50000", Denom: usdc.Denom}, out.Payment.Amount)
	require.Equal(t, sdk.NewCoins(sdk.NewInt64Coin(usdc.Denom, 50_000)), sentCoins(t, f), "paid in USDC")

	// The seller's receipt states the asset, and checks out.
	require.NotNil(t, out.Receipt)
	require.True(t, out.Receipt.Verified, out.Receipt.Problem)
	require.Equal(t, "50000"+usdc.Denom, out.Receipt.Receipt.Amount)
	require.Equal(t, payTo, out.Receipt.Receipt.PayTo)

	// It counted against USDC's cap, not AETH's.
	_, status, err := toolGetSpendingStatus(context.Background(), nil, getSpendingStatusInput{})
	require.NoError(t, err)
	require.Equal(t, "0", status.SpentLast24h.Aeth)
	require.Equal(t, "0.05", status.Assets[1].SpentLast24h.Amount)

	_, list, err := toolListPurchases(context.Background(), nil, listPurchasesInput{})
	require.NoError(t, err)
	require.Equal(t, "USDC", list.Purchases[0].Amount.Asset)
}

func TestUSDC_UnknownAssetIsNotPaid(t *testing.T) {
	f := setupAgent(t)
	f.autoInclude = true
	usdc, err := wallet.USDC("channel-3")
	require.NoError(t, err)
	srv, _ := usdcSeller(t, f, usdc) // this agent has no --usdc-channel

	_, err = fetch(t, srv.URL, "1 AETH", "k", 5)
	ae := requireCode(t, err, codePaymentUnsupported)
	require.Contains(t, ae.Message, usdc.Denom)
	require.Empty(t, f.broadcasts)
}

// usdcSchemesSeller prices at 0.02 USDC and offers prepaid balances (with
// withdrawals) and pull allowances as well.
func usdcSchemesSeller(t *testing.T, f *fakeChain, usdc wallet.Asset, payout paywall.Payout) (*httptest.Server, *chainGrants) {
	t.Helper()
	ledger, err := paywall.NewFileLedger("")
	require.NoError(t, err)
	g := &chainGrants{f: f, denom: usdc.Denom, collected: map[string]int64{}, grantAt: map[string]int64{}}
	pw, err := paywall.New(paywall.Config{
		PayTo: sellerAddr(), Price: math.NewInt(20_000), Asset: usdc, Network: chainID, Lookup: f.lookup,
		Prepaid: &paywall.PrepaidConfig{Ledger: ledger, MinDeposit: math.NewInt(50_000), Payout: payout},
		Pull:    &paywall.PullConfig{Ledger: ledger, Collector: g, Grantee: collectorAddr(), Grants: g.get},
	})
	require.NoError(t, err)
	mux := http.NewServeMux()
	mux.Handle(paywall.ManifestPath, pw.ManifestHandler("svc", ""))
	mux.Handle(paywall.WithdrawPath, pw.WithdrawHandler())
	mux.Handle("/", pw.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "answer") })))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, g
}

func fetchUSDC(t *testing.T, url, key, prepay, allowance string) (fetchPaidOutput, error) {
	t.Helper()
	_, out, err := toolFetchPaid(context.Background(), nil, fetchPaidInput{URL: url, MaxAmount: "0.05 USDC", IdempotencyKey: key,
		Prepay: prepay, PullAllowance: allowance, TimeoutSeconds: 5})
	return out, err
}

func usdcSpent(t *testing.T) (aeth, usdc string) {
	t.Helper()
	_, status, err := toolGetSpendingStatus(context.Background(), nil, getSpendingStatusInput{})
	require.NoError(t, err)
	return status.SpentLast24h.Aeth, status.Assets[1].SpentLast24h.Amount
}

func TestUSDC_PrepaidDepositsDrawsAndWithdrawsInUSDC(t *testing.T) {
	f := setupAgent(t)
	f.autoInclude = true
	usdc := useUSDC(t, "1 USDC", "2 USDC", "")
	payout := &recordingPayout{}
	srv, _ := usdcSchemesSeller(t, f, usdc, payout)

	// The deposit has to be in the asset the service charges.
	_, err := fetchUSDC(t, srv.URL+"/q", "k0", "0.1 AETH", "")
	requireCode(t, err, codeAssetMismatch)
	require.Empty(t, f.broadcasts)

	out, err := fetchUSDC(t, srv.URL+"/q", "k1", "0.1 USDC", "")
	require.NoError(t, err)
	require.Equal(t, "paid", out.Status, out.Message)
	require.Equal(t, paywall.SchemePrepaid, out.Payment.Scheme)
	require.NotEmpty(t, out.Payment.DepositTxHash)
	require.Equal(t, sdk.NewCoins(sdk.NewInt64Coin(usdc.Denom, 100_000)), sentCoins(t, f), "the deposit moves USDC")
	require.Equal(t, amountDTO{Asset: "USDC", Amount: "0.08", Base: "80000", Denom: usdc.Denom}, *out.Payment.Balance)
	aeth, spentUSDC := usdcSpent(t)
	require.Equal(t, "0", aeth)
	require.Equal(t, "0.1", spentUSDC, "the deposit counts against USDC's budget")

	out, err = fetchUSDC(t, srv.URL+"/q", "k2", "0.1 USDC", "")
	require.NoError(t, err)
	require.Empty(t, out.Payment.DepositTxHash, "drawn from the balance")
	require.Equal(t, "0.06", out.Payment.Balance.Amount)
	require.Len(t, f.broadcasts, 1)

	got := balances(t)
	require.Len(t, got, 1)
	require.Equal(t, "USDC", got[0].Balance.Asset)
	require.Equal(t, "0.06", got[0].Balance.Amount)

	// Withdrawals are in the service's asset too.
	_, err = withdraw(t, srv.URL, "0.06 AETH", "w0")
	requireCode(t, err, codeAssetMismatch)
	wo, err := withdraw(t, srv.URL, "all", "w1")
	require.NoError(t, err)
	require.Equal(t, amountDTO{Asset: "USDC", Amount: "0.06", Base: "60000", Denom: usdc.Denom}, *wo.Amount)
	require.Equal(t, "0", wo.Balance.Base)
	require.Len(t, payout.signed, 1)
	require.Contains(t, payout.signed[0], " 60000 prepaid-withdrawal:")
	require.Empty(t, balances(t))
}

func TestUSDC_PullGrantsAUSDCAllowance(t *testing.T) {
	f := setupAgent(t)
	f.autoInclude = true
	usdc := useUSDC(t, "1 USDC", "2 USDC", "")
	srv, _ := usdcSchemesSeller(t, f, usdc, nil)

	_, err := fetchUSDC(t, srv.URL+"/q", "k0", "", "0.1 AETH")
	requireCode(t, err, codeAssetMismatch)
	require.Empty(t, f.broadcasts)

	out, err := fetchUSDC(t, srv.URL+"/q", "k1", "", "0.1 USDC")
	require.NoError(t, err)
	require.Equal(t, "paid", out.Status, out.Message)
	require.Equal(t, paywall.SchemePull, out.Payment.Scheme)
	require.Equal(t, amountDTO{Asset: "USDC", Amount: "0.02", Base: "20000", Denom: usdc.Denom}, *out.Payment.Owed)
	require.Equal(t, "0.08", out.Payment.Allowance.Amount)

	require.Len(t, f.broadcasts, 1)
	var a banktypes.SendAuthorization
	require.NoError(t, proto.Unmarshal(grantIn(t, f.broadcasts[0]).Grant.Authorization.Value, &a))
	require.Equal(t, sdk.NewCoins(sdk.NewInt64Coin(usdc.Denom, 100_000)), sdk.Coins(a.SpendLimit), "an allowance in USDC only: the seller can't collect AETH under it")
	require.Equal(t, []string{sellerAddr()}, a.AllowList)

	out, err = fetchUSDC(t, srv.URL+"/q", "k2", "", "0.1 USDC")
	require.NoError(t, err)
	require.Equal(t, "0.04", out.Payment.Owed.Amount)
	require.Len(t, f.broadcasts, 1, "no transaction per request")
	aeth, spentUSDC := usdcSpent(t)
	require.Equal(t, "0", aeth)
	require.Equal(t, "0.04", spentUSDC, "each request counts against USDC's budget")
}
