package main

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sync"

	"github.com/whoyoujoshin/aether/wallet"
)

// The encrypted keyring (--keyring-backend file, what the desktop app
// runs). Its password is typed into the wallet window, sent here once
// (POST /api/keyring/unlock) and kept only in this process's memory until
// it exits: never in a file, the environment or a command line. Every
// call that needs a key answers 423 Locked until then, and the window
// asks for the password.
//
// Accounts kept by an earlier version in the unencrypted test keyring in
// the same folder are offered for moving in (POST /api/keyring/migrate),
// then the unencrypted copies can be deleted
// (POST /api/keyring/remove-unencrypted). docs/MAINNET.md §6.

var (
	passMu   sync.RWMutex
	unlocked string
)

// errLocked answers 423 (see writeError).
var errLocked = errors.New("the wallet is locked: enter its password")

func encrypted() bool { return keyringBackend == "file" }

func currentPass() string {
	passMu.RLock()
	defer passMu.RUnlock()
	return unlocked
}

// openWallet is this wallet's keyring: the encrypted one with the typed
// password, or (a service, --keyring-backend file with
// $AETHER_KEYRING_PASSPHRASE_FILE) the passphrase file, or another
// backend as before.
func openWallet() (*wallet.Wallet, error) {
	if encrypted() && os.Getenv(wallet.KeyringPassphraseFileEnv) == "" {
		pass := currentPass()
		if pass == "" {
			return nil, errLocked
		}
		return wallet.NewFileWallet("aetherd", keyringDir, pass, walletCodec())
	}
	return wallet.NewWallet("aetherd", keyringBackend, keyringDir, walletCodec())
}

// unencryptedAccounts are the keys an earlier version left in the test
// keyring in this wallet's own folder, while this one keeps them
// encrypted.
func unencryptedAccounts() ([]wallet.Account, error) {
	if !encrypted() {
		return nil, nil
	}
	if _, err := os.Stat(filepath.Join(keyringDir, "keyring-test")); err != nil {
		return nil, nil
	}
	old, err := wallet.NewWallet("aetherd", "test", keyringDir, walletCodec())
	if err != nil {
		return nil, err
	}
	return old.ListAccounts()
}

// GET /api/keyring -> {encrypted, created, locked, unencrypted: [accounts]}
func handleKeyring(w http.ResponseWriter, r *http.Request) {
	out := map[string]any{"encrypted": encrypted(), "created": false, "locked": false, "unencrypted": []accountDTO{}}
	if encrypted() {
		out["created"] = wallet.FileKeyringCreated(keyringDir)
		out["locked"] = currentPass() == "" && os.Getenv(wallet.KeyringPassphraseFileEnv) == ""
		old, err := unencryptedAccounts()
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		out["unencrypted"] = toAccountDTOs(old)
	}
	writeJSON(w, http.StatusOK, out)
}

// POST /api/keyring/unlock {"password"}: opens the encrypted keyring, or
// creates it with this password if there is none yet.
func handleUnlock(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Password string `json:"password"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if !encrypted() {
		writeError(w, http.StatusBadRequest, errors.New("this wallet's keyring isn't encrypted"))
		return
	}
	created := wallet.FileKeyringCreated(keyringDir)
	// Opening it checks the password and, for a new keyring, writes its
	// password hash, so the next unlock needs the same one.
	if _, err := wallet.NewFileWallet("aetherd", keyringDir, req.Password, walletCodec()); err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, wallet.ErrWrongPassphrase) {
			status = http.StatusUnauthorized
		}
		writeError(w, status, err)
		return
	}
	passMu.Lock()
	unlocked = req.Password
	passMu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"unlocked": true, "created": !created})
}

// POST /api/keyring/migrate: copies every key from the unencrypted test
// keyring into the encrypted one, keeping each address. The unencrypted
// copies stay until remove-unencrypted.
func handleMigrate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, errors.New("use POST"))
		return
	}
	enc, err := openWallet()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	old, err := unencryptedAccounts()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	src, err := wallet.NewWallet("aetherd", "test", keyringDir, walletCodec())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	moved := []accountDTO{}
	for _, a := range old {
		if have, err := enc.GetAccount(a.Name); err == nil {
			if have.Address != a.Address {
				writeError(w, http.StatusConflict, errors.New("the encrypted keyring already has a different account called "+a.Name+": rename one first"))
				return
			}
			continue // moved before
		}
		acc, err := wallet.CopyKey(a.Name, src, enc)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		moved = append(moved, accountDTO{Name: acc.Name, Address: acc.Address})
	}
	writeJSON(w, http.StatusOK, map[string]any{"moved": moved})
}

// POST /api/keyring/remove-unencrypted: deletes the unencrypted test
// keyring once every account in it is in the encrypted one, at the same
// address. Its recovery phrases still restore each account.
func handleRemoveUnencrypted(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, errors.New("use POST"))
		return
	}
	enc, err := openWallet()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	old, err := unencryptedAccounts()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	for _, a := range old {
		have, err := enc.GetAccount(a.Name)
		if err != nil || have.Address != a.Address {
			writeError(w, http.StatusConflict, errors.New(a.Name+" isn't in the encrypted keyring yet: move the accounts in first"))
			return
		}
	}
	if err := os.RemoveAll(filepath.Join(keyringDir, "keyring-test")); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"removed": len(old)})
}
