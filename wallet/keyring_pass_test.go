package wallet

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// A file keyring opened from a typed passphrase, as the desktop wallet
// does: created with the first one, reopened only with it, its keys
// encrypted on disk.
func TestNewFileWallet_CreatesReopensAndRefusesTheWrongPassword(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(KeyringPassphraseFileEnv, "") // a typed passphrase, not a service's file

	require.False(t, FileKeyringCreated(dir))
	require.ErrorContains(t, CheckPassphrase(dir, "short"), "at least 8")
	require.Error(t, CheckPassphrase(dir, "two\nlines!!"))
	require.NoError(t, CheckPassphrase(dir, "correct horse"), "no keyring yet: any good passphrase may create it")

	w, err := NewFileWallet("aetherd", dir, "correct horse", testCodec())
	require.NoError(t, err)
	acc, mnemonic, err := w.CreateAccount("main")
	require.NoError(t, err)
	require.True(t, FileKeyringCreated(dir))

	require.ErrorIs(t, CheckPassphrase(dir, "wrong horse"), ErrWrongPassphrase)
	_, err = NewFileWallet("aetherd", dir, "wrong horse", testCodec())
	require.ErrorIs(t, err, ErrWrongPassphrase)

	again, err := NewFileWallet("aetherd", dir, "correct horse", testCodec())
	require.NoError(t, err)
	got, err := again.GetAccount("main")
	require.NoError(t, err)
	require.Equal(t, acc.Address, got.Address)

	// Nothing on disk holds the key or its phrase in the clear.
	require.NoError(t, filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		bz, err := os.ReadFile(path)
		require.NoError(t, err)
		require.NotContains(t, string(bz), mnemonic, path)
		require.NotContains(t, string(bz), "correct horse", path)
		return nil
	}))
	_, err = os.Stat(filepath.Join(dir, "keyring-test"))
	require.True(t, os.IsNotExist(err))
}

// A key from an earlier unencrypted (test) keyring moves into the
// encrypted one with its address.
func TestNewFileWallet_TakesKeysFromATestKeyring(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(KeyringPassphraseFileEnv, "")
	old, err := NewWallet("aetherd", "test", dir, testCodec())
	require.NoError(t, err)
	acc, _, err := old.CreateAccount("main")
	require.NoError(t, err)

	enc, err := NewFileWallet("aetherd", dir, "correct horse", testCodec())
	require.NoError(t, err)
	copied, err := CopyKey("main", old, enc)
	require.NoError(t, err)
	require.Equal(t, acc.Address, copied.Address)
	sig, pub, err := enc.SignBytes("main", []byte("hello"))
	require.NoError(t, err)
	require.NotEmpty(t, sig)
	require.NotEmpty(t, pub)
}

// A new password re-encrypts every key: the old password no longer opens
// it, the new one does, every address is kept, and the keys still sign.
func TestChangeFilePassphrase(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(KeyringPassphraseFileEnv, "")
	w, err := NewFileWallet("aetherd", dir, "correct horse", testCodec())
	require.NoError(t, err)
	a, _, err := w.CreateAccount("savings")
	require.NoError(t, err)
	b, _, err := w.CreateAccount("spending")
	require.NoError(t, err)

	require.ErrorIs(t, ChangeFilePassphrase("aetherd", dir, "wrong horse", "battery staple", testCodec()), ErrWrongPassphrase)
	require.ErrorContains(t, ChangeFilePassphrase("aetherd", dir, "correct horse", "short", testCodec()), "at least 8")
	require.NoError(t, CheckPassphrase(dir, "correct horse"), "a refused change leaves the old password")

	require.NoError(t, ChangeFilePassphrase("aetherd", dir, "correct horse", "battery staple", testCodec()))
	require.ErrorIs(t, CheckPassphrase(dir, "correct horse"), ErrWrongPassphrase)
	again, err := NewFileWallet("aetherd", dir, "battery staple", testCodec())
	require.NoError(t, err)
	for _, want := range []Account{a, b} {
		got, err := again.GetAccount(want.Name)
		require.NoError(t, err)
		require.Equal(t, want.Address, got.Address)
	}
	_, _, err = again.SignBytes("savings", []byte("hello"))
	require.NoError(t, err)

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	for _, e := range entries {
		require.NotContains(t, e.Name(), ".rekey-", "no copy left behind")
		require.NotEqual(t, "keyring-file.old", e.Name(), "no copy left behind")
	}
}
