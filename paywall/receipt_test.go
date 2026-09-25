package paywall

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"cosmossdk.io/math"
	"github.com/stretchr/testify/require"

	"github.com/whoyoujoshin/aether/wallet"
)

type receiptHarness struct {
	t      *testing.T
	ledger *fakeLedger
	payee  agentKey
	srv    *httptest.Server
	now    time.Time
	status int
	body   string
}

func newReceiptHarness(t *testing.T, receipts func(payee agentKey) *ReceiptConfig, handler ...http.Handler) *receiptHarness {
	h := &receiptHarness{t: t, ledger: &fakeLedger{txs: map[string]*wallet.TransactionDetail{}}, payee: newAgentKey(t), now: time.Now(), status: 200, body: "sunny"}
	led, err := NewFileLedger("")
	require.NoError(t, err)
	p, err := New(Config{
		PayTo: h.payee.address, Price: math.NewInt(10_000), Network: "aether-testnet-1",
		Lookup: h.ledger.lookup, Now: func() time.Time { return h.now },
		Prepaid:  &PrepaidConfig{Ledger: led, MinDeposit: math.NewInt(30_000)},
		Receipts: receipts(h.payee),
	})
	require.NoError(t, err)
	var next http.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		if h.status != 200 {
			w.WriteHeader(h.status)
		}
		fmt.Fprint(w, h.body)
	})
	if len(handler) > 0 {
		next = handler[0]
	}
	h.srv = httptest.NewServer(p.Middleware(next))
	t.Cleanup(h.srv.Close)
	return h
}

func payeeSigns(k agentKey) *ReceiptConfig { return &ReceiptConfig{Sign: k.sign} }

func (h *receiptHarness) payMemo(path, body string) (*http.Response, string, string) {
	h.t.Helper()
	resp, err := http.Post(h.srv.URL+path, "application/json", strings.NewReader(body))
	require.NoError(h.t, err)
	var pr PaymentRequired
	require.NoError(h.t, json.NewDecoder(resp.Body).Decode(&pr))
	resp.Body.Close()
	inv := pr.Accepts[0].Extra.Invoice
	hash := fmt.Sprintf("TX%d", time.Now().UnixNano())
	h.ledger.mu.Lock()
	h.ledger.txs[hash] = &wallet.TransactionDetail{Hash: hash, Memo: inv, Transfers: []wallet.Transfer{{From: buyer(), To: h.payee.address, Amount: "10000uaeth"}}}
	h.ledger.mu.Unlock()
	req, _ := http.NewRequest(http.MethodPost, h.srv.URL+path, strings.NewReader(body))
	req.Header.Set(HeaderPayment, proof(inv, hash))
	resp, err = http.DefaultClient.Do(req)
	require.NoError(h.t, err)
	defer resp.Body.Close()
	got, _ := io.ReadAll(resp.Body)
	return resp, string(got), hash
}

func (h *receiptHarness) expect(path, hash, reqBody, respBody string, status int) ReceiptExpectation {
	return ReceiptExpectation{
		Network: "aether-testnet-1", PayTo: h.payee.address, Payer: buyer(), Scheme: Scheme, Payment: hash, Amount: "10000",
		Method: http.MethodPost, Host: strings.TrimPrefix(h.srv.URL, "http://"), Path: path,
		RequestBody: []byte(reqBody), ResponseBody: []byte(respBody), Status: status,
	}
}

