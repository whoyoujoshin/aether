package main

import (
	"net/http"
	"testing"
)

// The faucet's side of the agent-facing contract (docs/API-STABILITY.md):
// the paths, response keys, error codes and headers below only ever gain
// things. A failure here means a change removed or renamed something bots
// branch on. Add the new name beside the old one instead.

func requireKeys(t *testing.T, what string, got map[string]any, want ...string) {
	t.Helper()
	for _, k := range want {
		if _, ok := got[k]; !ok {
			t.Errorf("%s lost the stable key %q (docs/API-STABILITY.md): %v", what, k, got)
		}
	}
}

func TestContract_ErrorCodesNeverChange(t *testing.T) {
	// Bots switch on these strings: the values are the contract, not the
	// Go names.
	for got, want := range map[string]string{
		codeSent:            "sent",
		codePending:         "pending",
		codeInvalidRequest:  "invalid_request",
		codeInvalidAddress:  "invalid_address",
		codeBatchTooLarge:   "batch_too_large",
		codeAddressCooldown: "address_cooldown",
		codeCallerLimit:     "caller_limit",
		codeSendFailed:      "send_failed",
		codeInvalidPow:      "invalid_pow",
		codeInvalidAgent:    "invalid_agent",
		codeIBCUnavailable:  "ibc_unavailable",
	} {
		if got != want {
			t.Errorf("error code %q became %q", want, got)
		}
	}
}

func TestContract_RequestAndStatus(t *testing.T) {
	f, _, _ := testServer(t, 10)

	// A drip: the keys a bot reads, and the quota headers.
	r := do(t, f, http.MethodPost, "/request", single(addr(1)), "10.0.0.1")
	if r.status != http.StatusOK {
		t.Fatalf("request: %d %v", r.status, r.raw)
	}
	requireKeys(t, "POST /request", r.raw, "success", "code", "message", "tx_hash", "new_wallet")
	for _, h := range []string{"RateLimit-Limit", "RateLimit-Remaining", "RateLimit-Reset", "RateLimit-Policy"} {
		if r.header.Get(h) == "" {
			t.Errorf("POST /request lost the %s header", h)
		}
	}

	// The same address again: refused with a wait, in the body and the header.
	r = do(t, f, http.MethodPost, "/request", single(addr(1)), "10.0.0.1")
	if r.status != http.StatusTooManyRequests || r.body.Code != codeAddressCooldown {
		t.Fatalf("cooldown: %d %v", r.status, r.raw)
	}
	requireKeys(t, "a refusal", r.raw, "success", "code", "message", "retry_after_seconds")
	if r.header.Get("Retry-After") == "" {
		t.Error("a refusal lost the Retry-After header")
	}

	// A batch: addr(1) is still cooling down, so it's skipped, not sent.
	r = do(t, f, http.MethodPost, "/request/batch", batch(addr(1), addr(2), addr(3)), "10.0.0.2")
	if r.status != http.StatusOK {
		t.Fatalf("batch: %d %v", r.status, r.raw)
	}
	requireKeys(t, "POST /request/batch", r.raw, "success", "code", "tx_hash", "sent", "skipped")
	r = do(t, f, http.MethodPost, "/request/batch", batch(addr(5), "nope"), "10.0.0.2")
	if r.status != http.StatusBadRequest || r.body.Code != codeInvalidAddress {
		t.Fatalf("invalid batch: %d %v", r.status, r.raw)
	}
	requireKeys(t, "a refused batch", r.raw, "success", "code", "message", "invalid")

	r = do(t, f, http.MethodGet, "/status?address="+addr(4), "", "10.0.0.3")
	if r.status != http.StatusOK {
		t.Fatalf("status: %d %v", r.status, r.raw)
	}
	requireKeys(t, "GET /status", r.raw, "address", "eligible", "retry_after_seconds", "amount_uaeth", "caller_remaining")
}

func TestContract_InfoAndStats(t *testing.T) {
	f, _, _ := testServer(t, 10)
	do(t, f, http.MethodPost, "/request", single(addr(1)), "10.0.0.1")

	r := do(t, f, http.MethodGet, "/", "", "10.0.0.1")
	requireKeys(t, "GET /", r.raw, "chain_id", "denom", "amount_uaeth", "address_cooldown_secs",
		"caller_limit", "caller_window_secs", "batch_max", "endpoints")
	if eps, _ := r.raw["endpoints"].(map[string]any); eps != nil {
		requireKeys(t, "GET / endpoints", eps, "request", "batch", "status", "stats")
	} else {
		t.Error("GET / lost its endpoints object")
	}

	r = do(t, f, http.MethodGet, "/stats", "", "10.0.0.1")
	requireKeys(t, "GET /stats", r.raw, "drips", "sent_uaeth", "unique_wallets", "new_wallets",
		"new_wallets_by_agents", "since", "sources_30d", "new_wallets_daily", "top_agents_30d",
		"amount_uaeth", "address_cooldown_secs", "ibc_enabled", "pow_bits", "balance_uaeth")
}
