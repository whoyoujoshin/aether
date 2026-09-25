package paywall

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	"github.com/whoyoujoshin/aether/crypto/mldsa"
	"github.com/whoyoujoshin/aether/wallet"
)

type agentKey struct {
	sk      *mldsa.PrivKey
	address string
}

func newAgentKey(t *testing.T) agentKey {
	sk, err := mldsa.GenPrivKey()
	require.NoError(t, err)
	return agentKey{sk: sk, address: sdk.AccAddress(sk.PubKey().Address()).String()}
}

func (k agentKey) sign(msg []byte) ([]byte, []byte, error) {
	sig, err := k.sk.Sign(msg)
	return sig, k.sk.PubKey().Bytes(), err
}

type prepaidHarness struct {
	*harness
	ledger *FileLedger
	payTo  string
}

func newPrepaidHarness(t *testing.T, payTo string, ledgerPath string) *prepaidHarness {
	h := &harness{t: t, ledger: &fakeLedger{txs: map[string]*wallet.TransactionDetail{}}, now: time.Now()}
	led, err := NewFileLedger(ledgerPath)
	require.NoError(t, err)
	p, err := New(Config{
		PayTo: payTo, Price: math.NewInt(10_000), Network: "aether-testnet-1",
		Lookup: h.ledger.lookup, Now: func() time.Time { return h.now },
		Prepaid: &PrepaidConfig{Ledger: led, MinDeposit: math.NewInt(30_000)},
	})
	require.NoError(t, err)
	mux := http.NewServeMux()
	mux.Handle("/api/", p.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	return &prepaidHarness{harness: h, ledger: led, payTo: payTo}
}

type signedReq struct {
	key       agentKey
	path      string
	body      string
	requestID string
	deposit   string
	maxPrice  int64
	at        time.Time
	// tamper, if set, changes what's signed relative to what's sent.
	tamper func(*RequestFields)
	// tamperPayload edits the header after signing.
	tamperPayload func(*PrepaidPayment)
}

func (h *prepaidHarness) do(r signedReq) (*http.Response, string) {
	h.t.Helper()
	if r.maxPrice == 0 {
		r.maxPrice = 10_000
	}
	if r.at.IsZero() {
		r.at = h.now
	}
	host := strings.TrimPrefix(h.srv.URL, "http://")
	f := RequestFields{
		Network: "aether-testnet-1", PayTo: h.payTo, Host: host, Method: http.MethodPost, Path: r.path,
		Body: []byte(r.body), MaxPrice: math.NewInt(r.maxPrice), Timestamp: r.at.Unix(), RequestID: r.requestID, DepositTx: r.deposit,
	}
	if r.tamper != nil {
		r.tamper(&f)
	}
	header, err := EncodePrepaidPayment(r.key.address, f, r.key.sign)
	require.NoError(h.t, err)
	if r.tamperPayload != nil {
		var pay PaymentPayload
		require.NoError(h.t, DecodeHeader(header, &pay))
		var pp PrepaidPayment
		require.NoError(h.t, json.Unmarshal(pay.Payload, &pp))
		r.tamperPayload(&pp)
		pay.Payload, _ = json.Marshal(pp)
		header, _ = EncodeHeader(pay)
	}
	req, _ := http.NewRequest(http.MethodPost, h.srv.URL+r.path, strings.NewReader(r.body))
	req.Header.Set(HeaderPayment, header)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(h.t, err)
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp, string(body)
}

func (h *prepaidHarness) refused(r signedReq, want string) PaymentRequired {
	h.t.Helper()
	resp, body := h.do(r)
	require.Equal(h.t, http.StatusPaymentRequired, resp.StatusCode, body)
	var pr PaymentRequired
	require.NoError(h.t, json.Unmarshal([]byte(body), &pr))
	require.Equal(h.t, want, pr.Error, pr.Message)
	return pr
}

func (h *prepaidHarness) deposit(hash, beneficiary string, uaeth int64) {
	h.ledger2().pay(hash, DepositMemoPrefix+beneficiary, h.payTo, uaeth, h.now, 0)
}

func (h *prepaidHarness) ledger2() *fakeLedger { return h.harness.ledger }

func balanceOf(t *testing.T, resp *http.Response) string {
	var s SettlementResponse
	require.NoError(t, DecodeHeader(resp.Header.Get(HeaderPaymentResponse), &s))
	return s.Balance
}

func TestPrepaid_DepositOnceThenPayPerRequestInstantly(t *testing.T) {
	h := newPrepaidHarness(t, seller(), "")
	agent := newAgentKey(t)

	// The 402 offers both schemes.
	resp, body := h.get("/api/x", "")
	require.Equal(t, http.StatusPaymentRequired, resp.StatusCode)
	var pr PaymentRequired
	require.NoError(t, json.Unmarshal([]byte(body), &pr))
	require.Len(t, pr.Accepts, 2)
	pre := pr.Accepts[1]
	require.Equal(t, SchemePrepaid, pre.Scheme)
	require.Equal(t, "prepaid:<address>", pre.Extra.DepositMemo)
	require.Equal(t, "30000", pre.Extra.MinDeposit)

	// No balance yet.
	pr = h.refused(signedReq{key: agent, path: "/api/x", requestID: "r0"}, ErrInsufficientBalance)
	require.Equal(t, "0", pr.Accepts[1].Extra.Balance)

	// Deposit 5 requests' worth; the first request after it names it.
	h.deposit("DEP1", agent.address, 50_000)
	resp, body = h.do(signedReq{key: agent, path: "/api/x", body: "q1", requestID: "r1", deposit: "DEP1"})
	require.Equal(t, http.StatusOK, resp.StatusCode, body)
	require.Equal(t, "ok /api/x q1", body)
	require.Equal(t, "40000", balanceOf(t, resp))

	// Then no chain involvement at all: no lookups, just signatures.
	h.ledger2().err = fmt.Errorf("node down")
	for i := 2; i <= 5; i++ {
		resp, _ = h.do(signedReq{key: agent, path: "/api/x", requestID: fmt.Sprintf("r%d", i)})
		require.Equal(t, http.StatusOK, resp.StatusCode)
	}
	require.Equal(t, "0", balanceOf(t, resp))
	h.refused(signedReq{key: agent, path: "/api/x", requestID: "r6"}, ErrInsufficientBalance)
	require.Equal(t, 5, h.served)
}

func TestPrepaid_NeverChargesTwice(t *testing.T) {
	h := newPrepaidHarness(t, seller(), "")
	agent := newAgentKey(t)
	h.deposit("DEP", agent.address, 100_000)

	req := signedReq{key: agent, path: "/api/x", requestID: "same", deposit: "DEP"}
	resp, _ := h.do(req)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	// A retry after a lost response, or a replayed header, isn't
	// charged again -- and the deposit isn't credited twice either.
	h.refused(req, ErrAlreadyRedeemed)
	bal, _ := h.ledger.Balance(agent.address)
	require.Equal(t, "90000", bal.String())
	require.Equal(t, 1, h.served)
}

func TestPrepaid_SignatureCoversEverything(t *testing.T) {
	h := newPrepaidHarness(t, seller(), "")
	agent, other := newAgentKey(t), newAgentKey(t)
	h.deposit("DEP", agent.address, 100_000)
	resp, _ := h.do(signedReq{key: agent, path: "/api/x", requestID: "setup", deposit: "DEP"})
	require.Equal(t, http.StatusOK, resp.StatusCode)

	for name, tamper := range map[string]func(*RequestFields){
		"path":   func(f *RequestFields) { f.Path = "/api/other" },
		"body":   func(f *RequestFields) { f.Body = []byte("different") },
		"host":   func(f *RequestFields) { f.Host = "evil.example" },
		"payee":  func(f *RequestFields) { f.PayTo = buyer() },
		"method": func(f *RequestFields) { f.Method = http.MethodGet },
	} {
		h.refused(signedReq{key: agent, path: "/api/x", body: "b", requestID: "t-" + name, tamper: tamper}, ErrInvalidSignature)
	}
	// Signed for another network: refused before the signature is read.
	h.refused(signedReq{key: agent, path: "/api/x", requestID: "net", tamper: func(f *RequestFields) { f.Network = "other-chain" }}, ErrUnsupportedScheme)
	for name, tamper := range map[string]func(*PrepaidPayment){
		"maxPrice":  func(p *PrepaidPayment) { p.MaxPrice = "99999" },
		"requestId": func(p *PrepaidPayment) { p.RequestID = "reused-" + p.RequestID },
		"timestamp": func(p *PrepaidPayment) { p.Timestamp++ },
		"deposit":   func(p *PrepaidPayment) { p.DepositTx = "DEP" },
	} {
		h.refused(signedReq{key: agent, path: "/api/x", requestID: "p-" + name, tamperPayload: tamper}, ErrInvalidSignature)
	}

	// Someone else's key can't spend this account.
	forged := signedReq{key: other, path: "/api/x", requestID: "forged"}
	resp, body := h.do(forged)
	require.Equal(t, http.StatusPaymentRequired, resp.StatusCode, body)
	hdr, err := EncodePrepaidPayment(agent.address, RequestFields{Network: "aether-testnet-1", PayTo: seller(),
		Host: strings.TrimPrefix(h.srv.URL, "http://"), Method: http.MethodPost, Path: "/api/x", MaxPrice: math.NewInt(10_000),
		Timestamp: h.now.Unix(), RequestID: "impersonate"}, other.sign)
	require.NoError(t, err)
	req, _ := http.NewRequest(http.MethodPost, h.srv.URL+"/api/x", nil)
	req.Header.Set(HeaderPayment, hdr)
	r2, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	var pr PaymentRequired
	require.NoError(t, json.NewDecoder(r2.Body).Decode(&pr))
	r2.Body.Close()
	require.Equal(t, ErrInvalidSignature, pr.Error, "a key must match the account it spends")

	h.refused(signedReq{key: agent, path: "/api/x", requestID: "old", at: h.now.Add(-6 * time.Minute)}, ErrStaleRequest)
	h.refused(signedReq{key: agent, path: "/api/x", requestID: "cheap", maxPrice: 9_999}, ErrPriceAboveMax)

	bal, _ := h.ledger.Balance(agent.address)
	require.Equal(t, "90000", bal.String(), "nothing refused was charged")
}

func TestPrepaid_DepositsCreditOnlyTheNamedAccount(t *testing.T) {
	h := newPrepaidHarness(t, seller(), "")
	victim, thief := newAgentKey(t), newAgentKey(t)

	// A thief who saw the victim's deposit on chain presents it with
	// their own request: the victim is credited, the thief is not.
	h.deposit("VICTIMDEP", victim.address, 50_000)
	h.refused(signedReq{key: thief, path: "/api/x", requestID: "steal", deposit: "VICTIMDEP"}, ErrInsufficientBalance)
	vb, _ := h.ledger.Balance(victim.address)
	tb, _ := h.ledger.Balance(thief.address)
	require.Equal(t, "50000", vb.String())
	require.Equal(t, "0", tb.String())

	// Deposits below the minimum, to someone else, or with a bad memo don't count.
	h.ledger2().pay("SMALL", DepositMemoPrefix+thief.address, seller(), 29_999, h.now, 0)
	h.refused(signedReq{key: thief, path: "/api/x", requestID: "a", deposit: "SMALL"}, ErrInsufficient)
	h.ledger2().pay("ELSEWHERE", DepositMemoPrefix+thief.address, buyer(), 50_000, h.now, 0)
	h.refused(signedReq{key: thief, path: "/api/x", requestID: "b", deposit: "ELSEWHERE"}, ErrInsufficient)
	h.ledger2().pay("NOMEMO", "hello", seller(), 50_000, h.now, 0)
	h.refused(signedReq{key: thief, path: "/api/x", requestID: "c", deposit: "NOMEMO"}, ErrInvalidDeposit)
	h.refused(signedReq{key: thief, path: "/api/x", requestID: "d", deposit: "NOTYET"}, ErrNotConfirmed)
}

func TestPrepaid_ServerErrorRefunds(t *testing.T) {
	h := newPrepaidHarness(t, seller(), "")
	agent := newAgentKey(t)
	h.deposit("DEP", agent.address, 30_000)
	h.fail = true
	resp, _ := h.do(signedReq{key: agent, path: "/api/x", requestID: "r", deposit: "DEP"})
	require.Equal(t, http.StatusInternalServerError, resp.StatusCode)
	bal, _ := h.ledger.Balance(agent.address)
	require.Equal(t, "30000", bal.String())

	h.fail = false
	resp, _ = h.do(signedReq{key: agent, path: "/api/x", requestID: "r"})
	require.Equal(t, http.StatusOK, resp.StatusCode, "the same request can be retried after a server failure")
}

func TestFileLedger_SurvivesRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.json")
	h := newPrepaidHarness(t, seller(), path)
	agent := newAgentKey(t)
	h.deposit("DEP", agent.address, 50_000)
	resp, _ := h.do(signedReq{key: agent, path: "/api/x", requestID: "r1", deposit: "DEP"})
	require.Equal(t, http.StatusOK, resp.StatusCode)

	reopened, err := NewFileLedger(path)
	require.NoError(t, err)
	bal, err := reopened.Balance(agent.address)
	require.NoError(t, err)
	require.Equal(t, "40000", bal.String())
	_, credited, err := reopened.Credit("DEP", agent.address, math.NewInt(50_000))
	require.NoError(t, err)
	require.False(t, credited, "a deposit is credited once, across restarts")
	_, _, fresh, err := reopened.Charge(agent.address, "r1", math.NewInt(1), time.Now().Add(time.Hour))
	require.NoError(t, err)
	require.False(t, fresh, "a charged request stays charged across restarts")
}

func TestSigningMessage_CannotBeATransaction(t *testing.T) {
	msg := SigningMessage(RequestFields{MaxPrice: math.NewInt(1)})
	require.NotEqual(t, byte(0x0a), msg[0], "protobuf SignDoc sign bytes start with 0x0a")
	require.NotEqual(t, byte('{'), msg[0], "amino JSON sign bytes start with '{'")
}
