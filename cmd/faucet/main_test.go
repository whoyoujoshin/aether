package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/whoyoujoshin/aether/wallet"
)

func addr(i int) string { return fmt.Sprintf("aether1%038d", i) }

type fakeChain struct {
	mu       sync.Mutex
	sends    [][]string
	sendErr  error
	code     uint32 // DeliverTx result
	unknown  bool   // confirm times out
	nextHash int
	accounts map[string]bool // addresses the chain already has an account for
}

func testServer(t *testing.T, callerLimit int) (*faucetServer, *fakeChain, *time.Time) {
	t.Helper()
	clock := time.Unix(1_700_000_000, 0)
	l := newLimiter(time.Hour, callerLimit, time.Hour)
	l.now = func() time.Time { return clock }
	chain := &fakeChain{accounts: map[string]bool{}}
	drips, _ := openLedger("")
	agents, _ := openAgentRegistry("")
	agents.now = func() time.Time { return clock }
	pow := newPowIssuer(8)
	pow.now = func() time.Time { return clock }
	f := &faucetServer{
		ledger:    drips,
		pow:       pow,
		agents:    agents,
		regLimits: newLimiter(0, 5, time.Hour),
		now:       func() time.Time { return clock },
		accountExists: func(a string) (bool, error) {
			chain.mu.Lock()
			defer chain.mu.Unlock()
			return chain.accounts[a], nil
		},
		balance:     func() (int64, error) { return 5_000_000_000, nil },
		limits:      l,
		trusted:     parseTrusted("127.0.0.1"),
		batchMax:    5,
		chainID:     "aether-test-1",
		amountUaeth: 1_000_000,
		send: func(addresses []string) (string, error) {
			chain.mu.Lock()
			defer chain.mu.Unlock()
			if chain.sendErr != nil {
				return "", chain.sendErr
			}
			chain.sends = append(chain.sends, addresses)
			chain.nextHash++
			return fmt.Sprintf("TX%d", chain.nextHash), nil
		},
		confirm: func(h string) (*wallet.TransactionDetail, error) {
			if chain.unknown {
				return nil, errors.New("not found")
			}
			return &wallet.TransactionDetail{Hash: h, Height: 7, Code: chain.code}, nil
		},
	}
	return f, chain, &clock
}

type result struct {
	status int
	header http.Header
	body   responseBody
	raw    map[string]any
}

func do(t *testing.T, f *faucetServer, method, path, body, from string) result {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.RemoteAddr = from + ":5555"
	rec := httptest.NewRecorder()
	f.routes().ServeHTTP(rec, req)
	var r result
	r.status, r.header = rec.Code, rec.Header()
	json.Unmarshal(rec.Body.Bytes(), &r.body)
	json.Unmarshal(rec.Body.Bytes(), &r.raw)
	return r
}

func single(a string) string { return fmt.Sprintf(`{"address":%q}`, a) }

func batch(as ...string) string {
	bz, _ := json.Marshal(map[string][]string{"addresses": as})
	return string(bz)
}

func TestSingleRequestCooldownAndHeaders(t *testing.T) {
	f, chain, clock := testServer(t, 20)
	r := do(t, f, "POST", "/request", single(addr(1)), "203.0.113.9")
	if r.status != 200 || !r.body.Success || r.body.Code != codeSent || r.body.TxHash != "TX1" {
		t.Fatalf("first request: %d %+v", r.status, r.body)
	}
	if r.header.Get("RateLimit-Limit") != "20" || r.header.Get("RateLimit-Remaining") != "19" ||
		r.header.Get("RateLimit-Reset") != "3600" || r.header.Get("RateLimit-Policy") != "20;w=3600" {
		t.Fatalf("headers: %v", r.header)
	}
	if r.body.Sent != nil {
		t.Fatal("single answer lists sent")
	}

	*clock = clock.Add(20 * time.Minute)
	r = do(t, f, "POST", "/request", single(addr(1)), "198.51.100.1") // someone else, same address
	if r.status != 429 || r.body.Code != codeAddressCooldown || r.body.RetryAfterSeconds != 2400 || r.header.Get("Retry-After") != "2400" {
		t.Fatalf("cooldown: %d %+v %v", r.status, r.body, r.header)
	}
	if !strings.Contains(r.body.Message, "please wait 40m0s") {
		t.Fatalf("message: %q", r.body.Message)
	}

	*clock = clock.Add(41 * time.Minute)
	if r = do(t, f, "POST", "/request", single(addr(1)), "198.51.100.1"); r.status != 200 {
		t.Fatalf("after cooldown: %d %+v", r.status, r.body)
	}
	if len(chain.sends) != 2 {
		t.Fatalf("sends = %v", chain.sends)
	}
}

