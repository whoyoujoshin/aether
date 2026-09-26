package paywall

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	"github.com/whoyoujoshin/aether/wallet"
)

// fakeGrants is the chain's x/authz state as the paywall sees it.
type fakeGrants struct {
	mu      sync.Mutex
	grants  map[string]*wallet.SendGrant // granter -> grant to the collector
	lookups int
}

func (f *fakeGrants) get(granter, grantee string) (*wallet.SendGrant, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lookups++
	g := f.grants[granter]
	if g == nil {
		return nil, fmt.Errorf("%w from %s to %s", wallet.ErrGrantNotFound, granter, grantee)
	}
	cp := *g
	return &cp, nil
}

func (f *fakeGrants) set(granter string, g *wallet.SendGrant) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if g == nil {
		delete(f.grants, granter)
	} else {
		f.grants[granter] = g
	}
}

// fakeCollector records collections and reports what the test says.
type fakeCollector struct {
	mu      sync.Mutex
	signed  []string // "from amount memo"
	submits int
	state   func() (PayoutState, error)
}

func (f *fakeCollector) SignCollect(from string, amount math.Int, memo string) ([]byte, uint64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s := fmt.Sprintf("%s %s %s", from, amount, memo)
	f.signed = append(f.signed, s)
	return []byte(s), uint64(len(f.signed)), nil
}

func (f *fakeCollector) Submit(tx []byte, _ uint64) (PayoutState, error) {
	f.mu.Lock()
	f.submits++
	state := f.state
	f.mu.Unlock()
	if state == nil {
		return PayoutState{Status: PayoutPending}, nil
	}
	return state()
}

func (f *fakeCollector) respond(st string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.state = func() (PayoutState, error) { return PayoutState{Status: st, Log: "chain says " + st}, err }
}

type pullHarness struct {
	*harness
	p         *Paywall
	ledger    *FileLedger
	grants    *fakeGrants
	collector *fakeCollector
	payTo     string
	grantee   string
}

func newPullHarness(t *testing.T, ledgerPath string, credit int64) *pullHarness {
	// Real time: FileLedger forgets charged request IDs by the wall clock.
	h := &harness{t: t, ledger: &fakeLedger{txs: map[string]*wallet.TransactionDetail{}}, now: time.Now().Truncate(time.Second)}
	led, err := NewFileLedger(ledgerPath)
	require.NoError(t, err)
	ph := &pullHarness{harness: h, ledger: led, grants: &fakeGrants{grants: map[string]*wallet.SendGrant{}}, collector: &fakeCollector{},
		payTo: seller(), grantee: sdk.AccAddress("collector___________").String()}
	cfg := Config{
		PayTo: ph.payTo, Price: math.NewInt(10_000), Network: "aether-testnet-1",
		Lookup: h.ledger.lookup, Now: func() time.Time { return h.now },
		Pull: &PullConfig{Ledger: led, Collector: ph.collector, Grantee: ph.grantee, Grants: ph.grants.get},
	}
	if credit > 0 {
		cfg.Pull.Credit = math.NewInt(credit)
	}
	ph.p, err = New(cfg)
	require.NoError(t, err)
	mux := http.NewServeMux()
	mux.Handle("/api/", ph.p.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if h.fail {
			http.Error(w, "broke", http.StatusInternalServerError)
			return
		}
		body, _ := io.ReadAll(r.Body)
		h.served++
		fmt.Fprintf(w, "ok %s %s", r.URL.Path, body)
	})))
	h.srv = httptest.NewServer(mux)
	t.Cleanup(h.srv.Close)
	return ph
}

// grant has buyer grant the collector limit uaeth, payable to payTo.
func (h *pullHarness) grant(buyer agentKey, limit int64) {
	exp := h.now.Add(7 * 24 * time.Hour)
	h.grants.set(buyer.address, &wallet.SendGrant{SpendLimit: sdk.NewCoins(sdk.NewInt64Coin("uaeth", limit)), AllowList: []string{h.payTo}, Expiration: &exp})
	h.p.forgetGrant(buyer.address)
}

