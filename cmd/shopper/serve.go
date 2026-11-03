package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/whoyoujoshin/aether/shopper"
	"github.com/whoyoujoshin/aether/wallet"
)

func serveCmd(args []string) int {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	listen := fs.String("listen", "127.0.0.1:8470", "local address for the approval page")
	stateDir := fs.String("state-dir", defaultStateDir(), "shopper state directory")
	keyName := fs.String("approver-key", "", "approver key; empty shows the request and does not sign")
	backend := fs.String("keyring-backend", "file", "keyring backend: file or test")
	dir := fs.String("keyring-dir", "", "approver keyring directory, required with --approver-key")
	passFile := fs.String("keyring-passphrase-file", "", "passphrase file for a file keyring")
	approver := fs.String("approver", "", "approver address")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if !shopper.Loopback(*listen) {
		fmt.Fprintln(os.Stderr, "serve only listens on localhost. Approvals are not hosted on a public machine.")
		return 2
	}
	page := shopper.ApprovalPage{Dir: *stateDir, Approver: *approver, KeyName: *keyName}
	if *keyName != "" {
		if *dir == "" {
			fmt.Fprintln(os.Stderr, "--approver-key needs --keyring-dir")
			return 2
		}
		if *passFile != "" {
			_ = os.Setenv(wallet.KeyringPassphraseFileEnv, *passFile)
		}
		w, err := openShopperWallet(*backend, *dir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "keyring: %s\n", err.Error())
			return 2
		}
		page.Wallet = w
	}
	srv := &http.Server{Addr: *listen, Handler: page.Handler(), ReadHeaderTimeout: 10 * time.Second}
	fmt.Fprintf(os.Stderr, "approval page on http://%s/approve/<id>\n", *listen)
	if err := srv.ListenAndServe(); err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		return 1
	}
	return 0
}
