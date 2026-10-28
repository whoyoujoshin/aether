package wallet

import (
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"

	"github.com/cosmos/cosmos-sdk/crypto/keyring"

	"github.com/whoyoujoshin/aether/crypto/mldsa"
)

// KeyringPassphraseFileEnv names a file holding the passphrase of a "file"
// keyring (encrypted on disk), so a service can open it unattended
// (docs/SERVICE-KEYS.md). Under systemd the file is a credential:
// LoadCredentialEncrypted=keyring:/etc/credstore.encrypted/<name> with
// Environment=AETHER_KEYRING_PASSPHRASE_FILE=%d/keyring. It applies to the
// "file" backend only; without it the passphrase is read from stdin, as
// before.
const KeyringPassphraseFileEnv = "AETHER_KEYRING_PASSPHRASE_FILE"

// KeyringInput is the reader a keyring of backend prompts on: for "file"
// with KeyringPassphraseFileEnv set, the passphrase in that file, given
// as often as the keyring asks (it asks twice when it creates the
// keyring); otherwise stdin.
func KeyringInput(backend string) (io.Reader, error) {
	path := os.Getenv(KeyringPassphraseFileEnv)
	if backend != keyring.BackendFile || path == "" {
		return os.Stdin, nil
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", KeyringPassphraseFileEnv, err)
	}
	// Windows has no Unix permission bits; restrict the file with icacls there.
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("%s: %s is readable by others (%v); chmod 600 it", KeyringPassphraseFileEnv, path, info.Mode().Perm())
	}
	bz, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", KeyringPassphraseFileEnv, err)
	}
	pass := strings.TrimRight(string(bz), "\r\n")
	if len(pass) < 8 {
		return nil, fmt.Errorf("%s: the passphrase in %s is shorter than 8 characters", KeyringPassphraseFileEnv, path)
	}
	if strings.ContainsAny(pass, "\r\n") {
		return nil, fmt.Errorf("%s: the passphrase in %s spans more than one line", KeyringPassphraseFileEnv, path)
	}
	return &repeatReader{line: []byte(pass + "\n")}, nil
}

// repeatReader gives line over and over.
type repeatReader struct {
	line []byte
	off  int
}

func (r *repeatReader) Read(p []byte) (int, error) {
	n := 0
	for n < len(p) {
		c := copy(p[n:], r.line[r.off:])
		n += c
		r.off = (r.off + c) % len(r.line)
	}
	return n, nil
}

// CopyKey copies the named ML-DSA key from one keyring into another, e.g.
// from a "test" keyring into an encrypted "file" one, keeping its address.
// The SDK's `keys export` can't carry ML-DSA keys (its armor decodes
// private keys with the legacy amino codec, which doesn't know them), so
// the 32-byte seed the key is generated from is imported directly.
func CopyKey(name string, from, to *Wallet) (Account, error) {
	rec, err := from.kr.Key(name)
	if err != nil {
		return Account{}, err
	}
	local := rec.GetLocal()
	if local == nil || local.PrivKey == nil {
		return Account{}, fmt.Errorf("%s is not a local key (ledger, offline or multisig keys can't be copied)", name)
	}
	priv, ok := local.PrivKey.GetCachedValue().(*mldsa.PrivKey)
	if !ok {
		return Account{}, fmt.Errorf("%s is a %s key; only ML-DSA keys are copied", name, local.PrivKey.TypeUrl)
	}
	if _, err := to.kr.Key(name); err == nil {
		return Account{}, fmt.Errorf("the destination already has a key named %s", name)
	}
	if err := to.kr.ImportPrivKeyHex(name, hex.EncodeToString(priv.Key), string(mldsa.AlgoName)); err != nil {
		return Account{}, err
	}
	src, err := recordToAccount(name, rec)
	if err != nil {
		return Account{}, err
	}
	dst, err := to.GetAccount(name)
	if err != nil {
		return Account{}, err
	}
	if dst.KeyAddress != src.KeyAddress {
		_ = to.kr.Delete(name)
		return Account{}, errors.New("the copy has a different address; removed it")
	}
	// A rotated key goes on signing for its account.
	if a, ok := from.signsFor[name]; ok {
		if err := to.SignFor(name, a); err != nil {
			return Account{}, err
		}
		dst.Address = a
	}
	return dst, nil
}
