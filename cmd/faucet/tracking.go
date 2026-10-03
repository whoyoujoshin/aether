package main

import (
	"encoding/json"
	"net/http"
	"strconv"
	"sync"
	"time"
)

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// GET /challenge?address=aether1... -- a proof of work for one request
// from a browser (pow.go).
func (f *faucetServer) handleChallenge(w http.ResponseWriter, r *http.Request) {
	address := r.URL.Query().Get("address")
	if !bech32Pattern.MatchString(address) {
		writeJSON(w, http.StatusBadRequest, responseBody{Code: codeInvalidAddress, Message: "pass ?address=aether1..."})
		return
	}
	challenge, expires := f.pow.issue(address)
	writeJSON(w, http.StatusOK, map[string]any{
		"challenge":  challenge,
		"bits":       f.pow.bits,
		"expires_at": expires.UTC(),
		"solve":      `find a decimal nonce where sha256(challenge + ":" + nonce) starts with "bits" zero bits, then POST /request {"address", "pow":{"challenge","nonce"}}`,
	})
}

// POST /agents {"name":"my-bot","public_key":"<base64 ed25519>"} --
// register an agent key (agents.go). 201 for a new key, 200 with the
// existing registration for a known one.
func (f *faucetServer) handleAgents(w http.ResponseWriter, r *http.Request) {
	caller := clientIP(r, f.trusted)
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, responseBody{Code: codeInvalidRequest, Message: "use POST"})
		return
	}
	var req struct {
		Name      string `json:"name"`
		PublicKey string `json:"public_key"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, responseBody{Code: codeInvalidRequest, Message: "invalid request body"})
		return
	}
	res, _, q, ok := f.regLimits.reserve(caller, []string{caller + "|" + req.PublicKey})
	if !ok {
		setQuotaHeaders(w.Header(), q, f.regLimits.callerWindow)
		w.Header().Set("Retry-After", strconv.Itoa(ceilSeconds(q.reset)))
		writeJSON(w, http.StatusTooManyRequests, responseBody{Code: codeCallerLimit,
			Message: "too many agent registrations from this caller; try later", RetryAfterSeconds: ceilSeconds(q.reset)})
		return
	}
	k, created, err := f.agents.register(req.Name, req.PublicKey)
	if err != nil {
		f.regLimits.release(res)
		writeJSON(w, http.StatusBadRequest, responseBody{Code: codeInvalidRequest, Message: err.Error()})
		return
	}
	if !created {
		f.regLimits.release(res)
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	writeJSON(w, status, map[string]any{
		"agent_id":   k.ID,
		"name":       k.Name,
		"registered": k.Registered,
		"sign":       `send ` + headerAgent + `: <agent_id>, ` + headerAgentTimestamp + `: <unix seconds>, ` + headerAgentSignature + `: base64(ed25519(timestamp + "\n" + body))`,
		"limit":      f.limits.agentLimit,
	})
}

type statsBody struct {
	ledgerStats
	AmountUaeth  int64  `json:"amount_uaeth"`
	CooldownSecs int    `json:"address_cooldown_secs"`
	IBCEnabled   bool   `json:"ibc_enabled"`
	PowBits      int    `json:"pow_bits"`
	BalanceUaeth *int64 `json:"balance_uaeth,omitempty"`
}

// GET /stats -- what the faucet has given, and to whom.
func (f *faucetServer) handleStats(w http.ResponseWriter, r *http.Request) {
	body := statsBody{
		ledgerStats:  f.ledger.stats(f.now()),
		AmountUaeth:  f.amountUaeth,
		CooldownSecs: ceilSeconds(f.limits.cooldown),
		PowBits:      f.pow.bits,
	}
	if b, err := f.balance(); err == nil {
		body.BalanceUaeth = &b
	}
	writeJSON(w, http.StatusOK, body)
}

// GET /drips?limit=20 -- the newest drips, newest first (at most 100).
func (f *faucetServer) handleDrips(w http.ResponseWriter, r *http.Request) {
	n, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil || n <= 0 {
		n = 20
	}
	writeJSON(w, http.StatusOK, map[string]any{"drips": f.ledger.recent(min(n, 100))})
}

// cachedBalance asks at most every ttl, so /stats can be polled.
func cachedBalance(ttl time.Duration, fetch func() (int64, error)) func() (int64, error) {
	var mu sync.Mutex
	var at time.Time
	var last int64
	var lastErr error
	return func() (int64, error) {
		mu.Lock()
		defer mu.Unlock()
		if time.Since(at) > ttl {
			last, lastErr = fetch()
			at = time.Now()
		}
		return last, lastErr
	}
}
