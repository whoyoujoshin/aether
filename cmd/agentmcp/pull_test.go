package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/x/authz"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	"github.com/cosmos/gogoproto/proto"
	"github.com/stretchr/testify/require"

	"github.com/whoyoujoshin/aether/app"
	"github.com/whoyoujoshin/aether/paywall"
	"github.com/whoyoujoshin/aether/wallet"
)

func collectorAddr() string { return sdk.AccAddress("pull_collector______").String() }

// chainGrants reads the allowances the fake chain has in blocks, less
// what the seller has collected under them.
type chainGrants struct {
	f         *fakeChain
	mu        sync.Mutex
	collected map[string]int64 // granter -> uaeth, under its latest grant
	grantAt   map[string]int64 // granter -> height of that grant
}

func (g *chainGrants) get(granter, grantee string) (*wallet.SendGrant, error) {
	g.f.mu.Lock()
	defer g.f.mu.Unlock()
	var found *wallet.SendGrant
	var at int64
	for hash, d := range g.f.blocks {
		if d.Code != 0 || d.Height < at {
			continue
		}
		tx, err := decodeTxBytes(g.f.signed[hash])
		if err != nil {
			continue
		}
		for _, m := range tx.GetMsgs() {
			mg, ok := m.(*authz.MsgGrant)
			if !ok || mg.Granter != granter || mg.Grantee != grantee {
				continue
			}
			var a banktypes.SendAuthorization
			if proto.Unmarshal(mg.Grant.Authorization.Value, &a) != nil {
				continue
			}
			found, at = &wallet.SendGrant{SpendLimit: a.SpendLimit, AllowList: a.AllowList, Expiration: mg.Grant.Expiration}, d.Height
		}
	}
	if found == nil {
		return nil, fmt.Errorf("%w from %s to %s", wallet.ErrGrantNotFound, granter, grantee)
	}
	g.mu.Lock()
	if g.grantAt[granter] != at { // a new grant replaces the old limit
		g.grantAt[granter], g.collected[granter] = at, 0
	}
	used := g.collected[granter]
	g.mu.Unlock()
	left := found.SpendLimit.AmountOf("uaeth").SubRaw(used)
	if !left.IsPositive() {
		return nil, fmt.Errorf("%w from %s to %s", wallet.ErrGrantNotFound, granter, grantee)
	}
	found.SpendLimit = sdk.NewCoins(sdk.NewCoin("uaeth", left))
	return found, nil
}

// SignCollect/Submit: a collector whose collections land at once.
func (g *chainGrants) SignCollect(from string, amount math.Int, memo string) ([]byte, uint64, error) {
	return []byte(from + " " + amount.String()), 0, nil
}

func (g *chainGrants) Submit(tx []byte, _ uint64) (paywall.PayoutState, error) {
	var from string
	var amount int64
	fmt.Sscanf(string(tx), "%s %d", &from, &amount)
	g.mu.Lock()
	g.collected[from] += amount
	g.mu.Unlock()
	return paywall.PayoutState{Status: paywall.PayoutConfirmed}, nil
}

// pullSeller offers every scheme, aether-pull included, at 0.02 AETH.
func pullSeller(t *testing.T, f *fakeChain) (*httptest.Server, *atomic.Int32, *paywall.Paywall, *chainGrants) {
	t.Helper()
	ledger, err := paywall.NewFileLedger("")
	require.NoError(t, err)
	g := &chainGrants{f: f, collected: map[string]int64{}, grantAt: map[string]int64{}}
	pw, err := paywall.New(paywall.Config{
		PayTo: sellerAddr(), Price: math.NewInt(20_000), Network: chainID, Lookup: f.lookup,
		Prepaid: &paywall.PrepaidConfig{Ledger: ledger, MinDeposit: math.NewInt(50_000)},
		Pull:    &paywall.PullConfig{Ledger: ledger, Collector: g, Grantee: collectorAddr(), Grants: g.get},
	})
	require.NoError(t, err)
	var served atomic.Int32
	srv := httptest.NewServer(pw.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		served.Add(1)
		fmt.Fprintf(w, "answer %d", served.Load())
	})))
	t.Cleanup(srv.Close)
	return srv, &served, pw, g
}

