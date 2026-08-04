package wallet_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"

	"github.com/whoyoujoshin/aether/crypto/mldsa"
	"github.com/whoyoujoshin/aether/wallet"
)

func newTestWallet(t *testing.T) *wallet.Wallet {
	t.Helper()
	registry := codectypes.NewInterfaceRegistry()
	mldsa.RegisterInterfaces(registry)
	cdc := codec.NewProtoCodec(registry)

	w, err := wallet.NewWallet("aetherd-test", "memory", t.TempDir(), cdc)
	require.NoError(t, err)
	return w
}

func TestCreateAccount_ProducesRealAddressAndMnemonic(t *testing.T) {
	w := newTestWallet(t)

	account, mnemonic, err := w.CreateAccount("alice")
	require.NoError(t, err)
	require.Equal(t, "alice", account.Name)
	require.NotEmpty(t, account.Address)
	require.NotEmpty(t, account.PubKey)
	require.NotEmpty(t, mnemonic)
}

func TestCreateAccount_DifferentAccountsGetDifferentAddresses(t *testing.T) {
	w := newTestWallet(t)

	acc1, _, err := w.CreateAccount("alice")
	require.NoError(t, err)
	acc2, _, err := w.CreateAccount("bob")
	require.NoError(t, err)

	require.NotEqual(t, acc1.Address, acc2.Address)
}

func TestImportAccount_RecoversSameAddressFromSameMnemonic(t *testing.T) {
	w := newTestWallet(t)

	original, mnemonic, err := w.CreateAccount("alice")
	require.NoError(t, err)

	w2 := newTestWallet(t) // fresh, independent keyring
	recovered, err := w2.ImportAccount("alice-recovered", mnemonic)
	require.NoError(t, err)

	require.Equal(t, original.Address, recovered.Address, "recovering from the same mnemonic must reproduce the exact same address")
}

func TestImportAccount_RejectsInvalidMnemonic(t *testing.T) {
	w := newTestWallet(t)
	_, err := w.ImportAccount("bad", "this is not a real bip39 mnemonic at all")
	require.Error(t, err)
}

func TestListAccounts_ReturnsAllCreatedAccounts(t *testing.T) {
	w := newTestWallet(t)

	_, _, err := w.CreateAccount("alice")
	require.NoError(t, err)
	_, _, err = w.CreateAccount("bob")
	require.NoError(t, err)

	accounts, err := w.ListAccounts()
	require.NoError(t, err)
	require.Len(t, accounts, 2)
}

func TestGetAccount_ReturnsCorrectAccount(t *testing.T) {
	w := newTestWallet(t)
	created, _, err := w.CreateAccount("alice")
	require.NoError(t, err)

	fetched, err := w.GetAccount("alice")
	require.NoError(t, err)
	require.Equal(t, created.Address, fetched.Address)
}

func TestDeleteAccount_RemovesAccount(t *testing.T) {
	w := newTestWallet(t)
	_, _, err := w.CreateAccount("alice")
	require.NoError(t, err)

	err = w.DeleteAccount("alice")
	require.NoError(t, err)

	_, err = w.GetAccount("alice")
	require.Error(t, err, "deleted account must no longer be retrievable")
}