func TestCallerLimit(t *testing.T) {
	f, chain, clock := testServer(t, 3)
	for i := 0; i < 3; i++ {
		if r := do(t, f, "POST", "/request", single(addr(i)), "203.0.113.9"); r.status != 200 {
			t.Fatalf("request %d: %d %+v", i, r.status, r.body)
		}
	}
	*clock = clock.Add(15 * time.Minute)
	r := do(t, f, "POST", "/request", single(addr(9)), "203.0.113.9")
	if r.status != 429 || r.body.Code != codeCallerLimit || r.header.Get("Retry-After") != "2700" || r.header.Get("RateLimit-Remaining") != "0" {
		t.Fatalf("over limit: %d %+v %v", r.status, r.body, r.header)
	}
	// Another caller isn't affected, and the refused address wasn't burned.
	if r := do(t, f, "POST", "/request", single(addr(9)), "198.51.100.1"); r.status != 200 {
		t.Fatalf("other caller: %d %+v", r.status, r.body)
	}
	*clock = clock.Add(46 * time.Minute)
	if r := do(t, f, "POST", "/request", single(addr(10)), "203.0.113.9"); r.status != 200 || r.header.Get("RateLimit-Remaining") != "2" {
		t.Fatalf("new window: %d %+v %v", r.status, r.body, r.header)
	}
	if len(chain.sends) != 5 {
		t.Fatalf("sends = %v", chain.sends)
	}
}

func TestBatchOneTransactionSkipsCoolingAddresses(t *testing.T) {
	f, chain, _ := testServer(t, 20)
	do(t, f, "POST", "/request", single(addr(2)), "203.0.113.9")

	r := do(t, f, "POST", "/request/batch", batch(addr(1), addr(2), addr(3), addr(1)), "203.0.113.9")
	if r.status != 200 || r.body.Code != codeSent || strings.Join(r.body.Sent, ",") != addr(1)+","+addr(3) {
		t.Fatalf("batch: %d %+v", r.status, r.body)
	}
	if len(r.body.Skipped) != 1 || r.body.Skipped[0].Address != addr(2) || r.body.Skipped[0].Code != codeAddressCooldown || r.body.Skipped[0].RetryAfterSeconds != 3600 {
		t.Fatalf("skipped: %+v", r.body.Skipped)
	}
	if len(chain.sends) != 2 || len(chain.sends[1]) != 2 {
		t.Fatalf("want one transaction for both: %v", chain.sends)
	}
	if r.header.Get("RateLimit-Remaining") != "17" {
		t.Fatalf("remaining = %s", r.header.Get("RateLimit-Remaining"))
	}

	// Everything cooling down: 429, nothing sent.
	r = do(t, f, "POST", "/request/batch", batch(addr(1), addr(3)), "203.0.113.9")
	if r.status != 429 || r.body.Code != codeAddressCooldown || len(r.body.Skipped) != 2 || r.header.Get("Retry-After") != "3600" {
		t.Fatalf("all cooling: %d %+v", r.status, r.body)
	}
}

func TestBatchValidationAndQuotaIsAllOrNothing(t *testing.T) {
	f, chain, _ := testServer(t, 4)
	if r := do(t, f, "POST", "/request/batch", batch(addr(1), "cosmos1nope"), "203.0.113.9"); r.status != 400 || r.body.Code != codeInvalidAddress || len(r.body.Invalid) != 1 {
		t.Fatalf("invalid: %d %+v", r.status, r.body)
	}
	if r := do(t, f, "POST", "/request/batch", batch(addr(1), addr(2), addr(3), addr(4), addr(5), addr(6)), "203.0.113.9"); r.status != 400 || r.body.Code != codeBatchTooLarge {
		t.Fatalf("too large: %d %+v", r.status, r.body)
	}
	if r := do(t, f, "POST", "/request/batch", `{"addresses":[]}`, "203.0.113.9"); r.status != 400 || r.body.Code != codeInvalidRequest {
		t.Fatalf("empty: %d %+v", r.status, r.body)
	}
	do(t, f, "POST", "/request/batch", batch(addr(1), addr(2)), "203.0.113.9")
	r := do(t, f, "POST", "/request/batch", batch(addr(3), addr(4), addr(5)), "203.0.113.9")
	if r.status != 429 || r.body.Code != codeCallerLimit || !strings.Contains(r.body.Message, "2 more") {
		t.Fatalf("over quota: %d %+v", r.status, r.body)
	}
	// Nothing was reserved: two of them still go through.
	if r := do(t, f, "POST", "/request/batch", batch(addr(3), addr(4)), "203.0.113.9"); r.status != 200 {
		t.Fatalf("within quota: %d %+v", r.status, r.body)
	}
	if len(chain.sends) != 2 {
		t.Fatalf("sends = %v", chain.sends)
	}
}

