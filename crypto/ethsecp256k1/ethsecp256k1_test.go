package ethsecp256k1_test

import (
	"encoding/hex"
	"strings"
	"testing"

	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	"github.com/cosmos/cosmos-sdk/crypto/hd"
	"github.com/cosmos/cosmos-sdk/crypto/keyring"
	"github.com/cosmos/cosmos-sdk/types/tx/signing"
	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/decred/dcrd/dcrec/secp256k1/v4/ecdsa"
	"github.com/stretchr/testify/require"

	"github.com/whoyoujoshin/aether/crypto/ethsecp256k1"
)

// The well-known test mnemonic and its first Ethereum account
// (m/44'/60'/0'/0/0), which every Ethereum wallet derives the same way.
const (
	mnemonic   = "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about"
	ethPath    = "m/44'/60'/0'/0/0"
	ethPrivHex = "1ab42cc412b618bdea3a599e3c9bae199ebf030895b039e9db1e30dafb12b727"
	ethAddress = "9858effd232b4033e47d90003d41ec34ecaeda94" // 0x9858EfFD232B4033E47d90003D41EC34EcaEda94
)

func TestKeccak256(t *testing.T) {
	// Ethereum's empty-input Keccak-256 (not SHA3-256's a7ffc6f8...).
	require.Equal(t, "c5d2460186f7233c927e7db2dcc703c0e500b653ca82273b7bfad8045d85a470", hex.EncodeToString(ethsecp256k1.Keccak256(nil)))
}

func TestAddressMatchesEthereum(t *testing.T) {
	priv, _ := hex.DecodeString(ethPrivHex)
	pk := (&ethsecp256k1.PrivKey{Key: priv}).PubKey()
	require.Len(t, pk.Bytes(), ethsecp256k1.PubKeySize)
	require.Equal(t, ethAddress, hex.EncodeToString(pk.Address()))
}

func TestTypeURLsAreInjectives(t *testing.T) {
	priv, _ := hex.DecodeString(ethPrivHex)
	sk := &ethsecp256k1.PrivKey{Key: priv}
	pkAny, err := codectypes.NewAnyWithValue(sk.PubKey())
	require.NoError(t, err)
	require.Equal(t, "/injective.crypto.v1beta1.ethsecp256k1.PubKey", pkAny.TypeUrl)
	skAny, err := codectypes.NewAnyWithValue(sk)
	require.NoError(t, err)
	require.Equal(t, "/injective.crypto.v1beta1.ethsecp256k1.PrivKey", skAny.TypeUrl)
}

func TestSignVerify(t *testing.T) {
	sk, err := ethsecp256k1.GenPrivKey()
	require.NoError(t, err)
	pk := sk.PubKey()
	msg := []byte("sign bytes of an Injective transaction")

	sig, err := sk.Sign(msg)
	require.NoError(t, err)
	require.Len(t, sig, ethsecp256k1.SignatureSize)
	require.LessOrEqual(t, sig[64], byte(1), "V is the bare recovery id, as go-ethereum's crypto.Sign returns")
	require.True(t, pk.VerifySignature(msg, sig))
	require.True(t, pk.VerifySignature(msg, sig[:64]), "R || S without V also verifies")

	// It's a signature over Keccak-256(msg), and V recovers the key.
	compact := append([]byte{27 + sig[64]}, sig[:64]...)
	rec, _, err := ecdsa.RecoverCompact(compact, ethsecp256k1.Keccak256(msg))
	require.NoError(t, err)
	require.Equal(t, pk.Bytes(), rec.SerializeCompressed())

	require.False(t, pk.VerifySignature([]byte("another message"), sig))
	bad := append([]byte(nil), sig...)
	bad[10] ^= 1
	require.False(t, pk.VerifySignature(msg, bad))

	// The high-S twin of a valid signature is refused (malleability).
	var s secp256k1.ModNScalar
	s.SetByteSlice(sig[32:64])
	s.Negate()
	high := append([]byte(nil), sig[:32]...)
	sb := s.Bytes()
	high = append(high, sb[:]...)
	require.False(t, pk.VerifySignature(msg, high))
}

// A keyring built with Algo derives the Ethereum account from the
// mnemonic, signs with it, and stores the key under Injective's type URL.
func TestKeyring(t *testing.T) {
	registry := codectypes.NewInterfaceRegistry()
	ethsecp256k1.RegisterInterfaces(registry)
	cdc := codec.NewProtoCodec(registry)
	kr, err := keyring.New("injectived", keyring.BackendTest, t.TempDir(), nil, cdc, func(o *keyring.Options) {
		o.SupportedAlgos = keyring.SigningAlgoList{hd.Secp256k1, ethsecp256k1.Algo}
	})
	require.NoError(t, err)

	rec, err := kr.NewAccount("relayer", mnemonic, "", ethPath, ethsecp256k1.Algo)
	require.NoError(t, err)
	addr, err := rec.GetAddress()
	require.NoError(t, err)
	require.Equal(t, ethAddress, hex.EncodeToString(addr))
	require.True(t, strings.HasSuffix(rec.PubKey.TypeUrl, "ethsecp256k1.PubKey"))

	msg := []byte("tx sign bytes")
	sig, pub, err := kr.Sign("relayer", msg, signing.SignMode_SIGN_MODE_DIRECT)
	require.NoError(t, err)
	require.IsType(t, &ethsecp256k1.PubKey{}, pub)
	require.True(t, pub.VerifySignature(msg, sig))
}