func fetchPullWith(t *testing.T, url, key, allowance string) (fetchPaidOutput, error) {
	t.Helper()
	_, out, err := toolFetchPaid(context.Background(), nil, fetchPaidInput{URL: url, MaxAmount: "0.05 AETH", IdempotencyKey: key,
		PullAllowance: allowance, Prepay: "0.1 AETH", TimeoutSeconds: 5})
	return out, err
}

func grantIn(t *testing.T, bz []byte) *authz.MsgGrant {
	t.Helper()
	msgs := decodeTx(t, bz).GetMsgs()
	require.Len(t, msgs, 1)
	mg, ok := msgs[0].(*authz.MsgGrant)
	require.True(t, ok, "a MsgGrant, got %T", msgs[0])
	return mg
}

func TestFetchPaid_PullGrantsOnceThenPaysInstantly(t *testing.T) {
	f := setupAgent(t)
	f.autoInclude = true
	srv, served, _, _ := pullSeller(t, f)

	out, err := fetchPullWith(t, srv.URL+"/q", "k1", "0.1 AETH")
	require.NoError(t, err)
	require.Equal(t, "paid", out.Status, out.Message)
	require.Equal(t, "answer 1", out.Body)
	require.Equal(t, paywall.SchemePull, out.Payment.Scheme, "preferred over prepay when both are offered")
	require.Equal(t, "0.02", out.Payment.Owed.Aeth)
	require.Equal(t, "0.08", out.Payment.Allowance.Aeth)

	require.Len(t, f.broadcasts, 1, "one transaction: the allowance")
	require.NotEmpty(t, out.Payment.GrantTxHash, "the paid output names the allowance it granted")
	mg := grantIn(t, f.broadcasts[0])
	w, _ := newWallet()
	agent, _ := getOrCreateAgentAccount(w)
	require.Equal(t, agent.Address, mg.Granter)
	require.Equal(t, collectorAddr(), mg.Grantee)
	var a banktypes.SendAuthorization
	require.NoError(t, proto.Unmarshal(mg.Grant.Authorization.Value, &a))
	require.Equal(t, "100000uaeth", sdk.Coins(a.SpendLimit).String())
	require.Equal(t, []string{sellerAddr()}, a.AllowList, "payable only to the seller")
	require.WithinDuration(t, time.Now().Add(pullGrantDays*24*time.Hour), *mg.Grant.Expiration, time.Minute)

	for i := 2; i <= 4; i++ {
		out, err = fetchPullWith(t, srv.URL+"/q", fmt.Sprintf("k%d", i), "0.1 AETH")
		require.NoError(t, err)
		require.Equal(t, fmt.Sprintf("answer %d", i), out.Body)
	}
	require.Equal(t, "0.08", out.Payment.Owed.Aeth)
	require.Len(t, f.broadcasts, 1, "no transaction per request")
	require.Equal(t, int64(80_000), spent(t), "each request counts against the daily budget; the allowance itself doesn't")
	require.Equal(t, int32(4), served.Load())

	// Retrying a served request pays nothing more.
	_, err = fetchPullWith(t, srv.URL+"/q", "k4", "0.1 AETH")
	requireCode(t, err, codePaymentAlreadyRedeemed)
	require.Equal(t, int64(80_000), spent(t))
}

func TestFetchPaid_PullRenewsAnAllowanceUsedUp(t *testing.T) {
	f := setupAgent(t)
	f.autoInclude = true
	srv, _, pw, _ := pullSeller(t, f)

	for _, k := range []string{"a", "b"} {
		out, err := fetchPullWith(t, srv.URL+"/q", k, "0.05 AETH")
		require.NoError(t, err)
		require.Equal(t, "paid", out.Status)
	}
	pw.CollectAll() // the seller collects 0.04: 0.01 of the allowance is left
	out, err := fetchPullWith(t, srv.URL+"/q", "c", "0.05 AETH")
	require.NoError(t, err)
	require.Equal(t, "paid", out.Status)
	require.Len(t, f.broadcasts, 2, "a fresh allowance, granted once")
	require.Equal(t, "0.03", out.Payment.Allowance.Aeth)
}

