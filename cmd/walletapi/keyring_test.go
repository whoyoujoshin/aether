package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/whoyoujoshin/aether/wallet"
)

func call(t *testing.T, h http.HandlerFunc, method, path, body string) (int, map[string]any) {
	t.Helper()
	rec := httptest.NewRecorder()
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, path, nil)
	} else {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
	}
	h(rec, req)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func encryptedWallet(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	origDir, origBackend := keyringDir, keyringBackend
	keyringDir, keyringBackend = dir, "file"
	t.Setenv(wallet.KeyringPassphraseFileEnv, "")
	t.Cleanup(func() {
		keyringDir, keyringBackend = origDir, origBackend
		passMu.Lock()
		unlocked = ""
		passMu.Unlock()
	})
	return dir
}

// The desktop app's keyring is encrypted: locked until its password is
// given, created with the first one, and refused with any other.
func TestKeyring_LockedUntilUnlocked(t *testing.T) {
	encryptedWallet(t)

	code, out := call(t, handleKeyring, "GET", "/api/keyring", "")
	require.Equal(t, http.StatusOK, code)
	require.Equal(t, true, out["encrypted"])
	require.Equal(t, false, out["created"])
	require.Equal(t, true, out["locked"])

	code, out = call(t, handleCreateAccount, "POST", "/api/accounts/create", `{"name":"main"}`)
	require.Equal(t, http.StatusLocked, code, "no key use while locked")
	require.Equal(t, true, out["locked"])

	code, _ = call(t, handleUnlock, "POST", "/api/keyring/unlock", `{"password":"short"}`)
	require.Equal(t, http.StatusBadRequest, code)
	code, out = call(t, handleUnlock, "POST", "/api/keyring/unlock", `{"password":"correct horse"}`)
	require.Equal(t, http.StatusOK, code)
	require.Equal(t, true, out["created"])

	code, out = call(t, handleCreateAccount, "POST", "/api/accounts/create", `{"name":"main"}`)
	require.Equal(t, http.StatusOK, code)
	addr := out["address"]

	// A restart forgets the password: the same one opens it again, another doesn't.
	passMu.Lock()
	unlocked = ""
	passMu.Unlock()
	_, out = call(t, handleKeyring, "GET", "/api/keyring", "")
	require.Equal(t, true, out["created"])
	require.Equal(t, true, out["locked"])
	code, _ = call(t, handleUnlock, "POST", "/api/keyring/unlock", `{"password":"wrong horse"}`)
	require.Equal(t, http.StatusUnauthorized, code)
	_, out = call(t, handleKeyring, "GET", "/api/keyring", "")
	require.Equal(t, true, out["locked"], "a wrong password unlocks nothing")
	code, out = call(t, handleUnlock, "POST", "/api/keyring/unlock", `{"password":"correct horse"}`)
	require.Equal(t, http.StatusOK, code)
	require.Equal(t, false, out["created"])
	w, err := openWallet()
	require.NoError(t, err)
	acc, err := w.GetAccount("main")
	require.NoError(t, err)
	require.Equal(t, addr, acc.Address)
}

// Accounts an earlier version kept unencrypted move into the encrypted
// keyring at the same address, and only then can the unencrypted copies
// go.
func TestKeyring_MovesUnencryptedAccountsIn(t *testing.T) {
	dir := encryptedWallet(t)
	old, err := wallet.NewWallet("aetherd", "test", dir, walletCodec())
	require.NoError(t, err)
	a, _, err := old.CreateAccount("savings")
	require.NoError(t, err)
	b, _, err := old.CreateAccount("spending")
	require.NoError(t, err)

	_, out := call(t, handleKeyring, "GET", "/api/keyring", "")
	require.Len(t, out["unencrypted"], 2)

	code, _ := call(t, handleMigrate, "POST", "/api/keyring/migrate", "")
	require.Equal(t, http.StatusLocked, code)
	code, _ = call(t, handleUnlock, "POST", "/api/keyring/unlock", `{"password":"correct horse"}`)
	require.Equal(t, http.StatusOK, code)

	code, _ = call(t, handleRemoveUnencrypted, "POST", "/api/keyring/remove-unencrypted", "")
	require.Equal(t, http.StatusConflict, code, "never deleted before they are moved")
	_, err = os.Stat(filepath.Join(dir, "keyring-test"))
	require.NoError(t, err)

	code, out = call(t, handleMigrate, "POST", "/api/keyring/migrate", "")
	require.Equal(t, http.StatusOK, code)
	require.Len(t, out["moved"], 2)
	code, out = call(t, handleMigrate, "POST", "/api/keyring/migrate", "")
	require.Equal(t, http.StatusOK, code)
	require.Len(t, out["moved"], 0, "moving again changes nothing")

	w, err := openWallet()
	require.NoError(t, err)
	for _, want := range []wallet.Account{a, b} {
		got, err := w.GetAccount(want.Name)
		require.NoError(t, err)
		require.Equal(t, want.Address, got.Address)
	}

	code, out = call(t, handleRemoveUnencrypted, "POST", "/api/keyring/remove-unencrypted", "")
	require.Equal(t, http.StatusOK, code)
	require.EqualValues(t, 2, out["removed"])
	_, err = os.Stat(filepath.Join(dir, "keyring-test"))
	require.True(t, os.IsNotExist(err))
	_, out = call(t, handleKeyring, "GET", "/api/keyring", "")
	require.Len(t, out["unencrypted"], 0)
	code, out = call(t, handleAccounts, "GET", "/api/accounts", "")
	require.Equal(t, http.StatusOK, code, "%v", out)
}