func (h *pullHarness) do(key agentKey, requestID string, sign func(RequestFields) []byte) (*http.Response, string) {
	h.t.Helper()
	host := strings.TrimPrefix(h.srv.URL, "http://")
	f := RequestFields{Network: "aether-testnet-1", PayTo: h.payTo, Host: host, Method: http.MethodPost, Path: "/api/x",
		Body: []byte("hi"), MaxPrice: math.NewInt(10_000), Timestamp: h.now.Unix(), RequestID: requestID}
	var header string
	var err error
	if sign == nil {
		header, err = EncodePullPayment(key.address, f, key.sign)
	} else {
		// Sign some other message, e.g. the prepaid one, but present it as pull.
		sig, pub, serr := key.sign(sign(f))
		require.NoError(h.t, serr)
		header, err = encodePayment(SchemePull, f.Network, PullPayment{Account: key.address, PubKey: b64(pub), Timestamp: f.Timestamp,
			RequestID: requestID, MaxPrice: "10000", Signature: b64(sig)})
	}
	require.NoError(h.t, err)
	req, _ := http.NewRequest(http.MethodPost, h.srv.URL+"/api/x", strings.NewReader("hi"))
	req.Header.Set(HeaderPayment, header)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(h.t, err)
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp, string(body)
}

func (h *pullHarness) served(key agentKey, requestID string) SettlementResponse {
	h.t.Helper()
	resp, body := h.do(key, requestID, nil)
	require.Equal(h.t, http.StatusOK, resp.StatusCode, body)
	var s SettlementResponse
	require.NoError(h.t, DecodeHeader(resp.Header.Get(HeaderPaymentResponse), &s))
	return s
}

func (h *pullHarness) refused(key agentKey, requestID, want string) PaymentRequired {
	h.t.Helper()
	resp, body := h.do(key, requestID, nil)
	require.Equal(h.t, http.StatusPaymentRequired, resp.StatusCode, body)
	var pr PaymentRequired
	require.NoError(h.t, json.Unmarshal([]byte(body), &pr))
	require.Equal(h.t, want, pr.Error, pr.Message)
	return pr
}

func (h *pullHarness) account(key agentKey) PullAccount {
	a, err := h.ledger.PullAccount(key.address)
	require.NoError(h.t, err)
	return a
}

func b64(b []byte) string { return base64.StdEncoding.EncodeToString(b) }

func TestPull_ServesInstantlyUnderAGrantAndAccrues(t *testing.T) {
	h := newPullHarness(t, "", 0)
	buyer := newAgentKey(t)

	pr := h.refused(buyer, "r0", ErrNoGrant)
	var offer *PaymentRequirements
	for i := range pr.Accepts {
		if pr.Accepts[i].Scheme == SchemePull {
			offer = &pr.Accepts[i]
		}
	}
	require.NotNil(t, offer, "the 402 offers aether-pull")
	require.Equal(t, h.grantee, offer.Extra.Grantee)
	require.Equal(t, "1000000", offer.Extra.Credit, "default credit: 100 requests")
	require.Contains(t, pr.Message, h.grantee)

	h.grant(buyer, 50_000)
	s := h.served(buyer, "r1")
	require.Equal(t, buyer.address, s.Payer)
	require.Equal(t, "10000", s.Owed)
	require.Equal(t, "40000", s.Allowance)
	s = h.served(buyer, "r2")
	require.Equal(t, "20000", s.Owed)
	require.Equal(t, 2, h.harness.served)
	require.Empty(t, h.collector.signed, "nothing is collected per request")

	h.refused(buyer, "r2", ErrAlreadyRedeemed)
	require.Equal(t, int64(20_000), h.account(buyer).Accrued.Int64(), "a replay charges nothing")
}

