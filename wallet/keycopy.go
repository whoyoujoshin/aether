package wallet

import (
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"github.com/cosmos/cosmos-sdk/codec"
	sdk "github.com/cosmos/cosmos-sdk/types"
)

// A "test" keyring keeps each key as two files under <dir>/keyring-test,
// both sealed with the fixed passphrase "test": <name>.info (the key)
// and <hex address>.address (naming it). Copying both moves the key to
// another "test" keyring unchanged. Other backends seal with a user
// passphrase or the OS store, so a key there is recovered from its
// phrase instead.

const testKeyringDir = "keyring-test"

var accountNameRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,32}$`)

// ValidAccountName reports whether name is safe as a key name, which is
// also a file name in the keyring.
func ValidAccountName(name string) bool { return accountNameRe.MatchString(name) }

// CopyTestKey copies the key called name from the test keyring at
// srcDir into the one at dstDir, and returns it as dstDir now holds it.
func CopyTestKey(srcDir, dstDir, name string, cdc codec.Codec) (Account, error) {
	if !ValidAccountName(name) {
		return Account{}, fmt.Errorf("invalid account name %q", name)
	}
	src, err := NewWallet("aetherd", "test", srcDir, cdc)
	if err != nil {
		return Account{}, err
	}
	acc, err := src.GetAccount(name)
	if err != nil {
		return Account{}, fmt.Errorf("no account %q in %s: %w", name, srcDir, err)
	}
	addr, err := sdk.AccAddressFromBech32(acc.Address)
	if err != nil {
		return Account{}, err
	}
	files := []string{name + ".info", hex.EncodeToString(addr.Bytes()) + ".address"}

	dstKeys := filepath.Join(dstDir, testKeyringDir)
	if err := os.MkdirAll(dstKeys, 0o700); err != nil {
		return Account{}, err
	}
	for _, f := range files {
		if _, err := os.Stat(filepath.Join(dstKeys, f)); err == nil {
			return Account{}, fmt.Errorf("this wallet already has an account named %q or with address %s", name, acc.Address)
		}
	}
	var copied []string
	undo := func() {
		for _, p := range copied {
			os.Remove(p)
		}
	}
	for _, f := range files {
		bz, err := os.ReadFile(filepath.Join(srcDir, testKeyringDir, f))
		if err != nil {
			undo()
			return Account{}, err
		}
		dst := filepath.Join(dstKeys, f)
		if err := os.WriteFile(dst, bz, 0o600); err != nil {
			undo()
			return Account{}, err
		}
		copied = append(copied, dst)
	}

	dst, err := NewWallet("aetherd", "test", dstDir, cdc)
	if err == nil {
		var got Account
		got, err = dst.GetAccount(name)
		if err == nil && got.Address != acc.Address {
			err = errors.New("copied key doesn't open to the same address")
		}
		if err == nil {
			return got, nil
		}
	}
	undo()
	return Account{}, fmt.Errorf("copying %q: %w", name, err)
}
