// cmd/facilitator is an x402 (v2) facilitator for the exact scheme on
// Aether (network cosmos:<chain-id>; see package x402). A resource server
// sends it a client's payment to check (POST /verify) or to put on chain
// (POST /settle); GET /supported names what it handles.
//
//	go run ./cmd/facilitator --listen :8403 --grpc localhost:9090 --chain-id aether-testnet-1
//
// It holds no key: the payer signs the whole transaction, pays its fee,
// and this only checks and broadcasts it. Anyone may call it: it handles
// at most --max-concurrent payments at once.
package main

import (
	"encoding/json"
	"flag"
	"log"
	"net/http"
	"time"

	"github.com/whoyoujoshin/aether/app"
	"github.com/whoyoujoshin/aether/wallet"
	"github.com/whoyoujoshin/aether/x402"
)

// maxBody bounds a /verify or /settle body: the transaction is at most
// 16 KB, about 22 KB in base64, and it appears once.
const maxBody = 64 << 10

func main() {
	app.SetAddressPrefixes()

	listen := flag.String("listen", ":8403", "address to serve on")
	grpcEndpoint := flag.String("grpc", "localhost:9090", "node gRPC endpoint")
	chainID := flag.String("chain-id", "aether-testnet-1", "chain payments are on")
	maxConcurrent := flag.Int("max-concurrent", 32, "most /verify and /settle requests handled at once (each simulates on the node); more get 503")
	settleTimeout := flag.Duration("settle-timeout", 80*time.Second, "how long /settle waits for the transaction to land in a block (a requirement's maxTimeoutSeconds, when larger, wins); the default answers before @x402/core's 90s facilitator timeout")
	flag.Parse()

	client, err := wallet.NewClient(*grpcEndpoint)
	if err != nil {
		log.Fatalf("connect to %s: %v", *grpcEndpoint, err)
	}
	f := x402.NewFacilitator(*chainID, x402.NodeChain{Client: client})
	f.SettleTimeout = *settleTimeout

	srv := &http.Server{
		Addr:              *listen,
		Handler:           limit(newHandler(f), *maxConcurrent),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		// /settle waits for a block.
		WriteTimeout: *settleTimeout + 30*time.Second,
	}
	log.Printf("x402 facilitator for %s on %s (node %s)", x402.Network(*chainID), *listen, *grpcEndpoint)
	log.Fatal(srv.ListenAndServe())
}

func newHandler(f *x402.Facilitator) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /supported", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, f.Supported())
	})
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		if _, err := f.Chain.LatestHeight(r.Context()); err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"ok": false, "error": "node unreachable"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "network": x402.Network(f.ChainID)})
	})
	mux.HandleFunc("POST /verify", func(w http.ResponseWriter, r *http.Request) {
		req, ok := readRequest(w, r)
		if !ok {
			writeJSON(w, http.StatusBadRequest, x402.VerifyResponse{InvalidReason: x402.ReasonPayload})
			return
		}
		writeJSON(w, http.StatusOK, f.Verify(r.Context(), req.PaymentPayload, req.PaymentRequirements))
	})
	mux.HandleFunc("POST /settle", func(w http.ResponseWriter, r *http.Request) {
		req, ok := readRequest(w, r)
		if !ok {
			writeJSON(w, http.StatusBadRequest, x402.SettleResponse{ErrorReason: x402.ReasonPayload, Network: x402.Network(f.ChainID)})
			return
		}
		resp := f.Settle(r.Context(), req.PaymentPayload, req.PaymentRequirements)
		if resp.Success {
			log.Printf("settled %s %s from %s: %s", resp.Amount, req.PaymentRequirements.Asset, resp.Payer, resp.Transaction)
		}
		writeJSON(w, http.StatusOK, resp)
	})
	return mux
}

// limit answers 503 to POSTs beyond max at once, so a flood can't queue
// unbounded work on the node.
func limit(next http.Handler, max int) http.Handler {
	slots := make(chan struct{}, max)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			next.ServeHTTP(w, r)
			return
		}
		select {
		case slots <- struct{}{}:
			defer func() { <-slots }()
			next.ServeHTTP(w, r)
		default:
			w.Header().Set("Retry-After", "1")
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "busy"})
		}
	})
}

func readRequest(w http.ResponseWriter, r *http.Request) (x402.VerifyRequest, bool) {
	var req x402.VerifyRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody))
	if err := dec.Decode(&req); err != nil {
		return req, false
	}
	return req, true
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