func TestReceipt_CoversTheRequestPaymentAndResponse(t *testing.T) {
	h := newReceiptHarness(t, payeeSigns)
	resp, body, hash := h.payMemo("/forecast", `{"city":"oslo"}`)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "sunny", body)
	r, err := DecodeReceipt(resp.Header.Get(HeaderReceipt))
	require.NoError(t, err)
	require.NoError(t, CheckReceipt(r, h.expect("/forecast", hash, `{"city":"oslo"}`, "sunny", 200)))
	sum := sha256.Sum256([]byte("sunny"))
	require.Equal(t, hex.EncodeToString(sum[:]), r.ResponseHash)
	require.Nil(t, r.Delegation, "signed by the payee itself")

	// Each detail is bound: a changed field, or a different response, fails.
	for name, mutate := range map[string]func(*Receipt){
		"amount": func(x *Receipt) { x.Amount = "1" }, "payer": func(x *Receipt) { x.Payer = seller() },
		"path": func(x *Receipt) { x.Path = "/other" }, "response": func(x *Receipt) { x.ResponseHash = strings.Repeat("0", 64) },
		"status": func(x *Receipt) { x.Status = 500 }, "at": func(x *Receipt) { x.At++ },
	} {
		c := *r
		mutate(&c)
		require.Error(t, c.Verify(), name)
	}
	require.Error(t, CheckReceipt(r, h.expect("/forecast", hash, `{"city":"oslo"}`, "rainy", 200)), "not what was received")
	require.Error(t, CheckReceipt(r, h.expect("/forecast", hash, `{"city":"rome"}`, "sunny", 200)), "not what was asked")
}

func TestReceipt_Prepaid(t *testing.T) {
	h := newReceiptHarness(t, payeeSigns)
	agent := newAgentKey(t)
	h.ledger.pay("DEP1", DepositMemoPrefix+agent.address, h.payee.address, 50_000, h.now, 0)
	body := `{"q":1}`
	host := strings.TrimPrefix(h.srv.URL, "http://")
	header, err := EncodePrepaidPayment(agent.address, RequestFields{
		Network: "aether-testnet-1", PayTo: h.payee.address, Host: host, Method: http.MethodPost, Path: "/q",
		Body: []byte(body), MaxPrice: math.NewInt(10_000), Timestamp: h.now.Unix(), RequestID: "req-1", DepositTx: "DEP1",
	}, agent.sign)
	require.NoError(t, err)
	req, _ := http.NewRequest(http.MethodPost, h.srv.URL+"/q", strings.NewReader(body))
	req.Header.Set(HeaderPayment, header)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	got, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode, string(got))
	r, err := DecodeReceipt(resp.Header.Get(HeaderReceipt))
	require.NoError(t, err)
	require.NoError(t, CheckReceipt(r, ReceiptExpectation{
		Network: "aether-testnet-1", PayTo: h.payee.address, Payer: agent.address, Scheme: SchemePrepaid, Payment: "req-1",
		Amount: "10000", Method: http.MethodPost, Host: host, Path: "/q", RequestBody: []byte(body), ResponseBody: got, Status: 200,
	}))
}

func TestReceipt_Delegated(t *testing.T) {
	receiptKey := newAgentKey(t)
	var delegation *ReceiptDelegation
	h := newReceiptHarness(t, func(payee agentKey) *ReceiptConfig {
		var err error
		delegation, err = NewReceiptDelegation(payee.address, receiptKey.address, time.Now().Add(time.Hour).Unix(), payee.sign)
		require.NoError(t, err)
		return &ReceiptConfig{Sign: receiptKey.sign, Delegation: delegation}
	})
	resp, body, hash := h.payMemo("/forecast", "")
	r, err := DecodeReceipt(resp.Header.Get(HeaderReceipt))
	require.NoError(t, err)
	require.NoError(t, CheckReceipt(r, h.expect("/forecast", hash, "", body, 200)))
	require.NotNil(t, r.Delegation)

	// Past its expiry the delegation no longer covers receipts.
	late := *r
	late.At = delegation.Expires + 1
	require.Error(t, late.Verify())
	// Nor does it cover another key, or come from anyone but the payee.
	other := newAgentKey(t)
	forged, err := NewReceiptDelegation(h.payee.address, receiptKey.address, delegation.Expires, other.sign)
	require.Error(t, err)
	require.Nil(t, forged)
	noDelegation := *r
	noDelegation.Delegation = nil
	require.Error(t, noDelegation.Verify())
}

