package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"time"

	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/whoyoujoshin/aether/directory"
	"github.com/whoyoujoshin/aether/wallet"
)

const listGas = 400_000

// listService lists a paywalled service in the on-chain directory, or
// with --delist withdraws it: a 1 uaeth send from the payee's key to the
// directory address with the URL in the memo. Before listing it reads the
// manifest at that URL and checks what the directory will check, that the
// payee it names is this key's account, so a listing can't be sent that
// agents would never see.
//
//	paywall list --url https://api.example.com/svc --key payee --keyring-dir ~/.aether-payee
func listService(args []string) {
	fs := flag.NewFlagSet("list", flag.ExitOnError)
	publicURL := fs.String("url", "", "the service's public base URL, as the paywall's --public-url")
	key := fs.String("key", "", "keyring account of the payee (the paywall's --pay-to): only the payee's own announcement lists a service")
	keyringDir := fs.String("keyring-dir", "", "keyring directory holding --key")
	keyringBackend := fs.String("keyring-backend", "test", "keyring backend holding --key")
	grpcEndpoint := fs.String("grpc", "grpc.157-245-252-221.sslip.io:443", "node gRPC endpoint (the public testnet node by default)")
	chainID := fs.String("chain-id", "aether-testnet-1", "chain ID")
	fees := fs.String("fees", "0uaeth", "transaction fee (fees are zero on the testnet)")
	delist := fs.Bool("delist", false, "withdraw the listing instead")
	allowPrivate := fs.Bool("allow-private", false, "devnets only: read the manifest from a local or private address")
	wait := fs.Duration("wait", 3*time.Minute, "how long to wait for the transaction to land in a block (0: don't wait)")
	_ = fs.Parse(args)
	if *publicURL == "" || *key == "" {
		log.Fatal("--url and --key are required")
	}
	u, err := directory.NormalizeURL(*publicURL)
	if err != nil {
		log.Fatalf("invalid --url: %v", err)
	}
	feeCoins, err := sdk.ParseCoinsNormalized(*fees)
	if err != nil {
		log.Fatalf("invalid --fees: %v", err)
	}
	w := openKeyring(*keyringDir, *keyringBackend, "--key")
	acc, err := w.GetAccount(*key)
	if err != nil {
		log.Fatalf("--key %q: %v", *key, err)
	}

	memo := directory.AnnouncePrefix + u
	if *delist {
		memo = directory.DelistPrefix + u
	} else {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		m, err := directory.SafeFetcher(*allowPrivate)(ctx, u)
		cancel()
		if err != nil {
			log.Fatalf("can't read the manifest at %s/.well-known/x402: %v\nStart the paywall first, reachable at that URL.", u, err)
		}
		if err := directory.Verify(directory.Announcement{URL: u, Announcer: acc.Address}, m, *chainID); err != nil {
			log.Fatalf("the directory wouldn't list it: %v\nThe paywall's --pay-to must be %s (this key's account) and its --chain-id %s.", err, acc.Address, *chainID)
		}
		fmt.Printf("Manifest OK: %q, paid to %s.\n", m.Name, m.PayTo)
	}

	c, err := wallet.NewClient(*grpcEndpoint)
	if err != nil {
		log.Fatal(err)
	}
	defer c.Close()
	number, sequence, err := c.GetAccountInfo(acc.Address)
	if err != nil {
		log.Fatalf("%s isn't on chain yet (%v): it needs %d uaeth for the listing; ask the faucet for some", acc.Address, err, directory.AnnounceAmount)
	}
	signed, err := w.BuildAndSignSendTx(*key, acc.Address, directory.Address(),
		sdk.NewCoins(sdk.NewCoin(wallet.BaseDenom, math.NewInt(directory.AnnounceAmount))),
		wallet.TxParams{ChainID: *chainID, AccountNumber: number, Sequence: sequence, GasLimit: listGas, Fees: feeCoins, Memo: memo})
	if err != nil {
		log.Fatal(err)
	}
	res, err := c.BroadcastTx(signed)
	if err != nil {
		log.Fatalf("broadcasting: %v", err)
	}
	if res.Code != 0 {
		log.Fatalf("the node refused the transaction (code %d): %s", res.Code, res.RawLog)
	}
	action := "listing"
	if *delist {
		action = "delisting"
	}
	fmt.Printf("Sent the %s of %s in %s.\n", action, u, res.TxHash)
	if *wait <= 0 {
		return
	}
	deadline := time.Now().Add(*wait)
	for time.Now().Before(deadline) {
		tx, err := c.GetTransactionByHash(res.TxHash)
		switch {
		case err == nil && tx.Code != 0:
			log.Fatalf("the transaction failed in block %d: %s", tx.Height, tx.RawLog)
		case err == nil:
			if *delist {
				fmt.Printf("Delisted at block %d.\n", tx.Height)
			} else {
				fmt.Printf("Listed at block %d. Agents find it with find_services (directories refresh every few minutes).\n", tx.Height)
			}
			return
		case !errors.Is(err, wallet.ErrTransactionNotFound):
			log.Printf("checking the transaction: %v", err)
		}
		time.Sleep(3 * time.Second)
	}
	log.Fatalf("not in a block after %s: check %s on the explorer before sending it again", *wait, res.TxHash)
}
