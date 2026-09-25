package paywall

import (
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
	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	"github.com/stretchr/testify/require"

	"github.com/whoyoujoshin/aether/crypto/mldsa"
	"github.com/whoyoujoshin/aether/wallet"
)

// fakePayout signs "transactions" that are just their fields, and
// lets tests decide what submitting them does.
type fakePayout struct {
	mu      sync.Mutex
	signed  []string // "to amount memo seq"
	submits [][]byte
	seq     uint64
	state   func(tx []byte) (PayoutState, error)
}

func (f *fakePayout) Sign(to string, amount math.Int, memo string) ([]byte, uint64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s := fmt.Sprintf("%s %s %s %d", to, amount, memo, f.seq)
	f.signed = append(f.signed, s)
	f.seq++
	return []byte(s), f.seq - 1, nil
}

func (f *fakePayout) Submit(tx []byte, _ uint64) (PayoutState, error) {
	f.mu.Lock()
	f.submits = append(f.submits, tx)
	state := f.state
	f.mu.Unlock()
	if state == nil {
		return PayoutState{Status: PayoutPending}, nil
	}
	return state(tx)
}

func (f *fakePayout) counts() (signs, submits int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.signed), len(f.submits)
}

type withdrawHarness struct {
	t      *testing.T
	srv    *httptest.Server
	ledger *FileLedger
	payout *fakePayout
	now    time.Time
	path   string
}

func newWithdrawHarness(t *testing.T, ledgerPath string, payout Payout) *withdrawHarness {
	h := &withdrawHarness{t: t, now: time.Now(), path: ledgerPath}
	if fp, ok := payout.(*fakePayout); ok {
		h.payout = fp
	}
	led, err := NewFileLedger(ledgerPath)
	require.NoError(t, err)
	h.ledger = led
	p, err := New(Config{
		PayTo: seller(), Price: math.NewInt(10_000), Network: "aether-testnet-1",
		Lookup: (&fakeLedger{txs: map[string]*wallet.TransactionDetail{}}).lookup, Now: func() time.Time { return h.now },
		Prepaid: &PrepaidConfig{Ledger: led, MinDeposit: math.NewInt(30_000), Payout: payout},
	})
	require.NoError(t, err)
	mux := http.NewServeMux()
	mux.Handle(WithdrawPath, p.WithdrawHandler())
	mux.Handle(ManifestPath, p.ManifestHandler("svc", ""))
	mux.Handle("/api/", p.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "ok") })))
	h.srv = httptest.NewServer(mux)
	t.Cleanup(h.srv.Close)
	return h
}

func (h *withdrawHarness) fund(account string, uaeth int64) {
	h.t.Helper()
	_, _, err := h.ledger.Credit(fmt.Sprintf("DEP%d%s", uaeth, account[len(account)-6:]), account, math.NewInt(uaeth))
	require.NoError(h.t, err)
}

type withdrawCall struct {
	key    agentKey
	id     string
	body   string // default {"amount":"all"}
	path   string // what's signed; default WithdrawPath
	method string
}

func (h *withdrawHarness) withdraw(c withdrawCall) (int, WithdrawalResponse) {
	h.t.Helper()
	if c.body == "" {
		c.body = `{"amount":"all"}`
	}
	if c.path == "" {
		c.path = WithdrawPath
	}
	if c.method == "" {
		c.method = http.MethodPost
	}
	header, err := EncodePrepaidPayment(c.key.address, RequestFields{
		Network: "aether-testnet-1", PayTo: seller(), Host: strings.TrimPrefix(h.srv.URL, "http://"), Method: c.method, Path: c.path,
		Body: []byte(c.body), MaxPrice: math.ZeroInt(), Timestamp: h.now.Unix(), RequestID: c.id,
	}, c.key.sign)
	require.NoError(h.t, err)
	req, _ := http.NewRequest(http.MethodPost, h.srv.URL+WithdrawPath, strings.NewReader(c.body))
	req.Header.Set(HeaderPayment, header)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(h.t, err)
	defer resp.Body.Close()
	var out WithdrawalResponse
	bz, _ := io.ReadAll(resp.Body)
	require.NoError(h.t, json.Unmarshal(bz, &out), string(bz))
	return resp.StatusCode, out
}

func (h *withdrawHarness) balance(account string) int64 {
	b, err := h.ledger.Balance(account)
	require.NoError(h.t, err)
	return b.Int64()
}

