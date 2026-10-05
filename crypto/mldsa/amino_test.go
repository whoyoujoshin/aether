package mldsa_test

import (
	"testing"

	"github.com/cosmos/cosmos-sdk/codec/legacy"
	cryptotypes "github.com/cosmos/cosmos-sdk/crypto/types"
	"github.com/cosmos/cosmos-sdk/x/auth/migrations/legacytx"
	"github.com/stretchr/testify/require"

	"github.com/whoyoujoshin/aether/crypto/mldsa"
)

// x/auth's ConsumeTxSizeGasDecorator does exactly this when simulating
// a tx from an account whose pubkey is already on chain.
func TestLegacyAminoEncodesPubKey(t *testing.T) {
	priv, err := mldsa.GenPrivKey()
	require.NoError(t, err)
	pub := priv.PubKey()

	bz := legacy.Cdc.MustMarshal(legacytx.StdSignature{Signature: make([]byte, 64), PubKey: pub}) //nolint:staticcheck // what x/auth encodes
	require.Greater(t, len(bz), mldsa.PubKeySize)

	pubBz, err := legacy.Cdc.Marshal(pub)
	require.NoError(t, err)
	got, err := legacy.PubKeyFromBytes(pubBz)
	require.NoError(t, err)
	require.True(t, pub.Equals(got))

	privBz, err := legacy.Cdc.Marshal(cryptotypes.PrivKey(priv))
	require.NoError(t, err)
	gotPriv, err := legacy.PrivKeyFromBytes(privBz)
	require.NoError(t, err)
	require.True(t, priv.Equals(gotPriv))
}
