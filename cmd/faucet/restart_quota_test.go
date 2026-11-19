package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"path/filepath"
	"testing"
	"time"
)

// A restart rebuilds the limits from the drip log: the address is still
// cooling down and the caller's quota is still spent.
func TestRestartKeepsCooldownsAndQuotas(t *testing.T) {
	f, _, clock := testServer(t, 2)
	path := filepath.Join(t.TempDir(), "drips.jsonl")
	f.ledger, _ = openLedger(path)

	if r := do(t, f, http.MethodPost, "/request", single(addr(1)), "203.0.113.9"); r.status != http.StatusOK {
		t.Fatalf("first drip: %d %+v", r.status, r.body)
	}
	*clock = clock.Add(5 * time.Minute)

	// Restart: a new limiter, and the drip log read back from disk.
	ledger, err := openLedger(path)
	if err != nil {
		t.Fatal(err)
	}
	l := newLimiter(time.Hour, 2, time.Hour)
	l.now = func() time.Time { return *clock }
	l.restore(ledger.all())
	f.limits, f.ledger = l, ledger

	if r := do(t, f, http.MethodPost, "/request", single(addr(1)), "198.51.100.1"); r.status != http.StatusTooManyRequests || r.body.Code != codeAddressCooldown {
		t.Fatalf("same address after a restart: %d %+v", r.status, r.body)
	}
	if r := do(t, f, http.MethodPost, "/request", single(addr(2)), "203.0.113.9"); r.status != http.StatusOK {
		t.Fatalf("second of two for the caller: %d %+v", r.status, r.body)
	}
	if r := do(t, f, http.MethodPost, "/request", single(addr(3)), "203.0.113.9"); r.status != http.StatusTooManyRequests || r.body.Code != codeCallerLimit {
		t.Fatalf("third for a caller limited to two: %d %+v", r.status, r.body)
	}

	// An hour on, both have expired.
	*clock = clock.Add(time.Hour)
	l2 := newLimiter(time.Hour, 2, time.Hour)
	l2.now = func() time.Time { return *clock }
	l2.restore(ledger.all())
	f.limits = l2
	if r := do(t, f, http.MethodPost, "/request", single(addr(1)), "203.0.113.9"); r.status != http.StatusOK {
		t.Fatalf("after the cooldown: %d %+v", r.status, r.body)
	}
}

// The drip log keeps who asked; /drips never shows it.
func TestDripsDontShowTheCaller(t *testing.T) {
	f, _, _ := testServer(t, 10)
	do(t, f, http.MethodPost, "/request", single(addr(1)), "203.0.113.9")
	if got := f.ledger.all(); len(got) != 1 || got[0].Caller != "203.0.113.9" {
		t.Fatalf("logged %+v", got)
	}
	r := do(t, f, http.MethodGet, "/drips?n=5", "", "198.51.100.1")
	ds, _ := r.raw["drips"].([]any)
	if len(ds) != 1 {
		t.Fatalf("/drips = %v", r.raw)
	}
	if _, ok := ds[0].(map[string]any)["caller"]; ok {
		t.Fatalf("/drips shows the caller: %v", ds[0])
	}
}

// Keys from one caller share one agent quota: registering more gets no
// more drips. A key registered before callers were recorded keeps its own.
func TestAgentKeysShareTheirRegistrantsQuota(t *testing.T) {
	f, _, clock := testServer(t, 1)
	f.limits.agentLimit = 2
	id1, priv1 := registerAgent(t, f, "bot-one")
	id2, priv2 := registerAgent(t, f, "bot-two")

	for i, k := range []struct {
		id   string
		priv ed25519.PrivateKey
	}{{id1, priv1}, {id2, priv2}} {
		if r := doSigned(t, f, "/request", single(addr(10+i)), "198.51.100.7", k.id, k.priv, *clock); r.status != http.StatusOK {
			t.Fatalf("drip %d: %d %+v", i, r.status, r.body)
		}
	}
	if r := doSigned(t, f, "/request", single(addr(20)), "198.51.100.7", id2, priv2, *clock); r.status != http.StatusTooManyRequests || r.body.Code != codeCallerLimit {
		t.Fatalf("a third drip through a second key: %d %+v", r.status, r.body)
	}

	// A legacy key (no registrant recorded) has a quota of its own.
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	legacy := agentKey{ID: agentID(pub), Name: "old", PublicKey: base64.StdEncoding.EncodeToString(pub), Registered: *clock}
	f.agents.keys[legacy.ID] = legacy
	if r := doSigned(t, f, "/request", single(addr(30)), "198.51.100.7", legacy.ID, priv, *clock); r.status != http.StatusOK {
		t.Fatalf("legacy key: %d %+v", r.status, r.body)
	}
}

func TestAgentKeysPerCallerAreCapped(t *testing.T) {
	f, _, _ := testServer(t, 1)
	f.regLimits = newLimiter(0, 100, time.Hour) // the hourly limit isn't what's tested here
	for i := 0; i < maxAgentKeysPerCaller; i++ {
		registerAgent(t, f, "bot")
	}
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	r := do(t, f, http.MethodPost, "/agents", `{"name":"one-more","public_key":"`+base64.StdEncoding.EncodeToString(pub)+`"}`, "10.0.0.9")
	if r.status != http.StatusTooManyRequests || r.body.Code != codeCallerLimit {
		t.Fatalf("key %d from one caller: %d %v", maxAgentKeysPerCaller+1, r.status, r.raw)
	}
	if r := do(t, f, http.MethodPost, "/agents", `{"name":"elsewhere","public_key":"`+base64.StdEncoding.EncodeToString(pub)+`"}`, "10.0.0.10"); r.status != http.StatusCreated {
		t.Fatalf("another caller: %d %v", r.status, r.raw)
	}
}
