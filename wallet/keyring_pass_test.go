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
