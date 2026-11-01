package wallet

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	dkeyring "github.com/99designs/keyring"
	"github.com/cosmos/cosmos-sdk/codec"
	"github.com/cosmos/cosmos-sdk/crypto/keyring"
	"golang.org/x/crypto/bcrypt"
)

// NewKeyring is keyring.New, except that a "file" keyring whose input came
// from KeyringPassphraseFileEnv (see KeyringInput) is opened with that
// passphrase directly. The SDK's own file prompt (crypto/keyring
// newRealPrompt -> client/input.GetPassword) ignores the reader whenever
// stdin is a console and prompts there instead; under a Windows scheduled
// task stdin is a hidden console, so the service hung on "Enter keyring
// passphrase". This path never looks at stdin.
func NewKeyring(appName, backend, rootDir string, in io.Reader, cdc codec.Codec, opts ...keyring.Option) (keyring.Keyring, error) {
	rr, ok := in.(*repeatReader)
	if backend != keyring.BackendFile || !ok {
		return keyring.New(appName, backend, rootDir, in, cdc, opts...)
	}
	pass := string(rr.line[:len(rr.line)-1])
	fileDir := filepath.Join(rootDir, "keyring-file") // the SDK's keyringFileDirName
	db, err := dkeyring.Open(dkeyring.Config{
		AllowedBackends:  []dkeyring.BackendType{dkeyring.FileBackend},
		ServiceName:      appName,
		FileDir:          fileDir,
		FilePasswordFunc: filePassword(fileDir, pass),
	})
	if err != nil {
		return nil, err
	}
	return keyring.NewInMemoryWithKeyring(db, cdc, opts...), nil
}

// filePassword checks pass against the keyring's keyhash as the SDK's
// prompt does (or writes the keyhash for a new keyring, as it does).
func filePassword(fileDir, pass string) dkeyring.PromptFunc {
	return func(string) (string, error) {
		path := filepath.Join(fileDir, "keyhash")
		hash, err := os.ReadFile(path)
		switch {
		case err == nil:
			if bcrypt.CompareHashAndPassword(hash, []byte(pass)) != nil {
				return "", fmt.Errorf("%s: incorrect passphrase", KeyringPassphraseFileEnv)
			}
			return pass, nil
		case errors.Is(err, os.ErrNotExist):
			hash, err := bcrypt.GenerateFromPassword([]byte(pass), 2)
			if err != nil {
				return "", err
			}
			if err := os.MkdirAll(fileDir, 0o700); err != nil {
				return "", err
			}
			if err := os.WriteFile(path, hash, 0o600); err != nil {
				return "", err
			}
			return pass, nil
		default:
			return "", err
		}
	}
}
