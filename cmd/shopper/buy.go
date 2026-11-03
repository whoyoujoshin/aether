package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/whoyoujoshin/aether/crypto/mldsa"
	"github.com/whoyoujoshin/aether/shopper"
	"github.com/whoyoujoshin/aether/wallet"
)

func buyCmd(args []string) int {
	fs := flag.NewFlagSet("buy", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	quoteID := fs.String("quote", "", "quote id from shopper quote")
	key := fs.String("idempotency-key", "", "unique id for this purchase; the same id never pays twice")
	confirm := fs.Bool("confirm", false, "confirm an in-policy buy; without this, nothing is signed")
	auto := fs.Bool("auto", false, "pay an in-policy AETH buy without --confirm (off unless you set it)")
	dry := fs.Bool("dry-run", false, "print the decision and do not sign")
	chainID := fs.String("chain-id", "aether-testnet-1", "chain to pay on")
	grpcAddr := fs.String("grpc", "grpc.157-245-252-221.sslip.io:443", "node gRPC")
	facilitator := fs.String("facilitator", "https://explorer.157-245-252-221.sslip.io/facilitator", "facilitator base URL")
	stateDir := fs.String("state-dir", defaultStateDir(), "where quotes, attempts and receipts are stored")
	keyName := fs.String("from", "", "key name in the keyring")
	keyringBackend := fs.String("keyring-backend", "file", "keyring backend: file or test")
	keyringDir := fs.String("keyring-dir", "", "keyring directory")
	passFile := fs.String("keyring-passphrase-file", "", "file holding the file keyring passphrase; also read from "+wallet.KeyringPassphraseFileEnv)
	timeout := fs.Duration("timeout", 150*time.Second, "how long to wait for settlement")
	fees := wallet.FeeFlags(fs)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if err := fees.Validate(); err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		return 2
	}
	if *quoteID == "" || *key == "" || *keyName == "" || *keyringDir == "" {
		fmt.Fprintln(os.Stderr, "buy needs --quote, --idempotency-key, --from and --keyring-dir")
		return 2
	}
	// The passphrase stays in the file. It is not printed.
	if *passFile != "" {
		if err := os.Setenv(wallet.KeyringPassphraseFileEnv, *passFile); err != nil {
			fmt.Fprintln(os.Stderr, "could not set the passphrase file")
			return 2
		}
	}
	w, err := openShopperWallet(*keyringBackend, *keyringDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "keyring: %s\n", err.Error())
		return 2
	}
	node, err := wallet.NewClient(*grpcAddr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "node: %s\n", err.Error())
		return 4
	}
	defer node.Close()

	assets, err := wallet.NewAssetsFor(wallet.TestnetUSDC)
	if err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		return 2
	}
	pol := shopper.DefaultCaps(usdcDenom(assets))
	pol.Auto = *auto
	ctx, cancel := context.WithTimeout(context.Background(), *timeout+30*time.Second)
	defer cancel()
	httpClient := &http.Client{Timeout: *timeout, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	res, err := (shopper.Buyer{
		ChainID: *chainID, StateDir: *stateDir, Policy: pol, Wallet: w, KeyName: *keyName,
		Facilitator: shopper.Facilitator{BaseURL: *facilitator, Client: httpClient},
		Chain:       grpcChain{c: node, fees: *fees}, HTTP: httpClient,
	}).Buy(ctx, shopper.BuyInput{QuoteID: *quoteID, IdempotencyKey: *key, Confirm: *confirm, DryRun: *dry})
	if res.Decision.Message != "" {
		fmt.Println(res.Decision.Message)
	}
	if res.Receipt != nil && res.Receipt.TxHash != "" {
		fmt.Printf("Payment: %s\n", res.Receipt.TxHash)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
	}
	return exitCode(res.Decision, err)
}

func exitCode(d shopper.Decision, err error) int {
	code := d.Code
	var se *shopper.Error
	if errors.As(err, &se) {
		code = se.Code
	}
	switch code {
	case shopper.CodeAlreadyPaid, shopper.CodeDryRun:
		return 0
	case shopper.CodeNeedsConfirm, shopper.CodeNeedsApproval, shopper.CodeUSDCNeedsApproval:
		return 3
	case shopper.CodeFacilitatorDown, shopper.CodeSettlementUnknown:
		return 4
	case "":
		if err != nil {
			return 4
		}
		return 0
	default:
		return 5
	}
}

func openShopperWallet(backend, dir string) (*wallet.Wallet, error) {
	registry := codectypes.NewInterfaceRegistry()
	mldsa.RegisterInterfaces(registry)
	return wallet.NewWallet("aetherd", backend, dir, codec.NewProtoCodec(registry))
}

// grpcChain reads account numbers, the fee, and transaction results. It does not broadcast.
type grpcChain struct {
	c    *wallet.Client
	fees wallet.FeeSetting
}

func (g grpcChain) Account(_ context.Context, address string) (uint64, uint64, error) {
	return g.c.GetAccountInfo(address)
}

func (g grpcChain) Fee(ctx context.Context, gas uint64, denom string) (sdk.Coins, error) {
	return g.c.Fee(ctx, g.fees, gas, denom)
}

func (g grpcChain) Tx(_ context.Context, hash string) (bool, uint32, int64, error) {
	d, err := g.c.GetTransactionByHash(hash)
	if errors.Is(err, wallet.ErrTransactionNotFound) {
		return false, 0, 0, nil
	}
	if err != nil {
		return false, 0, 0, err
	}
	return true, d.Code, d.Height, nil
}
