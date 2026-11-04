package wallet

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cosmos/cosmos-sdk/codec"
	"github.com/cosmos/cosmos-sdk/crypto/keyring"
	"golang.org/x/crypto/bcrypt"
)

// An encrypted ("file") keyring opened from a passphrase a person types,
// as the desktop wallet does (cmd/walletapi): the passphrase lives only in
// the process's memory, never in a file or on a command line. Services
// opening one unattended use KeyringPassphraseFileEnv instead.

// MinPassphraseLength is the shortest passphrase a new file keyring takes.
const MinPassphraseLength = 8

// ErrWrongPassphrase is the passphrase not opening an existing keyring.
var ErrWrongPassphrase = errors.New("wrong password")

// fileKeyhash is where the SDK keeps a file keyring's bcrypt passphrase
// hash (keyring-file/keyhash), written when the keyring is created.
func fileKeyhash(rootDir string) string {
	return filepath.Join(rootDir, "keyring-file", "keyhash")
}

// FileKeyringCreated reports whether rootDir already holds a file keyring,
// so a passphrase given opens it rather than creates it.
func FileKeyringCreated(rootDir string) bool {
	_, err := os.Stat(fileKeyhash(rootDir))
	return err == nil
}

// CheckPassphrase says whether pass opens rootDir's file keyring: nil if
// it does, ErrWrongPassphrase if not. Where there is no keyring yet it
// checks only that pass could create one.
func CheckPassphrase(rootDir, pass string) error {
	if err := validPassphrase(pass); err != nil {
		return err
	}
	hash, err := os.ReadFile(fileKeyhash(rootDir))
	switch {
	case errors.Is(err, os.ErrNotExist):
		return nil
	case err != nil:
		return err
	}
	if bcrypt.CompareHashAndPassword(hash, []byte(pass)) != nil {
		return ErrWrongPassphrase
	}
	return nil
}

func validPassphrase(pass string) error {
	if len(pass) < MinPassphraseLength {
		return fmt.Errorf("the password must be at least %d characters", MinPassphraseLength)
	}
	if strings.ContainsAny(pass, "\r\n") {
		return errors.New("the password can't contain a line break")
	}
	return nil
}

// NewFileWallet opens rootDir's encrypted file keyring with pass, creating
// it (with pass) if there is none yet.
func NewFileWallet(appName, rootDir, pass string, cdc codec.Codec) (*Wallet, error) {
	if err := CheckPassphrase(rootDir, pass); err != nil {
		return nil, err
	}
	return newWalletFrom(appName, keyring.BackendFile, rootDir, &repeatReader{line: []byte(pass + "\n")}, cdc)
}

// ChangeFilePassphrase re-encrypts rootDir's file keyring under newPass:
// every key is copied into a new keyring with newPass, each copy checked
// against its address, and only then is the new keyring put in the old
// one's place. If anything fails before the swap, the old keyring is left
// as it was.
func ChangeFilePassphrase(appName, rootDir, oldPass, newPass string, cdc codec.Codec) error {
	if err := validPassphrase(newPass); err != nil {
		return err
	}
	if !FileKeyringCreated(rootDir) {
		return errors.New("there is no encrypted keyring to change the password of")
	}
	src, err := NewFileWallet(appName, rootDir, oldPass, cdc)
	if err != nil {
		return err
	}
	accounts, err := src.ListAccounts()
	if err != nil {
		return err
	}
	tmpRoot, err := os.MkdirTemp(rootDir, ".rekey-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpRoot)
	dst, err := NewFileWallet(appName, tmpRoot, newPass, cdc)
	if err != nil {
		return err
	}
	for _, a := range accounts {
		copied, err := CopyKey(a.Name, src, dst)
		if err != nil {
			return fmt.Errorf("copying %s: %w", a.Name, err)
		}
		if copied.Address != a.Address {
			return fmt.Errorf("copying %s: got address %s, want %s", a.Name, copied.Address, a.Address)
		}
	}
	// Swap: the old keyring aside, the new one in, the old one gone.
	live := filepath.Join(rootDir, "keyring-file")
	aside := live + ".old"
	if err := os.RemoveAll(aside); err != nil {
		return err
	}
	if err := os.Rename(live, aside); err != nil {
		return err
	}
	if err := os.Rename(filepath.Join(tmpRoot, "keyring-file"), live); err != nil {
		if rerr := os.Rename(aside, live); rerr != nil {
			return fmt.Errorf("%v; and putting the old keyring back failed too (it is at %s): %v", err, aside, rerr)
		}
		return err
	}
	return os.RemoveAll(aside)
}
