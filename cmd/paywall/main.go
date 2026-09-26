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
// wait. That file holds customers' balances: back it up. Add
// --payout-key to let agents withdraw what they haven't spent: that
// keyring account pays withdrawals, so keep only a float in it.
//
// With --pull-key it also offers the aether-pull scheme: an agent grants
// that keyring account a capped, expiring allowance on chain (payable
// only to --pay-to) and pays per request by signature; what it owes is
// collected in batches under the allowance, so nothing sits with the
// seller. --pull-key needs no funds. What buyers owe is kept in
// --pull-ledger (default: the --prepaid-ledger file).
//
// With --receipt-key every paid response carries a signed receipt. The
// key must be --pay-to's, or one --pay-to delegated receipts to:
//
//	paywall delegate-receipts --payee-key <name> --signer <address> --keyring-dir <dir>
//
// run where the payee key is, then pass the file as --receipt-delegation.
//
// The upstream must not be reachable except through this proxy, or
// clients can skip paying. Paid requests reach it with X-PAYMENT
// removed and X-Aether-Payer / X-Aether-Payment-Tx added.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"

	"github.com/whoyoujoshin/aether/app"
	"github.com/whoyoujoshin/aether/crypto/mldsa"
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
	payoutKey := flag.String("payout-key", "", "keyring account that pays back unspent prepaid balances on request (needs --prepaid-ledger); keep only a small float in it")
	pullKey := flag.String("pull-key", "", "keyring account agents grant allowances to; setting it also offers the aether-pull scheme (pay per request from a capped on-chain allowance, collected in batches -- for agents). It needs no funds")
	pullLedgerPath := flag.String("pull-ledger", "", "file recording what aether-pull buyers owe (default: the --prepaid-ledger file)")
	pullCredit := flag.String("pull-credit", "", `most a buyer may owe before it's collected, e.g. "1 AETH" -- also what you lose if one revokes just before a collection (default: 100 requests)`)
	pullEvery := flag.Duration("pull-collect-every", time.Minute, "how often what aether-pull buyers owe is collected")
	receiptKey := flag.String("receipt-key", "", "keyring account that signs a receipt for every paid response: --pay-to's own key, or one it delegated receipts to (--receipt-delegation)")
	receiptDelegation := flag.String("receipt-delegation", "", "file from `paywall delegate-receipts`, letting --receipt-key sign for --pay-to so its key can stay offline")
	keyringDir := flag.String("keyring-dir", "", "keyring directory holding --payout-key and --receipt-key")
	keyringBackend := flag.String("keyring-backend", "test", "keyring backend holding --payout-key and --receipt-key")
	if len(os.Args) > 1 && os.Args[1] == "delegate-receipts" {
		delegateReceipts(os.Args[2:])
		return
	}
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
	var prepaidFile *paywall.FileLedger
	if *prepaidLedger != "" {
		ledger, err := paywall.NewFileLedger(*prepaidLedger)
		if err != nil {
			log.Fatal(err)
		}
		prepaidFile = ledger
		cfg.Prepaid = &paywall.PrepaidConfig{Ledger: ledger}
		if *minDeposit != "" {
			if cfg.Prepaid.MinDeposit, err = wallet.ParseAmount(*minDeposit); err != nil {
				log.Fatalf("invalid --min-deposit: %v", err)
			}
		}
	}
	if *payoutKey != "" {
		if cfg.Prepaid == nil {
			log.Fatal("--payout-key pays back prepaid balances: it needs --prepaid-ledger")
		}
		w := openKeyring(*keyringDir, *keyringBackend, "--payout-key")
		acc, err := w.GetAccount(*payoutKey)
		if err != nil {
			log.Fatalf("--payout-key %q: %v", *payoutKey, err)
		}
		cfg.Prepaid.Payout = &paywall.ChainPayout{Wallet: w, KeyName: *payoutKey, Address: acc.Address, Chain: client, ChainID: *chainID}
		log.Printf("paywall: withdrawals of unspent prepaid balances are paid from %s (%s)", *payoutKey, acc.Address)
	}
	if *pullKey != "" {
		ledger := prepaidFile
		if *pullLedgerPath != "" && *pullLedgerPath != *prepaidLedger {
			if ledger, err = paywall.NewFileLedger(*pullLedgerPath); err != nil {
				log.Fatal(err)
			}
		}
		if ledger == nil {
			log.Fatal("--pull-key needs --pull-ledger (or --prepaid-ledger) to record what buyers owe")
		}
		w := openKeyring(*keyringDir, *keyringBackend, "--pull-key")
		acc, err := w.GetAccount(*pullKey)
		if err != nil {
			log.Fatalf("--pull-key %q: %v", *pullKey, err)
		}
		collector := &paywall.ChainCollector{ChainPayout: paywall.ChainPayout{Wallet: w, KeyName: *pullKey, Address: acc.Address, Chain: client, ChainID: *chainID}, PayTo: *payTo}
		cfg.Pull = &paywall.PullConfig{Ledger: ledger, Collector: collector, Grantee: acc.Address, CollectEvery: *pullEvery, Grants: client.GetSendGrant}
		if *pullCredit != "" {
			if cfg.Pull.Credit, err = wallet.ParseAmount(*pullCredit); err != nil {
				log.Fatalf("invalid --pull-credit: %v", err)
			}
		}
		log.Printf("paywall: aether-pull allowances are granted to %s (%s) and collected every %s", *pullKey, acc.Address, *pullEvery)
	}
	if *receiptKey != "" {
		w := openKeyring(*keyringDir, *keyringBackend, "--receipt-key")
		acc, err := w.GetAccount(*receiptKey)
		if err != nil {
			log.Fatalf("--receipt-key %q: %v", *receiptKey, err)
		}
		cfg.Receipts = &paywall.ReceiptConfig{Sign: func(msg []byte) ([]byte, []byte, error) { return w.SignBytes(*receiptKey, msg) }}
		if *receiptDelegation != "" {
			bz, err := os.ReadFile(*receiptDelegation)
			if err != nil {
				log.Fatal(err)
			}
			cfg.Receipts.Delegation = &paywall.ReceiptDelegation{}
			if err := json.Unmarshal(bz, cfg.Receipts.Delegation); err != nil {
				log.Fatalf("--receipt-delegation: %v", err)
			}
		} else if acc.Address != *payTo {
			log.Fatalf("--receipt-key %s isn't --pay-to: create a delegation with `paywall delegate-receipts` and pass --receipt-delegation", acc.Address)
		}
		log.Printf("paywall: paid responses carry receipts signed by %s (%s)", *receiptKey, acc.Address)
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

	handler := newHandler(proxy, paid, freePrefixes, pw.ManifestHandler(*name, *description), pw.WithdrawHandler())

	log.Printf("paywall: %s AETH per request to %s, proxying %s on %s", wallet.FormatAeth(amount), *payTo, target, *listen)
	if *publicURL != "" {
		u, err := directory.NormalizeURL(*publicURL)
		if err != nil {
			log.Fatalf("invalid --public-url: %v", err)
		}
		log.Printf("paywall: to list this service in the directory, send %d uaeth from %s:\n  aetherd tx bank send <%s key> %s %duaeth --note %q --chain-id %s",
			directory.AnnounceAmount, *payTo, *payTo, directory.Address(), directory.AnnounceAmount, directory.AnnouncePrefix+u, *chainID)
	}
	if cfg.Pull != nil {
		go pw.RunCollector(context.Background())
	}
	srv := &http.Server{Addr: *listen, Handler: handler, ReadHeaderTimeout: 10 * time.Second}
	log.Fatal(srv.ListenAndServe())
}

