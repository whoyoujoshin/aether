package relayer

import (
	"strings"
	"testing"

	"github.com/cosmos/cosmos-sdk/crypto/hd"
	"github.com/cosmos/cosmos-sdk/crypto/keyring"
	"github.com/cosmos/cosmos-sdk/types/tx/signing"
	"github.com/stretchr/testify/require"

	"github.com/whoyoujoshin/aether/counterparty"
	"github.com/whoyoujoshin/aether/crypto/ethsecp256k1"
)

// Both key types the relayers sign with come over to an encrypted keyring
// at the same address, and the copy signs.
func TestCopyCounterpartyKey(t *testing.T) {
	cdc := counterparty.MakeEncodingConfig("inj").Codec
	from, err := OpenCounterpartyKeyring(keyring.BackendTest, t.TempDir(), nil, cdc)
	require.NoError(t, err)
	_, _, err = from.NewMnemonic("osmo", keyring.English, hd.CreateHDPath(118, 0, 0).String(), "", hd.Secp256k1)
	require.NoError(t, err)
	_, _, err = from.NewMnemonic("inj", keyring.English, hd.CreateHDPath(60, 0, 0).String(), "", ethsecp256k1.Algo)
	require.NoError(t, err)

	dir := t.TempDir()
	pass := strings.NewReader(strings.Repeat("relayer-passphrase\n", 20))
	to, err := OpenCounterpartyKeyring(keyring.BackendFile, dir, pass, cdc)
	require.NoError(t, err)
	for _, name := range []string{"osmo", "inj"} {
		addr, err := CopyCounterpartyKey(name, from, to)
		require.NoError(t, err, name)
		src, _ := from.Key(name)
		want, _ := src.GetAddress()
		require.Equal(t, want, addr, name)

		msg := []byte("relay")
		sig, pub, err := to.Sign(name, msg, signing.SignMode_SIGN_MODE_DIRECT)
		require.NoError(t, err, name)
		require.True(t, pub.VerifySignature(msg, sig), name)
		srcPub, _ := src.GetPubKey()
		require.True(t, srcPub.Equals(pub), name)
	}
	_, err = CopyCounterpartyKey("osmo", from, to)
	require.ErrorContains(t, err, "already has a key")

	// Reopened with the passphrase, as outbound does.
	again, err := OpenCounterpartyKeyring(keyring.BackendFile, dir, strings.NewReader(strings.Repeat("relayer-passphrase\n", 5)), cdc)
	require.NoError(t, err)
	recs, err := again.List()
	require.NoError(t, err)
	require.Len(t, recs, 2)
}