func TestFailedSendReleasesCooldownAndQuota(t *testing.T) {
	f, chain, _ := testServer(t, 2)
	chain.sendErr = errors.New("node down")
	r := do(t, f, "POST", "/request/batch", batch(addr(1), addr(2)), "203.0.113.9")
	if r.status != 500 || r.body.Code != codeSendFailed || r.header.Get("RateLimit-Remaining") != "2" {
		t.Fatalf("send error: %d %+v %v", r.status, r.body, r.header)
	}
	chain.sendErr = nil
	chain.code = 5 // lands, but fails in the block
	if r = do(t, f, "POST", "/request/batch", batch(addr(1), addr(2)), "203.0.113.9"); r.status != 500 || r.body.TxHash == "" {
		t.Fatalf("on-chain failure: %d %+v", r.status, r.body)
	}
	chain.code = 0
	if r = do(t, f, "POST", "/request/batch", batch(addr(1), addr(2)), "203.0.113.9"); r.status != 200 {
		t.Fatalf("after failures: %d %+v", r.status, r.body)
	}

	// Unconfirmed: 202, and the reservation stands (it may still land).
	chain.unknown = true
	r = do(t, f, "POST", "/request", single(addr(3)), "198.51.100.1")
	if r.status != 202 || r.body.Code != codePending || r.body.TxHash == "" {
		t.Fatalf("pending: %d %+v", r.status, r.body)
	}
	if r = do(t, f, "POST", "/request", single(addr(3)), "198.51.100.1"); r.status != 429 {
		t.Fatalf("pending address re-requested: %d %+v", r.status, r.body)
	}
}

func TestStatusAndInfo(t *testing.T) {
	f, _, clock := testServer(t, 1)
	r := do(t, f, "GET", "/status?address="+addr(1), "", "203.0.113.9")
	if r.status != 200 || r.raw["eligible"] != true || r.raw["retry_after_seconds"] != float64(0) || r.raw["caller_remaining"] != float64(1) {
		t.Fatalf("status before: %d %v", r.status, r.raw)
	}
	do(t, f, "POST", "/request", single(addr(1)), "203.0.113.9")
	*clock = clock.Add(10 * time.Minute)
	r = do(t, f, "GET", "/status?address="+addr(1), "", "203.0.113.9")
	if r.raw["eligible"] != false || r.raw["retry_after_seconds"] != float64(3000) {
		t.Fatalf("status after: %v", r.raw)
	}
	// A fresh address, but this caller's quota is spent until the window resets.
	r = do(t, f, "GET", "/status?address="+addr(2), "", "203.0.113.9")
	if r.raw["eligible"] != false || r.raw["retry_after_seconds"] != float64(3000) || r.raw["caller_remaining"] != float64(0) {
		t.Fatalf("status, quota spent: %v", r.raw)
	}
	if r = do(t, f, "GET", "/status?address=nope", "", "203.0.113.9"); r.status != 400 {
		t.Fatalf("bad address: %d", r.status)
	}

	r = do(t, f, "GET", "/", "", "203.0.113.9")
	if r.status != 200 || r.raw["batch_max"] != float64(5) || r.raw["caller_limit"] != float64(1) || r.raw["address_cooldown_secs"] != float64(3600) {
		t.Fatalf("info: %d %v", r.status, r.raw)
	}
	if r = do(t, f, "GET", "/nope", "", "203.0.113.9"); r.status != 404 {
		t.Fatalf("unknown path: %d", r.status)
	}
	if r = do(t, f, "GET", "/request", "", "203.0.113.9"); r.status != 405 || r.body.Code != codeInvalidRequest {
		t.Fatalf("GET /request: %d", r.status)
	}
}

func TestClientIPTrustsOnlyConfiguredProxies(t *testing.T) {
	trusted := parseTrusted("127.0.0.1, 10.0.0.2")
	for _, tc := range []struct{ remote, xff, want string }{
		{"203.0.113.9:1", "", "203.0.113.9"},
		{"203.0.113.9:1", "1.2.3.4", "203.0.113.9"},              // not a proxy: header ignored
		{"127.0.0.1:1", "198.51.100.7", "198.51.100.7"},          // Caddy on the same host
		{"127.0.0.1:1", "6.6.6.6, 198.51.100.7", "198.51.100.7"}, // a spoofed first hop is ignored
		{"127.0.0.1:1", "198.51.100.7, 10.0.0.2", "198.51.100.7"},
		{"127.0.0.1:1", "", "127.0.0.1"},
		{"127.0.0.1:1", "garbage", "127.0.0.1"},
		{"[::1]:1", "198.51.100.7", "::1"}, // ::1 not configured here
	} {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = tc.remote
		if tc.xff != "" {
			r.Header.Set("X-Forwarded-For", tc.xff)
		}
		if got := clientIP(r, trusted); got != tc.want {
			t.Errorf("%s + %q = %s, want %s", tc.remote, tc.xff, got, tc.want)
		}
	}
}

func TestConcurrentRequestsForOneAddressSendOnce(t *testing.T) {
	f, chain, _ := testServer(t, 0)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			do(t, f, "POST", "/request", single(addr(1)), fmt.Sprintf("203.0.113.%d", i))
		}(i)
	}
	wg.Wait()
	if len(chain.sends) != 1 {
		t.Fatalf("sends = %d", len(chain.sends))
	}
}
