package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func doSigned(t *testing.T, f *faucetServer, path, body, from, id string, priv ed25519.PrivateKey, at time.Time) result {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.RemoteAddr = from + ":5555"
	signAgentRequest(req.Header, id, priv, []byte(body), at)
	rec := httptest.NewRecorder()
	f.routes().ServeHTTP(rec, req)
	var r result
	r.status, r.header = rec.Code, rec.Header()
	json.Unmarshal(rec.Body.Bytes(), &r.body)
	json.Unmarshal(rec.Body.Bytes(), &r.raw)
	return r
}

func registerAgent(t *testing.T, f *faucetServer, name string) (string, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	r := do(t, f, http.MethodPost, "/agents",
		`{"name":"`+name+`","public_key":"`+base64.StdEncoding.EncodeToString(pub)+`"}`, "10.0.0.9")
	if r.status != http.StatusCreated {
		t.Fatalf("register: %d %v", r.status, r.raw)
	}
	return r.raw["agent_id"].(string), priv
}

func TestKeylessRequestIsAPIAndNewWalletIsDetected(t *testing.T) {
	f, chain, _ := testServer(t, 10)
	chain.accounts[addr(2)] = true
	for _, a := range []string{addr(1), addr(2)} {
		r := do(t, f, http.MethodPost, "/request", single(a), "10.0.0.1")
		if r.status != http.StatusOK || r.raw["new_wallet"] != (a == addr(1)) {
			t.Fatalf("%s: %d %v", a, r.status, r.raw)
		}
	}
	got := f.ledger.recent(10)
	if len(got) != 2 || got[0].Address != addr(2) || got[0].NewWallet || got[1].Address != addr(1) || !got[1].NewWallet {
		t.Fatalf("drips = %+v", got)
	}
	for _, d := range got {
		if d.Source != sourceAPI || d.Strand != strandAether || d.TxHash == "" || d.AmountUaeth != 1_000_000 {
			t.Fatalf("drip = %+v", d)
		}
	}
}

func TestBrowserProofOfWork(t *testing.T) {
	f, _, clock := testServer(t, 10)
	challenge := func(a string) string {
		r := do(t, f, http.MethodGet, "/challenge?address="+a, "", "10.0.0.1")
		if r.status != http.StatusOK || r.raw["bits"] != float64(8) {
			t.Fatalf("challenge: %d %v", r.status, r.raw)
		}
		return r.raw["challenge"].(string)
	}
	withPow := func(a, c, n string) string {
		return `{"address":"` + a + `","pow":{"challenge":"` + c + `","nonce":"` + n + `"}}`
	}

	c := challenge(addr(1))
	n := solvePow(c, 8)
	if r := do(t, f, http.MethodPost, "/request", withPow(addr(1), c, n), "10.0.0.1"); r.status != http.StatusOK {
		t.Fatalf("solved: %d %v", r.status, r.raw)
	}
	if d := f.ledger.recent(1)[0]; d.Source != sourceWeb {
		t.Fatalf("source = %q, want web", d.Source)
	}

	for name, tc := range map[string]struct{ body string }{
		"reused":        {withPow(addr(1), c, n)},
		"other address": {withPow(addr(2), c, n)},
		"forged":        {withPow(addr(3), c[:len(c)-2]+"AA", n)},
		"no work":       {withPow(addr(4), challenge(addr(4)), "x")},
	} {
		r := do(t, f, http.MethodPost, "/request", tc.body, "10.0.0.1")
		if r.status != http.StatusBadRequest || r.body.Code != codeInvalidPow {
			t.Fatalf("%s: %d %v", name, r.status, r.raw)
		}
	}

	c = challenge(addr(5))
	n = solvePow(c, 8)
	*clock = clock.Add(challengeTTL + time.Second)
	if r := do(t, f, http.MethodPost, "/request", withPow(addr(5), c, n), "10.0.0.1"); r.body.Code != codeInvalidPow ||
		!strings.Contains(r.body.Message, "expired") {
		t.Fatalf("expired: %d %v", r.status, r.raw)
	}
}

