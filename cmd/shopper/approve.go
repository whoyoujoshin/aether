package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/whoyoujoshin/aether/shopper"
	"github.com/whoyoujoshin/aether/wallet"
)

func approveCmd(args []string, reject bool) int {
	fs := flag.NewFlagSet("approve", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	stateDir := fs.String("state-dir", defaultStateDir(), "shopper state directory")
	keyName := fs.String("approver-key", "", "approver key name; this must not be the spending key")
	backend := fs.String("keyring-backend", "file", "keyring backend: file or test")
	dir := fs.String("keyring-dir", "", "approver keyring directory")
	passFile := fs.String("keyring-passphrase-file", "", "passphrase file for a file keyring")
	approver := fs.String("approver", "", "approver address; the signing key must be this account")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 || *keyName == "" || *dir == "" {
		fmt.Fprintln(os.Stderr, "usage: shopper approve <id> --approver-key <name> --keyring-backend file --keyring-dir <dir>")
		return 2
	}
	if *passFile != "" {
		if err := os.Setenv(wallet.KeyringPassphraseFileEnv, *passFile); err != nil {
			fmt.Fprintln(os.Stderr, "could not set the passphrase file")
			return 2
		}
	}
	w, err := openShopperWallet(*backend, *dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "keyring: %s\n", err.Error())
		return 2
	}
	decision := "approve"
	if reject {
		decision = "reject"
	}
	if err := shopper.SignApproval(*stateDir, fs.Arg(0), decision, *approver, w, *keyName); err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		return 5
	}
	if reject {
		fmt.Println("Rejected. Nothing was spent.")
	} else {
		fmt.Println("Approved. Run the buy again with the same idempotency key.")
	}
	return 0
}
