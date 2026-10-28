package wallet_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/whoyoujoshin/aether/crypto/mldsa"
	"github.com/whoyoujoshin/aether/wallet"
	"github.com/whoyoujoshin/aether/x/accountauth"
)

// A key rotated onto another account sends from that account, across a
// reopen of the keyring, until the record is removed.
func TestSignFor(t *testing.T) {
	cdc := testCodec()
	dir := t.TempDir()
	w, err := wallet.NewWallet("aetherd", "test", dir, cdc)
	require.NoError(t, err)
	old, _, err := w.CreateAccount("old")
	require.NoError(t, err)
	next, _, err := w.CreateAccount("next")
	require.NoError(t, err)
	require.Equal(t, next.Address, next.KeyAddress)

	require.Error(t, w.SignFor("next", "not-an-address"))
	require.Error(t, w.SignFor("missing", old.Address))
	require.NoError(t, w.SignFor("next", old.Address))

	st, err := os.Stat(filepath.Join(dir, wallet.SignsForFile))
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), st.Mode().Perm())

	w, err = wallet.NewWallet("aetherd", "test", dir, cdc)
	require.NoError(t, err)
	got, err := w.GetAccount("next")
	require.NoError(t, err)
	require.Equal(t, old.Address, got.Address, "sends from the account")
	require.Equal(t, next.KeyAddress, got.KeyAddress)
	require.Equal(t, next.PubKey, got.PubKey)
	list, err := w.ListAccounts()
	require.NoError(t, err)
	for _, a := range list {
		if a.Name == "next" {
			require.Equal(t, old.Address, a.Address)
		}
	}

	// It goes along with a copy of the key.
	copied, err := wallet.CopyTestKey(dir, t.TempDir(), "next", cdc)
	require.NoError(t, err)
	require.Equal(t, old.Address, copied.Address)

	// Its own address removes the record, as does deleting the key.
	require.NoError(t, w.SignFor("next", next.Address))
	got, err = w.GetAccount("next")
	require.NoError(t, err)
	require.Equal(t, next.Address, got.Address)
	require.NoError(t, w.SignFor("next", old.Address))
	require.NoError(t, w.DeleteAccount("next"))
	w, err = wallet.NewWallet("aetherd", "test", dir, cdc)
	require.NoError(t, err)
	_, _, err = w.CreateAccount("next")
	require.NoError(t, err)
	got, err = w.GetAccount("next")
	require.NoError(t, err)
	require.Equal(t, got.KeyAddress, got.Address, "a new key by that name signs for itself")
}

func TestSignFor_BadFileRefused(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, wallet.SignsForFile), []byte(`{"next":"nope"}`), 0o600))
	_, err := wallet.NewWallet("aetherd", "test", dir, testCodec())
	require.ErrorContains(t, err, wallet.SignsForFile)
}

// The messages carry the new key's proof over what x/accountauth checks,
// and guardian signatures verify under their keys.
func TestRotationMessages(t *testing.T) {
	w := newTestWallet(t)
	old, _, err := w.CreateAccount("old")
	require.NoError(t, err)
	next, _, err := w.CreateAccount("next")
	require.NoError(t, err)
	guardian, _, err := w.CreateAccount("guardian")
	require.NoError(t, err)
	const chainID = "aether-rc-wallet"

	m, err := w.RotateKeyMsg(chainID, old.Address, "next", 3)
	require.NoError(t, err)
	require.Equal(t, next.PubKey, m.NewPubkey)
	pub := &mldsa.PubKey{Key: next.PubKey}
	require.True(t, pub.VerifySignature(accountauth.RotateKeySigningBytes(chainID, old.Address, 3, next.PubKey), m.NewKeySignature))
	require.False(t, pub.VerifySignature(accountauth.RotateKeySigningBytes(chainID, old.Address, 4, next.PubKey), m.NewKeySignature))

	sb := accountauth.RecoverKeySigningBytes(chainID, old.Address, 7, next.PubKey)
	gs, err := w.GuardianSign("guardian", sb)
	require.NoError(t, err)
	require.Equal(t, guardian.PubKey, gs.Pubkey)
	require.True(t, (&mldsa.PubKey{Key: gs.Pubkey}).VerifySignature(sb, gs.Signature))

	r, err := w.RecoverKeyMsg(chainID, guardian.Address, old.Address, "next", 7, []*accountauth.GuardianSignature{gs})
	require.NoError(t, err)
	require.Equal(t, guardian.Address, r.Submitter)
	require.True(t, pub.VerifySignature(accountauth.RotateKeySigningBytes(chainID, old.Address, 7, next.PubKey), r.NewKeySignature))

	_, err = w.RotateKeyMsg(chainID, old.Address, "missing", 0)
	require.Error(t, err)
}
