// cmd/paywall puts any HTTP API behind per-request AETH payments: a
// reverse proxy that answers unpaid requests with HTTP 402 (x402 wire
// format, "aether-memo" scheme -- see package paywall) and forwards
// paid ones to --upstream.
//
//	go run ./cmd/paywall --upstream http://localhost:8000 --pay-to aether1... \
//	    --price "0.01 AETH" --grpc localhost:9090 --chain-id aether-testnet-1
//
// With --prepaid-ledger it also offers the aether-prepaid scheme: an
// agent deposits once and pays per request by signature, with no block
// wait. That file holds customers' balances: back it up.
//
// The upstream must not be reachable except through this proxy, or
// clients can skip paying. Paid requests reach it with X-PAYMENT
// removed and X-Aether-Payer / X-Aether-Payment-Tx added.
package main

import (
	"flag"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"github.com/whoyoujoshin/aether/app"
	"github.com/whoyoujoshin/aether/directory"
	"github.com/whoyoujoshin/aether/paywall"
	"github.com/whoyoujoshin/aether/wallet"
)

func main() {
	app.SetAddressPrefixes()

	listen := flag.String("listen", ":8402", "address to serve on")
	upstream := flag.String("upstream", "", "URL of the API to charge for (required)")
	payTo := flag.String("pay-to", "", "address payments go to (required)")
	price := flag.String("price", "", `price per request with its unit, e.g. "0.01 AETH" (required)`)
	grpcEndpoint := flag.String("grpc", "localhost:9090", "node gRPC endpoint used to verify payments")
	chainID := flag.String("chain-id", "aether-testnet-1", "chain ID payments must be on")
	description := flag.String("description", "", "what a payment buys, shown to payers and in the service directory")
	name := flag.String("name", "", "the service's name in its manifest and the service directory")
	publicURL := flag.String("public-url", "", "the URL clients reach this proxy at; prints how to list it in the service directory")
	free := flag.String("free", "", "comma-separated path prefixes served without payment, e.g. /health,/docs")
	ttl := flag.Duration("invoice-ttl", 24*time.Hour, "how long a payer has to pay an invoice and present the payment")
	prepaidLedger := flag.String("prepaid-ledger", "", "file holding prepaid balances; setting it also offers the aether-prepaid scheme (deposit once, then pay per request instantly -- for agents)")
	minDeposit := flag.String("min-deposit", "", `smallest prepaid deposit accepted, e.g. "1 AETH" (default: the price)`)
	flag.Parse()

	if *upstream == "" || *payTo == "" || *price == "" {
		log.Fatal("--upstream, --pay-to and --price are required")
	}
	target, err := url.Parse(*upstream)
	if err != nil || target.Scheme == "" || target.Host == "" {
		log.Fatalf("invalid --upstream %q", *upstream)
	}
	amount, err := wallet.ParseAmount(*price)
	if err != nil {
		log.Fatalf("invalid --price: %v", err)
	}
	client, err := wallet.NewClient(*grpcEndpoint)
	if err != nil {
		log.Fatalf("failed to connect to %s: %v", *grpcEndpoint, err)
	}
	defer client.Close()

	cfg := paywall.Config{
		PayTo: *payTo, Price: amount, Network: *chainID, Description: *description,
		InvoiceTTL: *ttl, Lookup: client.GetTransactionByHash,
	}
	if *prepaidLedger != "" {
		ledger, err := paywall.NewFileLedger(*prepaidLedger)
		if err != nil {
			log.Fatal(err)
		}
		cfg.Prepaid = &paywall.PrepaidConfig{Ledger: ledger}
		if *minDeposit != "" {
			if cfg.Prepaid.MinDeposit, err = wallet.ParseAmount(*minDeposit); err != nil {
				log.Fatalf("invalid --min-deposit: %v", err)
			}
		}
	}
	pw, err := paywall.New(cfg)
	if err != nil {
		log.Fatal(err)
	}

	proxy := newProxy(target)
	paid := pw.Middleware(withPayerHeaders(proxy))
	var freePrefixes []string
	for _, p := range strings.Split(*free, ",") {
		if p = strings.TrimSpace(p); p != "" {
			freePrefixes = append(freePrefixes, p)
		}
	}

	handler := newHandler(proxy, paid, freePrefixes, pw.ManifestHandler(*name, *description))

	log.Printf("paywall: %s AETH per request to %s, proxying %s on %s", wallet.FormatAeth(amount), *payTo, target, *listen)
	if *publicURL != "" {
		u, err := directory.NormalizeURL(*publicURL)
		if err != nil {
			log.Fatalf("invalid --public-url: %v", err)
		}
		log.Printf("paywall: to list this service in the directory, send %d uaeth from %s:\n  aetherd tx bank send <%s key> %s %duaeth --note %q --chain-id %s",
			directory.AnnounceAmount, *payTo, *payTo, directory.Address(), directory.AnnounceAmount, directory.AnnouncePrefix+u, *chainID)
	}
	srv := &http.Server{Addr: *listen, Handler: handler, ReadHeaderTimeout: 10 * time.Second}
	log.Fatal(srv.ListenAndServe())
}

const (
	headerPayer     = "X-Aether-Payer"
	headerPaymentTx = "X-Aether-Payment-Tx"
)

// withPayerHeaders tells the upstream who paid, from the settlement
// the paywall recorded.
func withPayerHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var s paywall.SettlementResponse
		if paywall.DecodeHeader(w.Header().Get(paywall.HeaderPaymentResponse), &s) == nil {
			r.Header.Set(headerPayer, s.Payer)
			r.Header.Set(headerPaymentTx, s.Transaction)
		}
		next.ServeHTTP(w, r)
	})
}

// newProxy forwards to target without the payment proof.
func newProxy(target *url.URL) *httputil.ReverseProxy {
	proxy := httputil.NewSingleHostReverseProxy(target)
	forward := proxy.Director
	proxy.Director = func(r *http.Request) {
		forward(r)
		r.Header.Del(paywall.HeaderPayment)
	}
	return proxy
}

func newHandler(upstream, paid http.Handler, freePrefixes []string, manifest http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Only the paywall may say who paid.
		r.Header.Del(headerPayer)
		r.Header.Del(headerPaymentTx)
		if r.URL.Path == paywall.ManifestPath {
			manifest.ServeHTTP(w, r)
			return
		}
		for _, p := range freePrefixes {
			if strings.HasPrefix(r.URL.Path, p) {
				upstream.ServeHTTP(w, r)
				return
			}
		}
		paid.ServeHTTP(w, r)
	})
}