func TestAgentKeys(t *testing.T) {
	f, _, clock := testServer(t, 2)
	f.limits.agentLimit = 50
	id, priv := registerAgent(t, f, "relayer-sim")
	if !strings.HasPrefix(id, "ak_") || len(id) != 15 {
		t.Fatalf("agent id %q", id)
	}

	// Registering the same key again answers with the same agent.
	pub := priv.Public().(ed25519.PublicKey)
	r := do(t, f, http.MethodPost, "/agents", `{"name":"other","public_key":"`+base64.StdEncoding.EncodeToString(pub)+`"}`, "10.0.0.9")
	if r.status != http.StatusOK || r.raw["agent_id"] != id || r.raw["name"] != "relayer-sim" {
		t.Fatalf("re-register: %d %v", r.status, r.raw)
	}

	// A signed request is the agent's, on the agent's own quota.
	r = doSigned(t, f, "/request", single(addr(1)), "10.0.0.1", id, priv, *clock)
	if r.status != http.StatusOK || r.header.Get("RateLimit-Limit") != "50" {
		t.Fatalf("signed: %d %v %v", r.status, r.raw, r.header)
	}
	if d := f.ledger.recent(1)[0]; d.Source != sourceAgent || d.AgentID != id || d.AgentName != "relayer-sim" {
		t.Fatalf("drip = %+v", d)
	}
	// Batches too.
	r = doSigned(t, f, "/request/batch", `{"addresses":["`+addr(2)+`","`+addr(3)+`","`+addr(4)+`"]}`, "10.0.0.1", id, priv, *clock)
	if r.status != http.StatusOK || f.ledger.recent(1)[0].Source != sourceAgent {
		t.Fatalf("signed batch: %d %v", r.status, r.raw)
	}

	_, other, _ := ed25519.GenerateKey(rand.Reader)
	for name, tr := range map[string]result{
		"wrong key":   doSigned(t, f, "/request", single(addr(5)), "10.0.0.1", id, other, *clock),
		"unknown key": doSigned(t, f, "/request", single(addr(5)), "10.0.0.1", "ak_000000000000", priv, *clock),
		"stale":       doSigned(t, f, "/request", single(addr(5)), "10.0.0.1", id, priv, clock.Add(-10*time.Minute)),
	} {
		if tr.status != http.StatusUnauthorized || tr.body.Code != codeInvalidAgent {
			t.Fatalf("%s: %d %v", name, tr.status, tr.raw)
		}
	}

	for _, bad := range []string{`{"name":"","public_key":"x"}`, `{"name":"ok","public_key":"AAAA"}`, `{"name":"bad name!","public_key":"` + base64.StdEncoding.EncodeToString(pub) + `"}`} {
		if r := do(t, f, http.MethodPost, "/agents", bad, "10.0.0.8"); r.status != http.StatusBadRequest {
			t.Fatalf("%s: %d", bad, r.status)
		}
	}
	// At most five new registrations per caller per hour.
	for i := 0; i < 4; i++ {
		registerAgent(t, f, "bot")
	}
	pub2, _, _ := ed25519.GenerateKey(rand.Reader)
	if r := do(t, f, http.MethodPost, "/agents", `{"name":"bot","public_key":"`+base64.StdEncoding.EncodeToString(pub2)+`"}`, "10.0.0.9"); r.status != http.StatusTooManyRequests {
		t.Fatalf("sixth registration: %d", r.status)
	}
}

func TestIBCStrandRefusedUntilChannel(t *testing.T) {
	f, chain, _ := testServer(t, 10)
	r := do(t, f, http.MethodPost, "/request", `{"address":"`+addr(1)+`","strand":"ibc"}`, "10.0.0.1")
	if r.status != http.StatusBadRequest || r.body.Code != codeIBCUnavailable || len(chain.sends) != 0 {
		t.Fatalf("%d %v", r.status, r.raw)
	}
}

func TestStatsAndDrips(t *testing.T) {
	f, chain, clock := testServer(t, 0)
	f.limits.agentLimit = 50
	id, priv := registerAgent(t, f, "wallet-gen-ci")
	chain.accounts[addr(9)] = true

	*clock = clock.Add(-3 * 24 * time.Hour) // three days ago
	do(t, f, http.MethodPost, "/request", single(addr(1)), "10.0.0.1")
	*clock = clock.Add(3 * 24 * time.Hour)
	doSigned(t, f, "/request", single(addr(2)), "10.0.0.1", id, priv, *clock)
	doSigned(t, f, "/request", single(addr(3)), "10.0.0.1", id, priv, *clock)
	do(t, f, http.MethodPost, "/request", single(addr(9)), "10.0.0.2")

	r := do(t, f, http.MethodGet, "/stats", "", "10.0.0.1")
	var s statsBody
	bz, _ := json.Marshal(r.raw)
	json.Unmarshal(bz, &s)
	if s.Drips != 4 || s.UniqueWallets != 4 || s.NewWallets != 3 || s.NewWalletsAgent != 2 || s.SentUaeth != 4_000_000 {
		t.Fatalf("stats = %+v", s)
	}
	if s.Sources30d != (sourceCounts{Agent: 2, API: 2}) || s.IBCEnabled || s.BalanceUaeth == nil || *s.BalanceUaeth != 5_000_000_000 {
		t.Fatalf("stats = %+v", s)
	}
	days := s.NewWalletsDaily
	if len(days) != statsDays || days[statsDays-1].Agent != 2 || days[statsDays-4].API != 1 {
		t.Fatalf("daily = %+v", days)
	}
	if len(s.TopAgents30d) != 1 || s.TopAgents30d[0] != (agentCount{AgentID: id, Name: "wallet-gen-ci", NewWallets: 2, Drips: 2}) {
		t.Fatalf("top agents = %+v", s.TopAgents30d)
	}

	r = do(t, f, http.MethodGet, "/drips?limit=2", "", "10.0.0.1")
	list, _ := r.raw["drips"].([]any)
	if len(list) != 2 || list[0].(map[string]any)["address"] != addr(9) {
		t.Fatalf("drips = %v", r.raw)
	}
}

func TestLedgerAndRegistrySurviveRestart(t *testing.T) {
	dir := t.TempDir()
	l, err := openLedger(filepath.Join(dir, "drips.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	d := drip{Time: time.Unix(1_700_000_000, 0).UTC(), TxHash: "TX1", Address: addr(1), AmountUaeth: 5, Strand: strandAether, Source: sourceWeb, NewWallet: true}
	if err := l.add([]drip{d}); err != nil {
		t.Fatal(err)
	}
	l2, err := openLedger(filepath.Join(dir, "drips.jsonl"))
	if err != nil || len(l2.recent(5)) != 1 || l2.recent(1)[0] != d {
		t.Fatalf("reloaded %v %+v", err, l2.recent(5))
	}

	reg, _ := openAgentRegistry(filepath.Join(dir, "agents.json"))
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	k, created, err := reg.register("bot", base64.StdEncoding.EncodeToString(pub))
	if err != nil || !created {
		t.Fatal(err)
	}
	reg2, err := openAgentRegistry(filepath.Join(dir, "agents.json"))
	if err != nil || reg2.keys[k.ID].Name != "bot" {
		t.Fatalf("reloaded %v %+v", err, reg2.keys)
	}
}
