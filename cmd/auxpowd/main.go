// cmd/auxpowd is the merged-mining bridge a Litecoin pool runs beside its
// own node (docs/MERGED-MINING-PLAN.md). It speaks the merged-mining
// JSON-RPC Namecoin and Dogecoin use, so pool software that merge-mines
// those needs configuration, not code:
//
//	createauxblock <aether address>   work paid to that address
//	submitauxblock <hash> <auxpow>    a proof for that work
//	getauxblock [<hash> <auxpow>]     the legacy form of both
//	getblockcount                     the Aether tip
//
// It also answers getblocktemplate, validateaddress, getdifficulty and
// getmininginfo in bitcoind's shapes, for pool software that treats a
// merged-mined daemon as a full node (yiimp does).
//
// Each template commits to a recent Aether block and the reward address
// (x/pow.AuxPoWTemplateHash), so the reward goes to that address whoever
// relays the proof. A submission is checked locally with x/pow's own
// CheckAuxPow, then signed with the bridge's key and broadcast. The key
// pays only the fee, zero by default, so it needs no funds.
//
// The bridge refuses work until the chain reaches
// x/pow.MergedMiningActivationHeight: below it a submission's reward
// would go to the bridge's own key.
//
// Usage:
//
//	auxpowd --from bridge --rpc-user pool --rpc-password-file /etc/auxpowd.pass \
//	  [--node http://127.0.0.1:26657] [--grpc 127.0.0.1:9090] [--listen 127.0.0.1:8336]
//
// Bind --listen to the pool's private network: it hands out work and
// takes proofs for anyone holding the password.
package main

import (
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"google.golang.org/grpc"

	"github.com/whoyoujoshin/aether/app"
	"github.com/whoyoujoshin/aether/crypto/mldsa"
	"github.com/whoyoujoshin/aether/wallet"
)

func init() {
	app.SetAddressPrefixes()
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "auxpowd:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	fs := flag.NewFlagSet("auxpowd", flag.ContinueOnError)
	listen := fs.String("listen", "127.0.0.1:8336", "address for the JSON-RPC, /health and /metrics server; keep it on the pool's private network")
	node := fs.String("node", "http://127.0.0.1:26657", "Aether node's CometBFT RPC")
	grpcAddr := fs.String("grpc", "127.0.0.1:9090", "Aether node's gRPC")
	home := fs.String("home", defaultHome(), "aetherd home whose keyring holds --from")
	keyringBackend := fs.String("keyring-backend", "test", "keyring backend")
	from := fs.String("from", "", "keyring name of the bridge's ML-DSA key, which signs submissions (required)")
	feesStr := fs.String("fees", "0uaeth", "fee per submission, paid by --from")
	gas := fs.Uint64("gas", 400_000, "gas limit per submission")
	rewardStr := fs.String("reward-address", "", "aether1... address getauxblock (no arguments) pays; createauxblock names its own")
	rpcUser := fs.String("rpc-user", "", "JSON-RPC username (required)")
	rpcPassword := fs.String("rpc-password", "", "JSON-RPC password; prefer --rpc-password-file")
	rpcPasswordFile := fs.String("rpc-password-file", "", "file holding the JSON-RPC password")
	if err := fs.Parse(args); err != nil {
		return err
	}

	password := *rpcPassword
	if *rpcPasswordFile != "" {
		bz, err := os.ReadFile(*rpcPasswordFile)
		if err != nil {
			return err
		}
		password = strings.TrimSpace(string(bz))
	}
	switch {
	case *from == "":
		return errors.New("--from is required")
	case *rpcUser == "" || password == "":
		return errors.New("--rpc-user and a password (--rpc-password-file or --rpc-password) are required")
	}
	fees, err := sdk.ParseCoinsNormalized(*feesStr)
	if err != nil {
		return fmt.Errorf("--fees: %w", err)
	}
	var defaultReward sdk.AccAddress
	if *rewardStr != "" {
		if defaultReward, err = sdk.AccAddressFromBech32(*rewardStr); err != nil {
			return fmt.Errorf("--reward-address: %w", err)
		}
	}

	registry := codectypes.NewInterfaceRegistry()
	mldsa.RegisterInterfaces(registry)
	w, err := wallet.NewWallet("aetherd", *keyringBackend, *home, codec.NewProtoCodec(registry))
	if err != nil {
		return fmt.Errorf("keyring: %w", err)
	}
	acct, err := w.GetAccount(*from)
	if err != nil {
		return fmt.Errorf("--from %q: %w", *from, err)
	}
	client, err := wallet.NewClient(*grpcAddr)
	if err != nil {
		return fmt.Errorf("gRPC %s: %w", *grpcAddr, err)
	}
	defer client.Close()
	conn, err := grpc.NewClient(*grpcAddr, grpc.WithTransportCredentials(wallet.GRPCCredentials(*grpcAddr)))
	if err != nil {
		return fmt.Errorf("gRPC %s: %w", *grpcAddr, err)
	}
	defer conn.Close()
	c, err := newNodeChain(*node, conn, w, client, *from, acct.Address, fees, *gas)
	if err != nil {
		return err
	}

	logger := log.New(os.Stderr, "auxpowd: ", log.LstdFlags)
	b := newBridge(c, defaultReward, logger)
	logger.Printf("listening on %s; signing with %s (%s); node %s", *listen, *from, acct.Address, *node)
	srv := &http.Server{
		Addr:              *listen,
		Handler:           b.handler(*rpcUser, password),
		ReadHeaderTimeout: 10 * time.Second,
	}
	return srv.ListenAndServe()
}

func defaultHome() string {
	dir, err := os.UserHomeDir()
	if err != nil {
		return ".aether"
	}
	return filepath.Join(dir, ".aether")
}
