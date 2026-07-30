package mldsa_test

import (
	"testing"

	"github.com/cosmos/go-bip39"
	"github.com/stretchr/testify/require"

	"github.com/whoyoujoshin/aether/crypto/mldsa"
)

func testMnemonic(t *testing.T) string {
	t.Helper()
	entropy, err := bip39.NewEntropy(256)
	require.NoError(t, err)
	mnemonic, err := bip39.NewMnemonic(entropy)
	require.NoError(t, err)
	return mnemonic
}

func TestAlgo_Name(t *testing.T) {
	require.Equal(t, mldsa.AlgoName, mldsa.Algo.Name())
}

func TestAlgo_Derive_RejectsRealHDPath(t *testing.T) {
	mnemonic := testMnemonic(t)
	deriveFn := mldsa.Algo.Derive()

	_, err := deriveFn(mnemonic, "", "m/44'/118'/0'/0/0")
	require.Error(t, err, "a real HD path must be explicitly rejected, never silently ignored")
}

func TestAlgo_Derive_AcceptsEmptyPath(t *testing.T) {
	mnemonic := testMnemonic(t)
	deriveFn := mldsa.Algo.Derive()

	seed, err := deriveFn(mnemonic, "", "")
	require.NoError(t, err)
	require.Len(t, seed, mldsa.SeedSize)
}

func TestAlgo_Derive_AcceptsBareMPath(t *testing.T) {
	mnemonic := testMnemonic(t)
	deriveFn := mldsa.Algo.Derive()

	seed, err := deriveFn(mnemonic, "", "m")
	require.NoError(t, err)
	require.Len(t, seed, mldsa.SeedSize)
}

func TestAlgo_Derive_SameMnemonicProducesSameSeed(t *testing.T) {
	mnemonic := testMnemonic(t)
	deriveFn := mldsa.Algo.Derive()

	seed1, err := deriveFn(mnemonic, "", "")
	require.NoError(t, err)
	seed2, err := deriveFn(mnemonic, "", "")
	require.NoError(t, err)

	require.Equal(t, seed1, seed2, "the same mnemonic must always deterministically reproduce the same single key")
}

func TestAlgo_Derive_DifferentMnemonicsProduceDifferentSeeds(t *testing.T) {
	deriveFn := mldsa.Algo.Derive()

	seed1, err := deriveFn(testMnemonic(t), "", "")
	require.NoError(t, err)
	seed2, err := deriveFn(testMnemonic(t), "", "")
	require.NoError(t, err)

	require.NotEqual(t, seed1, seed2)
}

func TestAlgo_Derive_DifferentPassphraseProducesDifferentSeed(t *testing.T) {
	mnemonic := testMnemonic(t)
	deriveFn := mldsa.Algo.Derive()

	seed1, err := deriveFn(mnemonic, "", "")
	require.NoError(t, err)
	seed2, err := deriveFn(mnemonic, "different-passphrase", "")
	require.NoError(t, err)

	require.NotEqual(t, seed1, seed2)
}

func TestAlgo_Generate_ProducesValidWorkingKey(t *testing.T) {
	mnemonic := testMnemonic(t)
	seed, err := mldsa.Algo.Derive()(mnemonic, "", "")
	require.NoError(t, err)

	generateFn := mldsa.Algo.Generate()
	privKey := generateFn(seed)

	msg := []byte("test message")
	sig, err := privKey.Sign(msg)
	require.NoError(t, err)
	require.True(t, privKey.PubKey().VerifySignature(msg, sig), "a key generated via the algo must genuinely sign and verify correctly")
}

func TestAlgo_Generate_SameSeedProducesSameKey(t *testing.T) {
	mnemonic := testMnemonic(t)
	seed, err := mldsa.Algo.Derive()(mnemonic, "", "")
	require.NoError(t, err)

	generateFn := mldsa.Algo.Generate()
	priv1 := generateFn(seed)
	priv2 := generateFn(seed)

	require.True(t, priv1.Equals(priv2))
}

func TestAlgo_Generate_PanicsOnWrongSeedLength(t *testing.T) {
	generateFn := mldsa.Algo.Generate()
	require.Panics(t, func() {
		generateFn([]byte("too-short"))
	})
}