func TestWithdraw_AllPaysBackOnceToTheAccount(t *testing.T) {
	fp := &fakePayout{}
	h := newWithdrawHarness(t, "", fp)
	k := newAgentKey(t)
	h.fund(k.address, 50_000)

	status, out := h.withdraw(withdrawCall{key: k, id: "w1"})
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, "pending", out.Status)
	require.Equal(t, "50000", out.Amount)
	require.Equal(t, "0.05", out.AmountAeth)
	require.Equal(t, "0", out.Balance)
	require.Equal(t, []string{k.address + " 50000 prepaid-withdrawal:w1 0"}, fp.signed, "paid to the signer itself")
	require.Equal(t, wallet.TxHash(wallet.SignedTx{Bytes: []byte(fp.signed[0])}), out.TxHash)

	// Asking again re-sends the same bytes: never a second payout.
	fp.state = func([]byte) (PayoutState, error) { return PayoutState{Status: PayoutConfirmed}, nil }
	status, again := h.withdraw(withdrawCall{key: k, id: "w1"})
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, "confirmed", again.Status)
	require.Equal(t, out.TxHash, again.TxHash)
	signs, submits := fp.counts()
	require.Equal(t, 1, signs)
	require.Equal(t, 2, submits)
	require.Equal(t, fp.submits[0], fp.submits[1])

	// Confirmed: the signed bytes are dropped, the record kept.
	h.ledger.mu.Lock()
	rec := h.ledger.state.Withdrawals[k.address+"/w1"]
	h.ledger.mu.Unlock()
	require.Equal(t, WithdrawalConfirmed, rec.Status)
	require.Nil(t, rec.TxBytes)

	// A third ask doesn't even submit.
	_, third := h.withdraw(withdrawCall{key: k, id: "w1"})
	require.Equal(t, "confirmed", third.Status)
	_, submits = fp.counts()
	require.Equal(t, 2, submits)

	// Nothing left to withdraw or spend.
	status, empty := h.withdraw(withdrawCall{key: k, id: "w2"})
	require.Equal(t, http.StatusConflict, status)
	require.Equal(t, ErrInsufficientBalance, empty.Error)
}

func TestWithdraw_Amounts(t *testing.T) {
	fp := &fakePayout{}
	h := newWithdrawHarness(t, "", fp)
	k := newAgentKey(t)
	h.fund(k.address, 100_000)

	status, out := h.withdraw(withdrawCall{key: k, id: "small", body: `{"amount":"29999"}`})
	require.Equal(t, http.StatusConflict, status)
	require.Equal(t, ErrBelowMinimum, out.Error)
	require.Equal(t, "100000", out.Balance)

	status, out = h.withdraw(withdrawCall{key: k, id: "big", body: `{"amount":"100001"}`})
	require.Equal(t, http.StatusConflict, status)
	require.Equal(t, ErrInsufficientBalance, out.Error)

	status, out = h.withdraw(withdrawCall{key: k, id: "part", body: `{"amount":"30000"}`})
	require.Equal(t, http.StatusOK, status, out.Message)
	require.Equal(t, "70000", out.Balance)

	// The same ID for another amount is refused, not paid again.
	status, out = h.withdraw(withdrawCall{key: k, id: "part", body: `{"amount":"40000"}`})
	require.Equal(t, http.StatusConflict, status)
	require.Equal(t, ErrWithdrawalIDReused, out.Error)
	require.Equal(t, int64(70_000), h.balance(k.address))

	// Under the minimum is fine when it's everything left.
	_, _, _, err := h.ledger.Charge(k.address, "req", math.NewInt(50_000), h.now.Add(time.Hour))
	require.NoError(t, err)
	status, out = h.withdraw(withdrawCall{key: k, id: "rest", body: `{"amount":"20000"}`})
	require.Equal(t, http.StatusOK, status, out.Message)
	require.Equal(t, "0", out.Balance)

	for _, bad := range []string{`{"amount":"0"}`, `{"amount":"-5"}`, `{"amount":"1.5"}`, `{"amount":"1 AETH"}`, `not json`} {
		status, out = h.withdraw(withdrawCall{key: k, id: "bad", body: bad})
		require.Equal(t, http.StatusBadRequest, status, bad)
		require.Equal(t, ErrInvalidPayment, out.Error, bad)
	}
	require.Len(t, fp.signed, 2)
}

func TestWithdraw_FailedPayoutReturnsTheBalance(t *testing.T) {
	fp := &fakePayout{state: func([]byte) (PayoutState, error) {
		return PayoutState{Status: PayoutFailed, Log: "insufficient funds"}, nil
	}}
	h := newWithdrawHarness(t, "", fp)
	k := newAgentKey(t)
	h.fund(k.address, 50_000)

	status, out := h.withdraw(withdrawCall{key: k, id: "w1"})
	require.Equal(t, http.StatusServiceUnavailable, status)
	require.Equal(t, ErrPayoutFailed, out.Error)
	require.Equal(t, "50000", out.Balance)
	require.Equal(t, int64(50_000), h.balance(k.address))

	// Once the seller can pay, the same ID works.
	fp.state = nil
	status, out = h.withdraw(withdrawCall{key: k, id: "w1"})
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, "pending", out.Status)
	require.Equal(t, "0", out.Balance)
}

