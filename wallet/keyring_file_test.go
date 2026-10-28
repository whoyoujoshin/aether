package wallet

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	"github.com/stretchr/testify/require"

	"github.com/whoyoujoshin/aether/crypto/mldsa"
)

func testCodec() codec.Codec {
	reg := codectypes.NewInterfaceRegistry()
	mldsa.RegisterInterfaces(reg)
	return codec.NewProtoCodec(reg)
}

func writePass(t *testing.T, dir, name, pass string, mode os.FileMode) string {
	t.Helper()
	p := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(p, []byte(pass), mode))
	require.NoError(t, os.Chmod(p, mode))
	return p
}

// A service key moves from the unencrypted test keyring into an encrypted
// file keyring with its address unchanged, and the service opens and
// signs with it unattended from the passphrase file.
func TestCopyKey_TestToEncryptedFile(t *testing.T) {
	dir := t.TempDir()
	src, err := NewWallet("aetherd", "test", filepath.Join(dir, "test"), testCodec())
	require.NoError(t, err)
	acc, _, err := src.CreateAccount("faucet")
	require.NoError(t, err)

	t.Setenv(KeyringPassphraseFileEnv, writePass(t, dir, "pass", "correct-horse-battery\n", 0o600))
	dst, err := NewWallet("aetherd", "file", filepath.Join(dir, "file"), testCodec())
	require.NoError(t, err)
	got, err := CopyKey("faucet", src, dst)
	require.NoError(t, err)
	require.Equal(t, acc.Address, got.Address)
	_, err = CopyKey("faucet", src, dst)
	require.ErrorContains(t, err, "already has a key")

	// Reopened as a service would: same key, same signatures' public key.
	svc, err := NewWallet("aetherd", "file", filepath.Join(dir, "file"), testCodec())
	require.NoError(t, err)
	_, pub, err := svc.SignBytes("faucet", []byte("hello"))
	require.NoError(t, err)
	_, srcPub, err := src.SignBytes("faucet", []byte("hello"))
	require.NoError(t, err)
	require.Equal(t, srcPub, pub)

	// Nothing on disk holds the seed in the clear.
	rec, err := src.kr.Key("faucet")
	require.NoError(t, err)
	seed := rec.GetLocal().PrivKey.GetCachedValue().(*mldsa.PrivKey).Key
	files, err := filepath.Glob(filepath.Join(dir, "file", "keyring-file", "*"))
	require.NoError(t, err)
	require.NotEmpty(t, files)
	for _, f := range files {
		bz, err := os.ReadFile(f)
		require.NoError(t, err)
		require.False(t, bytes.Contains(bz, seed), "%s holds the seed", f)
	}

	// The wrong passphrase opens nothing.
	t.Setenv(KeyringPassphraseFileEnv, writePass(t, dir, "bad", "wrong-passphrase", 0o600))
	bad, err := NewWallet("aetherd", "file", filepath.Join(dir, "file"), testCodec())
	require.NoError(t, err)
	_, err = bad.GetAccount("faucet")
	require.Error(t, err)
	_, _, err = bad.SignBytes("faucet", []byte("hello"))
	require.Error(t, err)
}

func TestKeyringInput(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(KeyringPassphraseFileEnv, "")
	in, err := KeyringInput("file")
	require.NoError(t, err)
	require.Equal(t, os.Stdin, in, "no file named: stdin, as before")

	t.Setenv(KeyringPassphraseFileEnv, writePass(t, dir, "p", "correct-horse-battery\n", 0o600))
	in, err = KeyringInput("test")
	require.NoError(t, err)
	require.Equal(t, os.Stdin, in, "only the file backend reads it")

	in, err = KeyringInput("file")
	require.NoError(t, err)
	buf := make([]byte, 3*len("correct-horse-battery\n")+5)
	_, err = io.ReadFull(in, buf)
	require.NoError(t, err)
	require.Equal(t, "correct-horse-battery\ncorrect-horse-battery\ncorrect-horse-battery\ncorre", string(buf))

	for _, bad := range []struct {
		pass string
		mode os.FileMode
		want string
	}{
		{"correct-horse-battery", 0o644, "readable by others"},
		{"short", 0o600, "shorter than 8"},
		{"two\nlines-here", 0o600, "more than one line"},
	} {
		t.Setenv(KeyringPassphraseFileEnv, writePass(t, dir, "bad", bad.pass, bad.mode))
		_, err := KeyringInput("file")
		require.ErrorContains(t, err, bad.want)
	}
	t.Setenv(KeyringPassphraseFileEnv, filepath.Join(dir, "missing"))
	_, err = KeyringInput("file")
	require.Error(t, err)
}