func TestFetchPaid_PullAllowanceMustCoverWhatsOwed(t *testing.T) {
	f := setupAgent(t)
	f.autoInclude = true
	srv, _, _, _ := pullSeller(t, f)
	for _, k := range []string{"a", "b"} {
		_, err := fetchPullWith(t, srv.URL+"/q", k, "0.05 AETH")
		require.NoError(t, err)
	}
	// 0.04 owed and not yet collected; 0.05 can't cover it plus 0.02.
	_, err := fetchPullWith(t, srv.URL+"/q", "c", "0.05 AETH")
	e := requireCode(t, err, codeInvalidArgument)
	require.Contains(t, e.Message, "at least 0.06 AETH")
	require.Len(t, f.broadcasts, 1)
}

func TestFetchPaid_PendingAllowanceIsResentNotRegranted(t *testing.T) {
	f := setupAgent(t)
	srv, served, _, _ := pullSeller(t, f)

	out, err := fetchPullWith(t, srv.URL+"/q", "a", "0.1 AETH")
	require.NoError(t, err)
	require.Equal(t, "payment_pending", out.Status)
	require.NotEmpty(t, out.Payment.GrantTxHash)
	out2, err := fetchPullWith(t, srv.URL+"/q", "b", "0.1 AETH")
	require.NoError(t, err)
	require.Equal(t, "payment_pending", out2.Status)
	require.Equal(t, out.Payment.GrantTxHash, out2.Payment.GrantTxHash)
	for _, bz := range f.broadcasts {
		require.Equal(t, f.broadcasts[0], bz, "only ever the one allowance transaction")
	}

	f.include(out.Payment.GrantTxHash, 0)
	for _, k := range []string{"a", "b"} {
		out, err = fetchPullWith(t, srv.URL+"/q", k, "0.1 AETH")
		require.NoError(t, err)
		require.Equal(t, "paid", out.Status)
	}
	require.Equal(t, int32(2), served.Load())
}

func TestFetchPaid_PullAllowanceNeedsTheOwnersApproval(t *testing.T) {
	f := setupAgent(t)
	f.autoInclude = true
	o := setupApprovals(t, 50_000)
	srv, served, _, _ := pullSeller(t, f)

	out, err := fetchPullWith(t, srv.URL+"/q", "a", "0.1 AETH")
	require.NoError(t, err)
	require.Equal(t, "approval_pending", out.Status)
	require.Empty(t, f.broadcasts, "nothing signed before approval")
	o.decide(t, "approve", out.ApprovalID)

	out, err = fetchPullWith(t, srv.URL+"/q", "a", "0.1 AETH")
	require.NoError(t, err)
	require.Equal(t, "paid", out.Status)
	require.Len(t, f.broadcasts, 1)
	require.Equal(t, int32(1), served.Load())
}

func TestFetchPaid_PullIsntUsedInGrantMode(t *testing.T) {
	f := setupAgent(t)
	f.autoInclude = true
	granter = granterAddr()
	f.grant = &wallet.SendGrant{Unlimited: true}
	srv, _, _, _ := pullSeller(t, f)
	_, _, _ = toolFetchPaid(context.Background(), nil, fetchPaidInput{URL: srv.URL + "/q", MaxAmount: "0.05 AETH", IdempotencyKey: "a",
		PullAllowance: "0.1 AETH", TimeoutSeconds: 5})
	require.NotEmpty(t, f.broadcasts, "it paid another way")
	for _, bz := range f.broadcasts {
		for _, m := range decodeTx(t, bz).GetMsgs() {
			_, isGrant := m.(*authz.MsgGrant)
			require.False(t, isGrant, "the agent can't grant its owner's funds away")
		}
	}
}

func decodeTxBytes(bz []byte) (sdk.Tx, error) {
	return app.MakeEncodingConfig().TxConfig.TxDecoder()(bz)
}