func TestWithdraw_UnknownOutcomeIsNeverPaidTwice(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.json")
	fp := &fakePayout{state: func([]byte) (PayoutState, error) { return PayoutState{}, errors.New("node unreachable") }}
	h := newWithdrawHarness(t, path, fp)
	k := newAgentKey(t)
	h.fund(k.address, 50_000)

	status, out := h.withdraw(withdrawCall{key: k, id: "w1"})
	require.Equal(t, http.StatusAccepted, status)
	require.Equal(t, "pending", out.Status)
	require.Equal(t, "0", out.Balance, "set aside until the outcome is known")
	hash := out.TxHash

	// A restart (new process, same ledger file) re-sends the same bytes.
	fp2 := &fakePayout{seq: 1}
	h2 := newWithdrawHarness(t, path, fp2)
	status, out = h2.withdraw(withdrawCall{key: k, id: "w1"})
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, hash, out.TxHash)
	signs, submits := fp2.counts()
	require.Equal(t, 0, signs)
	require.Equal(t, 1, submits)
	require.Equal(t, fp.submits[0], fp2.submits[0])
}

func TestWithdraw_SpentSequenceIsResignedAfterGrace(t *testing.T) {
	fp := &fakePayout{}
	fp.state = func(tx []byte) (PayoutState, error) {
		if strings.HasSuffix(string(tx), " 0") {
			return PayoutState{Status: PayoutSequenceSpent}, nil
		}
		return PayoutState{Status: PayoutPending}, nil
	}
	h := newWithdrawHarness(t, "", fp)
	k := newAgentKey(t)
	h.fund(k.address, 50_000)

	_, out := h.withdraw(withdrawCall{key: k, id: "w1"})
	require.Equal(t, "pending", out.Status)
	first := out.TxHash

	h.now = h.now.Add(time.Minute) // within the grace: maybe the indexer is behind
	_, out = h.withdraw(withdrawCall{key: k, id: "w1"})
	require.Equal(t, first, out.TxHash)
	require.Len(t, fp.signed, 1)

	h.now = h.now.Add(2 * time.Minute)
	_, out = h.withdraw(withdrawCall{key: k, id: "w1"})
	require.Equal(t, "pending", out.Status)
	require.NotEqual(t, first, out.TxHash)
	require.Len(t, fp.signed, 2)
	require.Equal(t, int64(0), h.balance(k.address), "still one withdrawal's worth taken")
}

func TestWithdraw_OnlyTheAccountsOwnSignatureForThisPath(t *testing.T) {
	fp := &fakePayout{}
	h := newWithdrawHarness(t, "", fp)
	k, other := newAgentKey(t), newAgentKey(t)
	h.fund(k.address, 50_000)

	// A paid request's signature (another path) can't withdraw.
	status, out := h.withdraw(withdrawCall{key: k, id: "w1", path: "/api/x"})
	require.Equal(t, http.StatusForbidden, status)
	require.Equal(t, ErrInvalidSignature, out.Error)

	// Someone else's key withdraws their own (empty) balance, not k's.
	status, out = h.withdraw(withdrawCall{key: other, id: "w1"})
	require.Equal(t, http.StatusConflict, status)
	require.Equal(t, ErrInsufficientBalance, out.Error)

	// A stale signature is refused.
	signedAt := h.now
	h.now = h.now.Add(10 * time.Minute)
	header, _ := EncodePrepaidPayment(k.address, RequestFields{
		Network: "aether-testnet-1", PayTo: seller(), Host: strings.TrimPrefix(h.srv.URL, "http://"), Method: http.MethodPost,
		Path: WithdrawPath, Body: []byte(`{}`), MaxPrice: math.ZeroInt(), Timestamp: signedAt.Unix(), RequestID: "old",
	}, k.sign)
	req, _ := http.NewRequest(http.MethodPost, h.srv.URL+WithdrawPath, strings.NewReader(`{}`))
	req.Header.Set(HeaderPayment, header)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusForbidden, resp.StatusCode)

	resp, err = http.Get(h.srv.URL + WithdrawPath)
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode)

	require.Empty(t, fp.signed)
	require.Equal(t, int64(50_000), h.balance(k.address))
}