// A name clash is refused, not overwritten.
func TestKeyring_MigrateRefusesANameClash(t *testing.T) {
	dir := encryptedWallet(t)
	old, err := wallet.NewWallet("aetherd", "test", dir, walletCodec())
	require.NoError(t, err)
	_, _, err = old.CreateAccount("main")
	require.NoError(t, err)
	code, _ := call(t, handleUnlock, "POST", "/api/keyring/unlock", `{"password":"correct horse"}`)
	require.Equal(t, http.StatusOK, code)
	code, _ = call(t, handleCreateAccount, "POST", "/api/accounts/create", `{"name":"main"}`)
	require.Equal(t, http.StatusOK, code)

	code, out := call(t, handleMigrate, "POST", "/api/keyring/migrate", "")
	require.Equal(t, http.StatusConflict, code)
	require.Contains(t, out["error"], "different account called main")
}

// Lock now, lock after a while unused, and a new password.
func TestKeyring_LockAutoLockAndNewPassword(t *testing.T) {
	encryptedWallet(t)
	origAfter := lockAfter
	t.Cleanup(func() { lockAfter = origAfter })

	code, _ := call(t, handleUnlock, "POST", "/api/keyring/unlock", `{"password":"correct horse"}`)
	require.Equal(t, http.StatusOK, code)
	code, out := call(t, handleCreateAccount, "POST", "/api/accounts/create", `{"name":"main"}`)
	require.Equal(t, http.StatusOK, code)
	addr := out["address"]

	// Lock now.
	code, _ = call(t, handleLock, "POST", "/api/keyring/lock", "")
	require.Equal(t, http.StatusOK, code)
	code, _ = call(t, handleAccounts, "GET", "/api/accounts", "")
	require.Equal(t, http.StatusLocked, code)

	// Locks itself after the set time unused.
	call(t, handleUnlock, "POST", "/api/keyring/unlock", `{"password":"correct horse"}`)
	code, out = call(t, handleLockAfter, "POST", "/api/keyring/lock-after", `{"minutes":5}`)
	require.Equal(t, http.StatusOK, code)
	_, out = call(t, handleKeyring, "GET", "/api/keyring", "")
	require.EqualValues(t, 5, out["lockAfterMinutes"])
	passMu.Lock()
	lastUse = time.Now().Add(-6 * time.Minute)
	passMu.Unlock()
	_, out = call(t, handleKeyring, "GET", "/api/keyring", "")
	require.Equal(t, true, out["locked"], "six minutes unused with a five-minute lock")
	code, _ = call(t, handleLockAfter, "POST", "/api/keyring/lock-after", `{"minutes":-1}`)
	require.Equal(t, http.StatusBadRequest, code)
	code, _ = call(t, handleLockAfter, "POST", "/api/keyring/lock-after", `{"minutes":0}`)
	require.Equal(t, http.StatusOK, code)
	call(t, handleUnlock, "POST", "/api/keyring/unlock", `{"password":"correct horse"}`)
	passMu.Lock()
	lastUse = time.Now().Add(-48 * time.Hour)
	passMu.Unlock()
	_, out = call(t, handleKeyring, "GET", "/api/keyring", "")
	require.Equal(t, false, out["locked"], "0 never locks")

	// A new password: the wrong current one is refused; the right one
	// re-encrypts, and only the new one unlocks after.
	code, _ = call(t, handleChangePassword, "POST", "/api/keyring/change-password", `{"current":"wrong horse","new":"battery staple"}`)
	require.Equal(t, http.StatusUnauthorized, code)
	code, _ = call(t, handleChangePassword, "POST", "/api/keyring/change-password", `{"current":"correct horse","new":"short"}`)
	require.Equal(t, http.StatusBadRequest, code)
	code, out = call(t, handleChangePassword, "POST", "/api/keyring/change-password", `{"current":"correct horse","new":"battery staple"}`)
	require.Equal(t, http.StatusOK, code, "%v", out)
	code, out = call(t, handleAccounts, "GET", "/api/accounts", "")
	require.Equal(t, http.StatusOK, code, "still unlocked, with the new password")
	call(t, handleLock, "POST", "/api/keyring/lock", "")
	code, _ = call(t, handleUnlock, "POST", "/api/keyring/unlock", `{"password":"correct horse"}`)
	require.Equal(t, http.StatusUnauthorized, code)
	code, _ = call(t, handleUnlock, "POST", "/api/keyring/unlock", `{"password":"battery staple"}`)
	require.Equal(t, http.StatusOK, code)
	w, err := openWallet()
	require.NoError(t, err)
	acc, err := w.GetAccount("main")
	require.NoError(t, err)
	require.Equal(t, addr, acc.Address)
}