func TestReceipt_MisconfiguredSignerIsRefusedAtStart(t *testing.T) {
	wrong := newAgentKey(t)
	_, err := New(Config{
		PayTo: seller(), Price: math.NewInt(1), Network: "n", Lookup: (&fakeLedger{}).lookup,
		Receipts: &ReceiptConfig{Sign: wrong.sign},
	})
	require.ErrorContains(t, err, "receipts wouldn't verify")

	payee := newAgentKey(t)
	expired, err := NewReceiptDelegation(payee.address, wrong.address, time.Now().Add(-time.Minute).Unix(), payee.sign)
	require.NoError(t, err)
	_, err = New(Config{
		PayTo: payee.address, Price: math.NewInt(1), Network: "n", Lookup: (&fakeLedger{}).lookup,
		Receipts: &ReceiptConfig{Sign: wrong.sign, Delegation: expired},
	})
	require.ErrorContains(t, err, "expired")
}

func TestReceipt_BigAndFailedResponses(t *testing.T) {
	h := newReceiptHarness(t, payeeSigns)
	h.body = strings.Repeat("x", maxReceiptBody+10)
	resp, body, hash := h.payMemo("/big", "")
	require.Equal(t, len(h.body), len(body), "the response is intact")
	r, err := DecodeReceipt(resp.Header.Get(HeaderReceipt))
	require.NoError(t, err)
	require.Empty(t, r.ResponseHash, "too big to hash: streamed")
	require.NoError(t, CheckReceipt(r, h.expect("/big", hash, "", body, 200)))

	h.body, h.status = "broke", http.StatusBadGateway
	resp, body, hash = h.payMemo("/weather", "")
	require.Equal(t, http.StatusBadGateway, resp.StatusCode)
	r, err = DecodeReceipt(resp.Header.Get(HeaderReceipt))
	require.NoError(t, err)
	require.NoError(t, CheckReceipt(r, h.expect("/weather", hash, "", body, http.StatusBadGateway)), "the receipt says what was served")
}

func TestReceipt_OffByDefault(t *testing.T) {
	h := newHarness(t)
	req := h.invoice("/weather")
	h.ledger.pay("H1", req.Extra.Invoice, seller(), 10_000, h.now, 0)
	resp, _ := h.get("/weather", proof(req.Extra.Invoice, "H1"))
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Empty(t, resp.Header.Get(HeaderReceipt))
}

func TestReceipt_FlushOnlyStreamsEventStreams(t *testing.T) {
	for ct, hashed := range map[string]bool{"application/json": true, "text/event-stream": false} {
		h := newReceiptHarness(t, payeeSigns, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", ct)
			fmt.Fprint(w, "part1 ")
			w.(http.Flusher).Flush() // as httputil.ReverseProxy does after every write without a Content-Length
			fmt.Fprint(w, "part2")
		}))
		resp, body, hash := h.payMemo("/x", "")
		require.Equal(t, "part1 part2", body)
		r, err := DecodeReceipt(resp.Header.Get(HeaderReceipt))
		require.NoError(t, err)
		require.Equal(t, hashed, r.ResponseHash != "", ct)
		require.NoError(t, CheckReceipt(r, h.expect("/x", hash, "", body, 200)))
	}
}

func TestReceipt_LineBreaksCantShiftFields(t *testing.T) {
	h := newReceiptHarness(t, payeeSigns)
	resp, _, _ := h.payMemo("/a%0Ab", "")
	require.Equal(t, http.StatusOK, resp.StatusCode, "served")
	require.Empty(t, resp.Header.Get(HeaderReceipt), "but not given an ambiguous receipt")

	resp, _, _ = h.payMemo("/ok", "")
	r, err := DecodeReceipt(resp.Header.Get(HeaderReceipt))
	require.NoError(t, err)
	// A path ending in the lines after it can't be re-split to claim another status.
	forged := *r
	forged.Path = r.Path + "\n" + r.RequestHash
	require.Error(t, forged.Verify())
	upper := *r
	upper.RequestHash = strings.ToUpper(r.RequestHash)
	require.Error(t, upper.Verify())
}