func openKeyring(dir, backend, flagName string) *wallet.Wallet {
	if dir == "" {
		log.Fatalf("%s needs --keyring-dir", flagName)
	}
	registry := codectypes.NewInterfaceRegistry()
	mldsa.RegisterInterfaces(registry)
	w, err := wallet.NewWallet("aetherd", backend, dir, codec.NewProtoCodec(registry))
	if err != nil {
		log.Fatalf("opening the keyring: %v", err)
	}
	return w
}

// delegateReceipts signs, with the payee's key, a delegation letting
// another key sign receipts for it -- so the payee's key can stay off the
// server. Run it where the payee's key is; copy the output to the server.
func delegateReceipts(args []string) {
	fs := flag.NewFlagSet("delegate-receipts", flag.ExitOnError)
	payeeKey := fs.String("payee-key", "", "keyring account payments go to (--pay-to's key)")
	signer := fs.String("signer", "", "address of the key that will sign receipts on the server (--receipt-key)")
	valid := fs.Duration("valid-for", 365*24*time.Hour, "how long the delegation lasts")
	keyringDir := fs.String("keyring-dir", "", "keyring directory holding --payee-key")
	keyringBackend := fs.String("keyring-backend", "test", "keyring backend holding --payee-key")
	out := fs.String("out", "receipt-delegation.json", "file to write")
	_ = fs.Parse(args)
	if *payeeKey == "" || *signer == "" {
		log.Fatal("--payee-key and --signer are required")
	}
	w := openKeyring(*keyringDir, *keyringBackend, "--payee-key")
	acc, err := w.GetAccount(*payeeKey)
	if err != nil {
		log.Fatalf("--payee-key %q: %v", *payeeKey, err)
	}
	d, err := paywall.NewReceiptDelegation(acc.Address, *signer, time.Now().Add(*valid).Unix(),
		func(msg []byte) ([]byte, []byte, error) { return w.SignBytes(*payeeKey, msg) })
	if err != nil {
		log.Fatal(err)
	}
	bz, _ := json.MarshalIndent(d, "", "  ")
	if err := os.WriteFile(*out, append(bz, '\n'), 0o644); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("%s may sign receipts for %s until %s. Wrote %s: pass it to the paywall as --receipt-delegation.\n",
		*signer, acc.Address, time.Unix(d.Expires, 0).UTC().Format(time.RFC3339), *out)
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

func newHandler(upstream, paid http.Handler, freePrefixes []string, manifest, withdraw http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Only the paywall may say who paid.
		r.Header.Del(headerPayer)
		r.Header.Del(headerPaymentTx)
		switch r.URL.Path {
		case paywall.ManifestPath:
			manifest.ServeHTTP(w, r)
			return
		case paywall.WithdrawPath:
			withdraw.ServeHTTP(w, r)
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