func TestPull_SignaturesAreDomainSeparated(t *testing.T) {
	h := newPullHarness(t, "", 0)
	buyer := newAgentKey(t)
	h.grant(buyer, 50_000)
	resp, body := h.do(buyer, "r1", SigningMessage) // a prepaid signature, presented as pull
	require.Equal(t, http.StatusPaymentRequired, resp.StatusCode)
	require.Contains(t, body, ErrInvalidSignature)
	require.NotEqual(t, SigningMessage(RequestFields{MaxPrice: math.OneInt()}), PullSigningMessage(RequestFields{MaxPrice: math.OneInt()}))
	h.served(buyer, "r2")
}

func TestPull_RefusesAllowancesThatCantPay(t *testing.T) {
	h := newPullHarness(t, "", 0)
	buyer := newAgentKey(t)

	exp := h.now.Add(time.Hour)
	h.grants.set(buyer.address, &wallet.SendGrant{SpendLimit: sdk.NewCoins(sdk.NewInt64Coin("uaeth", 50_000)), AllowList: []string{sdk.AccAddress("somebody_else_______").String()}, Expiration: &exp})
	require.Contains(t, h.refused(buyer, "a", ErrNoGrant).Message, "doesn't allow paying "+h.payTo)

	soon := h.now.Add(90 * time.Second)
	h.grants.set(buyer.address, &wallet.SendGrant{SpendLimit: sdk.NewCoins(sdk.NewInt64Coin("uaeth", 50_000)), Expiration: &soon})
	h.p.forgetGrant(buyer.address)
	require.Contains(t, h.refused(buyer, "b", ErrNoGrant).Message, "expires too soon")

	h.grant(buyer, 25_000)
	h.served(buyer, "c")
	h.served(buyer, "d")
	pr := h.refused(buyer, "e", ErrGrantTooLow)
	require.Contains(t, pr.Message, "owe 20000")
	h.grants.set(buyer.address, &wallet.SendGrant{Unlimited: true})
	h.p.forgetGrant(buyer.address)
	require.Empty(t, h.served(buyer, "f").Allowance, "unlimited: no allowance figure")
}

func TestPull_CreditCapsWhatsOwedUntilCollected(t *testing.T) {
	h := newPullHarness(t, "", 30_000)
	buyer := newAgentKey(t)
	h.grant(buyer, 1_000_000)
	for _, id := range []string{"1", "2", "3"} {
		h.served(buyer, id)
	}
	pr := h.refused(buyer, "4", ErrSettling)
	require.Equal(t, "30000", pr.Accepts[len(pr.Accepts)-1].Extra.Owed)

	h.p.CollectAll() // signs and sends 30000; not in a block yet
	require.Equal(t, []string{buyer.address + " 30000 " + CollectionMemoPrefix + mustOpen(t, h, buyer).ID}, h.collector.signed)
	a := h.account(buyer)
	require.True(t, a.Accrued.IsZero())
	require.Equal(t, int64(30_000), a.InFlight.Int64())
	h.refused(buyer, "4", ErrSettling) // still being collected: it counts against the credit

	h.collector.respond(PayoutConfirmed, nil)
	h.p.CollectAll()
	require.Len(t, h.collector.signed, 1, "confirmed without signing again")
	require.True(t, h.account(buyer).Owed().IsZero())
	h.served(buyer, "4")
	require.Equal(t, WithdrawalConfirmed, onlyCollection(t, h).Status)
}

func mustOpen(t *testing.T, h *pullHarness, k agentKey) Collection {
	c, ok, err := h.ledger.OpenCollection(k.address, "unused", h.now)
	require.NoError(t, err)
	require.True(t, ok)
	return c
}

func onlyCollection(t *testing.T, h *pullHarness) Collection {
	require.Len(t, h.ledger.state.Collections, 1)
	for _, c := range h.ledger.state.Collections {
		return c
	}
	return Collection{}
}

