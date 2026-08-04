package wallet

import (
	"os"
	"errors"

	"github.com/cosmos/go-bip39"

	"github.com/cosmos/cosmos-sdk/codec"
	"github.com/cosmos/cosmos-sdk/crypto/keyring"

	"github.com/whoyoujoshin/aether/crypto/mldsa"
)

const mnemonicEntropySize = 256

// Account is a CLI/UI-agnostic view of a wallet key -- deliberately
// exposes only what a caller needs (name, address, pubkey bytes), not
// the underlying keyring record type.
type Account struct {
	Name    string
	Address string
	PubKey  []byte
}

// Wallet wraps a real SDK keyring, scoped to a specific backend and
// storage directory. All account operations go through this, keeping
// key-management concerns in one place rather than scattered across
// callers (a CLI, a future service, etc.).
type Wallet struct {
	kr  keyring.Keyring
	cdc codec.Codec
}

// NewWallet opens (or creates) a keyring at the given directory using
// the given backend ("test", "file", "os"). appName should match the
// chain's binary name ("aetherd") for consistency with existing
// keyring data.
func NewWallet(appName, backend, rootDir string, cdc codec.Codec) (*Wallet, error) {
	kr, err := keyring.New(appName, backend, rootDir, os.Stdin, cdc, func(options *keyring.Options) {
		options.SupportedAlgos = append(options.SupportedAlgos, mldsa.Algo)
	})
	if err != nil {
		return nil, err
	}
	return &Wallet{kr: kr, cdc: cdc}, nil
}

func recordToAccount(name string, record *keyring.Record) (Account, error) {
	addr, err := record.GetAddress()
	if err != nil {
		return Account{}, err
	}
	pub, err := record.GetPubKey()
	if err != nil {
		return Account{}, err
	}
	return Account{
		Name:    name,
		Address: addr.String(),
		PubKey:  pub.Bytes(),
	}, nil
}

// CreateAccount generates a new, independent ML-DSA-44 keypair (no HD
// derivation -- matches the locked design; see
// crypto/mldsa/algo.go). Returns the real BIP-39 mnemonic backing up
// this ONE key -- callers are responsible for prompting the user to
// record it securely, matching the same honest messaging used by
// `aetherd keys add`.
func (w *Wallet) CreateAccount(name string) (Account, string, error) {
	entropy, err := bip39.NewEntropy(mnemonicEntropySize)
	if err != nil {
		return Account{}, "", err
	}
	mnemonic, err := bip39.NewMnemonic(entropy)
	if err != nil {
		return Account{}, "", err
	}

	record, err := w.kr.NewAccount(name, mnemonic, "", "", mldsa.Algo)
	if err != nil {
		return Account{}, "", err
	}

	account, err := recordToAccount(name, record)
	if err != nil {
		return Account{}, "", err
	}
	return account, mnemonic, nil
}

// ImportAccount recovers the single key a mnemonic corresponds to --
// NOT hierarchical multi-account derivation. The same mnemonic always
// deterministically reproduces the same one key.
func (w *Wallet) ImportAccount(name, mnemonic string) (Account, error) {
	if !bip39.IsMnemonicValid(mnemonic) {
	return Account{}, errors.New("invalid mnemonic")
}

	record, err := w.kr.NewAccount(name, mnemonic, "", "", mldsa.Algo)
	if err != nil {
		return Account{}, err
	}
	return recordToAccount(name, record)
}

func (w *Wallet) ListAccounts() ([]Account, error) {
	records, err := w.kr.List()
	if err != nil {
		return nil, err
	}
	accounts := make([]Account, 0, len(records))
	for _, r := range records {
		acc, err := recordToAccount(r.Name, r)
		if err != nil {
			return nil, err
		}
		accounts = append(accounts, acc)
	}
	return accounts, nil
}

func (w *Wallet) GetAccount(name string) (Account, error) {
	record, err := w.kr.Key(name)
	if err != nil {
		return Account{}, err
	}
	return recordToAccount(name, record)
}

func (w *Wallet) DeleteAccount(name string) error {
	return w.kr.Delete(name)
}