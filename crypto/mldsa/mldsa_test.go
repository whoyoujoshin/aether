package mldsa_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/whoyoujoshin/aether/crypto/mldsa"
)

func TestGenPrivKey_ProducesValidKey(t *testing.T) {
	priv, err := mldsa.GenPrivKey()
	require.NoError(t, err)
	require.Len(t, priv.Bytes(), mldsa.SeedSize)
}

func TestGenPrivKey_ProducesDifferentKeysEachTime(t *testing.T) {
	priv1, err := mldsa.GenPrivKey()
	require.NoError(t, err)
	priv2, err := mldsa.GenPrivKey()
	require.NoError(t, err)

	require.False(t, priv1.Equals(priv2))
}

func TestPrivKey_PubKey_DerivesConsistently(t *testing.T) {
	priv, err := mldsa.GenPrivKey()
	require.NoError(t, err)

	pub1 := priv.PubKey()
	pub2 := priv.PubKey()

	require.True(t, pub1.Equals(pub2), "deriving the public key twice from the same private key must produce identical results")
}

func TestSignAndVerify_ValidSignatureVerifies(t *testing.T) {
	priv, err := mldsa.GenPrivKey()
	require.NoError(t, err)
	pub := priv.PubKey()

	msg := []byte("test message for aether")
	sig, err := priv.Sign(msg)
	require.NoError(t, err)
	require.Len(t, sig, mldsa.SignatureSize)

	require.True(t, pub.VerifySignature(msg, sig))
}

func TestSignAndVerify_TamperedMessageFailsVerification(t *testing.T) {
	priv, err := mldsa.GenPrivKey()
	require.NoError(t, err)
	pub := priv.PubKey()

	msg := []byte("test message for aether")
	sig, err := priv.Sign(msg)
	require.NoError(t, err)

	tamperedMsg := []byte("test message for AETHER")
	require.False(t, pub.VerifySignature(tamperedMsg, sig))
}

func TestSignAndVerify_WrongPublicKeyFailsVerification(t *testing.T) {
	priv1, err := mldsa.GenPrivKey()
	require.NoError(t, err)
	priv2, err := mldsa.GenPrivKey()
	require.NoError(t, err)

	msg := []byte("test message for aether")
	sig, err := priv1.Sign(msg)
	require.NoError(t, err)

	wrongPub := priv2.PubKey()
	require.False(t, wrongPub.VerifySignature(msg, sig))
}

func TestVerifySignature_WrongLengthSignatureRejected(t *testing.T) {
	priv, err := mldsa.GenPrivKey()
	require.NoError(t, err)
	pub := priv.PubKey()

	msg := []byte("test message")
	require.False(t, pub.VerifySignature(msg, []byte("too-short")))
}

func TestPubKey_Address_IsDeterministicAndCorrectLength(t *testing.T) {
	priv, err := mldsa.GenPrivKey()
	require.NoError(t, err)
	pub := priv.PubKey()

	addr1 := pub.Address()
	addr2 := pub.Address()

	require.Equal(t, addr1, addr2)
	require.Len(t, addr1, 20) // tmhash.SumTruncated produces 20 bytes
}

func TestPubKey_Type_ReturnsExpectedIdentifier(t *testing.T) {
	priv, err := mldsa.GenPrivKey()
	require.NoError(t, err)
	pub := priv.PubKey()

	require.Equal(t, "mldsa44", pub.Type())
	require.Equal(t, "mldsa44", priv.Type())
}

func TestPrivKey_Sign_RejectsUninitializedKey(t *testing.T) {
	empty := &mldsa.PrivKey{}
	_, err := empty.Sign([]byte("test"))
	require.Error(t, err)
}