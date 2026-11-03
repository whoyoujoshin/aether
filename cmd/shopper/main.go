// Command shopper compares paid services on Aether.
//
//	shopper quote --query "block" --max-price "0.1 AETH"
//
// Quote never signs a transaction and never broadcasts one. Keys are
// not read. Nothing here is logged that could be a key, a passphrase,
// or a signature, because quote does not have them.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/whoyoujoshin/aether/app"
	"github.com/whoyoujoshin/aether/directory"
	"github.com/whoyoujoshin/aether/shopper"
	"github.com/whoyoujoshin/aether/wallet"
)

func main() {
	app.SetAddressPrefixes()
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "quote":
		os.Exit(quoteCmd(os.Args[2:]))
	case "-h", "--help", "help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n", os.Args[1])
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, `shopper compares paid services and, in later commands, pays one.

  shopper quote --query "block" --max-price "0.1 AETH"

Quote does not spend. A buy is a later command and asks you to confirm.
`)
}

func quoteCmd(args []string) int {
	fs := flag.NewFlagSet("quote", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	query := fs.String("query", "", "words to look for, such as block or hello")
	maxPrice := fs.String("max-price", "", "most this search may consider, such as \"0.1 AETH\"")
	rawURL := fs.String("url", "", "optional exact link to check")
	method := fs.String("method", "GET", "HTTP method used to read the live price")
	chainID := fs.String("chain-id", "aether-testnet-1", "chain the offers are on")
	grpcAddr := fs.String("grpc", "grpc.157-245-252-221.sslip.io:443", "node gRPC used to read the service directory")
	board := fs.String("job-board", "https://explorer.157-245-252-221.sslip.io/jobs", "job board; empty skips it")
	stateDir := fs.String("state-dir", defaultStateDir(), "where the quote file is written")
	allowPrivate := fs.Bool("allow-private", false, "allow a service URL that points at this machine (devnets)")
	timeout := fs.Duration("timeout", 20*time.Second, "how long one price check may take")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	assets, err := wallet.NewAssetsFor(wallet.TestnetUSDC)
	if err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		return 2
	}
	syns, err := shopper.LoadSynonyms()
	if err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		return 2
	}
	listings, err := loadListings(ctx, *grpcAddr, *chainID, *allowPrivate)
	if err != nil {
		fmt.Fprintf(os.Stderr, "directory: %s\n", err.Error())
		return 4
	}
	client := directory.SafeHTTPClient(*allowPrivate, *timeout)
	var jobs []shopper.JobOffer
	var warnings []string
	if *board != "" && strings.TrimSpace(*query) != "" {
		got, err := shopper.FetchOpenJobs(ctx, client, *board, shopper.QueryVariants(syns, *query))
		if err != nil && len(got) == 0 {
			warnings = append(warnings, "The job board did not answer. Services are still listed.")
		}
		jobs = got
	}
	res, err := shopper.Quote(ctx, shopper.QuoteInput{
		ChainID: *chainID, Listings: listings, Jobs: jobs, Synonyms: syns,
		Policy: shopper.DefaultCaps(usdcDenom(assets)), Assets: assets,
		Request: shopper.Request{Query: *query, MaxPrice: *maxPrice, URL: *rawURL, Method: *method},
		Probe: func(ctx context.Context, method, raw string, body []byte) (shopper.ProbeResult, error) {
			cctx, cancel := context.WithTimeout(ctx, *timeout)
			defer cancel()
			return shopper.ProbePayment(cctx, client, method, raw, body)
		},
	})
	res.Warnings = append(warnings, res.Warnings...)
	if err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		return 2
	}
	if res.Selected != nil && *stateDir != "" {
		if err := shopper.SaveQuote(*stateDir, *chainID, *res.Selected, res.Decision); err != nil {
			fmt.Fprintf(os.Stderr, "could not save the quote: %s\n", err.Error())
		}
	}
	printQuote(res)
	if res.Decision.Code == shopper.CodeNothingFound && len(res.APIs) == 0 && len(res.Jobs) == 0 {
		return 3
	}
	return 0
}

func usdcDenom(a *wallet.Assets) string {
	if u, ok := a.BySymbol("USDC"); ok {
		return u.Denom
	}
	return ""
}

func loadListings(ctx context.Context, grpcAddr, chainID string, allowPrivate bool) ([]directory.Listing, error) {
	client, err := wallet.NewClient(grpcAddr)
	if err != nil {
		return nil, err
	}
	defer client.Close()
	dir := &directory.Directory{
		Network: chainID,
		Fetch:   directory.SafeFetcher(allowPrivate),
		Scan: func() ([]wallet.IncomingPayment, error) {
			return client.GetIncomingPayments(directory.Address(), 1, 5000)
		},
		ScanPayee: func(address string, since int64) ([]wallet.IncomingPayment, error) {
			return client.GetIncomingPayments(address, since, 5000)
		},
		LatestHeight: func() (int64, error) { return client.GetLatestHeight() },
	}
	return dir.Listings(ctx)
}

func printQuote(res shopper.Result) {
	for _, w := range res.Warnings {
		fmt.Fprintf(os.Stderr, "note: %s\n", w)
	}
	fmt.Println(res.Decision.Message)
	if res.Selected != nil {
		fmt.Printf("\nChosen: %s    %s\n", displayName(*res.Selected), res.Selected.PriceDisplay)
		if res.Selected.QuoteID != "" {
			fmt.Printf("Quote:  %s\n", res.Selected.QuoteID)
		}
	}
	if len(res.APIs) > 0 {
		fmt.Println("\nServices")
		for i, o := range res.APIs {
			extra := ""
			if o.Reason != "" && !o.Selectable {
				extra = "  (" + o.Reason + ")"
			}
			fmt.Printf("  %d. %s    %s%s\n", i+1, displayName(o), o.PriceDisplay, extra)
		}
	}
	if len(res.Jobs) > 0 {
		fmt.Println("\nCustom jobs (not ready to buy)")
		for _, o := range res.Jobs {
			price := o.PriceDisplay
			if price == "" {
				price = "no price yet"
			}
			fmt.Printf("  %s    %s\n", displayName(o), price)
		}
	}
}

func displayName(o shopper.Offer) string {
	if o.Name != "" {
		return o.Name
	}
	if o.URL != "" {
		return o.URL
	}
	return o.JobID
}

func defaultStateDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ".aether-shopper"
	}
	return home + string(os.PathSeparator) + ".aether-shopper"
}