func TestWithdraw_AdvertisedOnlyWithAPayout(t *testing.T) {
	manifest := func(h *withdrawHarness) Manifest {
		resp, err := http.Get(h.srv.URL + ManifestPath)
		require.NoError(t, err)
		defer resp.Body.Close()
		var m Manifest
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&m))
		return m
	}
	with := newWithdrawHarness(t, "", &fakePayout{})
	require.Equal(t, WithdrawPath, manifest(with).WithdrawPath)
	resp, err := http.Get(with.srv.URL + "/api/x")
	require.NoError(t, err)
	var pr PaymentRequired
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&pr))
	resp.Body.Close()
	require.Equal(t, WithdrawPath, pr.Accepts[1].Extra.WithdrawPath)

	without := newWithdrawHarness(t, "", nil)
	require.Empty(t, manifest(without).WithdrawPath)
	k := newAgentKey(t)
	status, out := without.withdraw(withdrawCall{key: k, id: "w"})
	require.Equal(t, http.StatusNotImplemented, status)
	require.Equal(t, ErrWithdrawalsUnavailable, out.Error)
}

// fakeChain is a node for ChainPayout.
type fakeChain struct {
	seq       uint64
	broadcast func(hash string) (wallet.BroadcastResult, error)
	found     map[string]*wallet.TransactionDetail
	lookupErr error
}

func (c *fakeChain) GetAccountInfo(string) (uint64, uint64, error) { return 3, c.seq, nil }
func (c *fakeChain) BroadcastTx(s wallet.SignedTx) (wallet.BroadcastResult, error) {
	return c.broadcast(wallet.TxHash(s))
}
func (c *fakeChain) GetTransactionByHash(hash string) (*wallet.TransactionDetail, error) {
	if c.lookupErr != nil {
		return nil, c.lookupErr
	}
	if d := c.found[hash]; d != nil {
		return d, nil
	}
	return nil, wallet.ErrTransactionNotFound
}

func TestChainPayout(t *testing.T) {
	registry := codectypes.NewInterfaceRegistry()
	mldsa.RegisterInterfaces(registry)
	w, err := wallet.NewWallet("aetherd-test", "memory", t.TempDir(), codec.NewProtoCodec(registry))
	require.NoError(t, err)
	acc, _, err := w.CreateAccount("payout")
	require.NoError(t, err)
	chain := &fakeChain{seq: 5, found: map[string]*wallet.TransactionDetail{}}
	c := &ChainPayout{Wallet: w, KeyName: "payout", Address: acc.Address, Chain: chain, ChainID: "aether-testnet-1"}
	to := newAgentKey(t).address

	// Two payouts before a block: consecutive sequences.
	tx1, seq1, err := c.Sign(to, math.NewInt(1), "a")
	require.NoError(t, err)
	tx2, seq2, err := c.Sign(to, math.NewInt(1), "b")
	require.NoError(t, err)
	require.Equal(t, []uint64{5, 6}, []uint64{seq1, seq2})

	result := func(code uint32) func(string) (wallet.BroadcastResult, error) {
		return func(h string) (wallet.BroadcastResult, error) {
			return wallet.BroadcastResult{TxHash: h, Code: code, Codespace: "sdk"}, nil
		}
	}
	chain.broadcast = result(0)
	st, err := c.Submit(tx1, seq1)
	require.NoError(t, err)
	require.Equal(t, PayoutPending, st.Status)

	chain.broadcast = result(codeTxInMempool)
	st, _ = c.Submit(tx1, seq1)
	require.Equal(t, PayoutPending, st.Status)

	chain.found[wallet.TxHash(wallet.SignedTx{Bytes: tx1})] = &wallet.TransactionDetail{Code: 0}
	chain.broadcast = func(string) (wallet.BroadcastResult, error) {
		t.Fatal("found: no broadcast needed")
		return wallet.BroadcastResult{}, nil
	}
	st, _ = c.Submit(tx1, seq1)
	require.Equal(t, PayoutConfirmed, st.Status)

	chain.broadcast = result(codeWrongSequence)
	st, _ = c.Submit(tx2, seq2)
	require.Equal(t, PayoutSequenceSpent, st.Status)
	// Local sequences reset: the next payout starts from the chain's.
	chain.seq = 6
	_, seq3, err := c.Sign(to, math.NewInt(1), "c")
	require.NoError(t, err)
	require.Equal(t, uint64(6), seq3)

	chain.broadcast = func(h string) (wallet.BroadcastResult, error) {
		return wallet.BroadcastResult{TxHash: h, Code: 5, Codespace: "sdk", RawLog: "insufficient funds"}, nil
	}
	st, _ = c.Submit(tx2, seq2)
	require.Equal(t, PayoutFailed, st.Status)

	chain.found[wallet.TxHash(wallet.SignedTx{Bytes: tx2})] = &wallet.TransactionDetail{Code: 11, RawLog: "out of gas"}
	st, _ = c.Submit(tx2, seq2)
	require.Equal(t, PayoutFailed, st.Status, "failed in a block moves nothing")

	chain.lookupErr = errors.New("unreachable")
	_, err = c.Submit(tx1, seq1)
	require.Error(t, err, "unknown is an error, never failed")
}