func TestPull_CollectionIsSavedBeforeBroadcastAndOnlyResent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.json")
	h := newPullHarness(t, path, 0)
	buyer := newAgentKey(t)
	h.grant(buyer, 100_000)
	h.served(buyer, "1")
	h.served(buyer, "2")

	h.collector.respond("", errors.New("node unreachable"))
	h.p.CollectAll()
	require.Len(t, h.collector.signed, 1)

	// A restart: the signed collection comes back from disk and is
	// re-sent as is.
	reopened, err := NewFileLedger(path)
	require.NoError(t, err)
	c, ok, err := reopened.OpenCollection(buyer.address, "new-id", h.now)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, WithdrawalPending, c.Status)
	require.Equal(t, "20000", c.Amount)
	require.NotEmpty(t, c.TxBytes)

	h.collector.respond(PayoutConfirmed, nil)
	h.p.CollectAll()
	require.Len(t, h.collector.signed, 1, "never signed a second time")
	require.True(t, h.account(buyer).Owed().IsZero())
}

func TestPull_FailedCollectionBlocksUntilAGrantCoversIt(t *testing.T) {
	h := newPullHarness(t, "", 0)
	buyer := newAgentKey(t)
	h.grant(buyer, 100_000)
	h.served(buyer, "1")
	h.served(buyer, "2")

	// The buyer revokes before the collection lands.
	h.grants.set(buyer.address, nil)
	h.collector.respond(PayoutFailed, nil)
	h.p.CollectAll()
	a := h.account(buyer)
	require.Equal(t, int64(20_000), a.Unpaid.Int64())
	h.refused(buyer, "3", ErrNoGrant)

	h.grant(buyer, 25_000) // covers the 20000 owed but not another request
	require.Contains(t, h.refused(buyer, "3", ErrPullUnpaid).Message, "20000")
	h.grant(buyer, 100_000)
	s := h.served(buyer, "3")
	require.Equal(t, "30000", s.Owed)
	a = h.account(buyer)
	require.True(t, a.Unpaid.IsZero())
	require.Equal(t, int64(30_000), a.Accrued.Int64(), "the debt is collected with the next batch")

	h.collector.respond(PayoutConfirmed, nil)
	h.p.CollectAll()
	require.Equal(t, buyer.address+" 30000 ", h.collector.signed[1][:len(buyer.address)+7])
}

func TestPull_ServerErrorsArentCharged(t *testing.T) {
	h := newPullHarness(t, "", 0)
	buyer := newAgentKey(t)
	h.grant(buyer, 100_000)
	h.harness.fail = true
	resp, _ := h.do(buyer, "1", nil)
	require.Equal(t, http.StatusInternalServerError, resp.StatusCode)
	require.True(t, h.account(buyer).Owed().IsZero())
	h.harness.fail = false
	h.served(buyer, "1") // the same request can be retried
}

func TestPull_GrantLookupsAreCachedBriefly(t *testing.T) {
	h := newPullHarness(t, "", 0)
	buyer := newAgentKey(t)
	h.grant(buyer, 100_000)
	h.served(buyer, "1")
	n := h.grants.lookups
	h.served(buyer, "2")
	require.Equal(t, n, h.grants.lookups)
	h.now = h.now.Add(grantCacheTTL + time.Second)
	h.served(buyer, "3")
	require.Equal(t, n+1, h.grants.lookups)
}

// A buyer who was refused and then granted, or raised, its allowance is
// served at once, not after the cache expires.
func TestPull_NewAllowancesCountImmediately(t *testing.T) {
	h := newPullHarness(t, "", 0)
	buyer := newAgentKey(t)
	h.refused(buyer, "1", ErrNoGrant)
	exp := h.now.Add(7 * 24 * time.Hour)
	set := func(limit int64) {
		h.grants.set(buyer.address, &wallet.SendGrant{SpendLimit: sdk.NewCoins(sdk.NewInt64Coin("uaeth", limit)), AllowList: []string{h.payTo}, Expiration: &exp})
	}
	set(10_000) // no forgetGrant: the paywall must look again by itself
	h.served(buyer, "1")
	h.refused(buyer, "2", ErrGrantTooLow)
	set(100_000)
	h.served(buyer, "2")
}
