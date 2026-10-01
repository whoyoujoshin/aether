package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	pow "github.com/whoyoujoshin/aether/x/pow"
)

// rpcRequest is a bitcoind-style JSON-RPC request. Params are strings for
// every method this bridge serves.
type rpcRequest struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params []any           `json:"params"`
}

type rpcResponse struct {
	Result any             `json:"result"`
	Error  *rpcError       `json:"error"`
	ID     json.RawMessage `json:"id"`
}

const maxRequestBytes = 1 << 20 // a proof is a few kilobytes

// handler serves JSON-RPC at POST / behind HTTP basic auth, as litecoind
// and namecoind do, plus unauthenticated GET /health and /metrics for
// monitoring on the pool's private network.
func (b *bridge) handler(user, password string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", b.health)
	mux.HandleFunc("/metrics", b.metrics)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "JSON-RPC is POST /", http.StatusMethodNotAllowed)
			return
		}
		u, p, ok := r.BasicAuth()
		// Constant time over the contents; only a length mismatch returns early.
		if !ok || subtle.ConstantTimeCompare([]byte(u), []byte(user))&subtle.ConstantTimeCompare([]byte(p), []byte(password)) != 1 {
			w.Header().Set("WWW-Authenticate", `Basic realm="auxpowd"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, maxRequestBytes+1))
		if err != nil || len(body) > maxRequestBytes {
			http.Error(w, "request too large", http.StatusRequestEntityTooLarge)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()

		// A batch (array) gets an array back, always HTTP 200, as bitcoind does.
		var batch []rpcRequest
		if json.Unmarshal(body, &batch) == nil {
			out := make([]rpcResponse, len(batch))
			for i, req := range batch {
				out[i], _ = b.serve(ctx, req)
			}
			writeJSON(w, http.StatusOK, out)
			return
		}
		var req rpcRequest
		if err := json.Unmarshal(body, &req); err != nil {
			writeJSON(w, http.StatusInternalServerError, rpcResponse{Error: &rpcError{-32700, "parse error"}})
			return
		}
		resp, status := b.serve(ctx, req)
		writeJSON(w, status, resp)
	})
	return mux
}

// serve runs one request and picks the HTTP status bitcoind would: 200,
// 404 for an unknown method, 500 for any other error.
func (b *bridge) serve(ctx context.Context, req rpcRequest) (rpcResponse, int) {
	resp := rpcResponse{ID: req.ID}
	params := make([]string, len(req.Params))
	for i, p := range req.Params {
		s, ok := p.(string)
		if !ok {
			resp.Error = &rpcError{rpcInvalidParams, fmt.Sprintf("argument %d must be a string", i+1)}
			return resp, http.StatusInternalServerError
		}
		params[i] = s
	}
	result, err := b.call(ctx, req.Method, params)
	var re *rpcError
	switch {
	case err == nil:
		resp.Result = result
		return resp, http.StatusOK
	case errors.Is(err, errNoMethod):
		resp.Error = &rpcError{-32601, "Method not found"}
		return resp, http.StatusNotFound
	case errors.As(err, &re):
		resp.Error = re
	default:
		resp.Error = &rpcError{rpcMiscError, err.Error()}
	}
	return resp, http.StatusInternalServerError
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// health answers 200 when the node is reachable and merged mining is
// active, 503 otherwise, with the reason.
func (b *bridge) health(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	out := map[string]any{"activation_height": pow.MergedMiningActivationHeight}
	st, err := b.chain.State(ctx)
	if err != nil {
		out["ok"], out["error"] = false, err.Error()
		writeJSON(w, http.StatusServiceUnavailable, out)
		return
	}
	out["height"], out["aux_difficulty"] = st.Height, st.AuxDifficulty
	if st.Height+1 < pow.MergedMiningActivationHeight {
		out["ok"], out["error"] = false, "merged mining isn't active yet"
		writeJSON(w, http.StatusServiceUnavailable, out)
		return
	}
	out["ok"] = true
	writeJSON(w, http.StatusOK, out)
}

// metrics serves the counters in Prometheus' text format.
func (b *bridge) metrics(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	fmt.Fprintln(w, "# HELP auxpowd_templates_total Work templates handed out.")
	fmt.Fprintln(w, "# TYPE auxpowd_templates_total counter")
	fmt.Fprintf(w, "auxpowd_templates_total %d\n", b.m.templates.Load())
	fmt.Fprintln(w, "# HELP auxpowd_shares_total Proofs received, by outcome.")
	fmt.Fprintln(w, "# TYPE auxpowd_shares_total counter")
	for _, c := range []struct {
		name string
		v    int64
	}{
		{"invalid", b.m.invalid.Load()},
		{"stale", b.m.stale.Load()},
		{"submitted", b.m.submitted.Load()},
		{"accepted", b.m.accepted.Load()},
		{"rejected", b.m.rejected.Load()},
		{"failed", b.m.failed.Load()},
	} {
		fmt.Fprintf(w, "auxpowd_shares_total{outcome=%q} %d\n", c.name, c.v)
	}
}
