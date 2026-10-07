// Package ethsecp256k1 is Injective's account key type, so Aether's
// relayers can sign transactions on Injective.
//
// Injective accounts use Ethereum-style keys: secp256k1, but signatures
// are over the Keccak-256 hash of the sign bytes (not SHA-256), and the
// address is the last 20 bytes of the Keccak-256 hash of the uncompressed
// public key, as on Ethereum. The proto types (keys.pb.go) carry
// Injective's own type URLs, /injective.crypto.v1beta1.ethsecp256k1.PubKey
// and .PrivKey, so a transaction's signer info is what Injective expects,
// and a keyring written by injectived (eth_secp256k1, coin type 60) reads
// here unchanged.
//
// Aether itself never accepts these keys: PostQuantumDecorator admits
// only ML-DSA-44. They are only for signing on Injective.
package ethsecp256k1

import (
	"bytes"
	"crypto/subtle"
	"fmt"

	"github.com/cosmos/cosmos-sdk/codec"
	"github.com/cosmos/cosmos-sdk/codec/types"
	"github.com/cosmos/cosmos-sdk/crypto/hd"
	cryptotypes "github.com/cosmos/cosmos-sdk/crypto/types"
	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/decred/dcrd/dcrec/secp256k1/v4/ecdsa"
	"golang.org/x/crypto/sha3"
)

const (
	// KeyType is the key type name, and the keyring algorithm name
	// injectived uses for these keys.
	KeyType = "eth_secp256k1"
	// PrivKeySize is a secp256k1 private key's size in bytes.
	PrivKeySize = 32
	// PubKeySize is a compressed secp256k1 public key's size in bytes.
	PubKeySize = 33
	// SignatureSize is an Ethereum signature: R || S || V, V in {0, 1}.
	SignatureSize = 65
)

var (
	_ cryptotypes.PubKey  = &PubKey{}
	_ cryptotypes.PrivKey = &PrivKey{}
)

// Keccak256 is Ethereum's hash (the original Keccak padding, not
// NIST SHA3-256).
func Keccak256(data ...[]byte) []byte {
	h := sha3.NewLegacyKeccak256()
	for _, d := range data {
		h.Write(d)
	}
	return h.Sum(nil)
}

// GenPrivKey is a new random key.
func GenPrivKey() (*PrivKey, error) {
	k, err := secp256k1.GeneratePrivateKey()
	if err != nil {
		return nil, err
	}
	return &PrivKey{Key: k.Serialize()}, nil
}

func (sk *PrivKey) Bytes() []byte { return sk.Key }

func (sk *PrivKey) Type() string { return KeyType }

func (sk *PrivKey) Equals(other cryptotypes.LedgerPrivKey) bool {
	o, ok := other.(*PrivKey)
	return ok && subtle.ConstantTimeCompare(sk.Key, o.Key) == 1
}

// PubKey is the compressed public key.
func (sk *PrivKey) PubKey() cryptotypes.PubKey {
	return &PubKey{Key: secp256k1.PrivKeyFromBytes(sk.Key).PubKey().SerializeCompressed()}
}

// Sign signs the Keccak-256 hash of msg, returning R || S || V with a
// low S, as go-ethereum's crypto.Sign does and Injective verifies.
func (sk *PrivKey) Sign(msg []byte) ([]byte, error) {
	if len(sk.Key) != PrivKeySize {
		return nil, fmt.Errorf("eth_secp256k1: private key is %d bytes, want %d", len(sk.Key), PrivKeySize)
	}
	compact := ecdsa.SignCompact(secp256k1.PrivKeyFromBytes(sk.Key), Keccak256(msg), false)
	// compact is V || R || S with V = 27 + recovery id.
	sig := make([]byte, SignatureSize)
	copy(sig, compact[1:])
	sig[64] = compact[0] - 27
	return sig, nil
}

// Address is the Ethereum address: the last 20 bytes of the Keccak-256
// hash of the uncompressed key without its 0x04 prefix. Injective's
// inj1... address is the same 20 bytes in bech32.
func (pk *PubKey) Address() cryptotypes.Address {
	p, err := secp256k1.ParsePubKey(pk.Key)
	if err != nil {
		return nil
	}
	return cryptotypes.Address(Keccak256(p.SerializeUncompressed()[1:])[12:])
}

func (pk *PubKey) Bytes() []byte { return pk.Key }

func (pk *PubKey) Type() string { return KeyType }

func (pk *PubKey) Equals(other cryptotypes.PubKey) bool {
	o, ok := other.(*PubKey)
	return ok && bytes.Equal(pk.Key, o.Key)
}

// VerifySignature checks an R || S || V (or bare R || S) signature over
// the Keccak-256 hash of msg, refusing a high S as go-ethereum does.
func (pk *PubKey) VerifySignature(msg, sig []byte) bool {
	if len(sig) == SignatureSize {
		sig = sig[:64]
	}
	if len(sig) != 64 {
		return false
	}
	p, err := secp256k1.ParsePubKey(pk.Key)
	if err != nil {
		return false
	}
	var r, s secp256k1.ModNScalar
	if r.SetByteSlice(sig[:32]) || s.SetByteSlice(sig[32:]) || r.IsZero() || s.IsZero() || s.IsOverHalfOrder() {
		return false
	}
	return ecdsa.NewSignature(&r, &s).Verify(Keccak256(msg), p)
}

// RegisterInterfaces registers PubKey and PrivKey under Injective's type
// URLs, for decoding signer infos and keyring records.
func RegisterInterfaces(registry types.InterfaceRegistry) {
	registry.RegisterImplementations((*cryptotypes.PubKey)(nil), &PubKey{})
	registry.RegisterImplementations((*cryptotypes.PrivKey)(nil), &PrivKey{})
}

// Algo is the keyring algorithm for these keys: BIP-44 derivation like
// secp256k1's (use coin type 60, m/44'/60'/0'/0/0, for Injective), with
// the result taken as an eth_secp256k1 key.
var Algo = ethAlgo{}

type ethAlgo struct{}

func (ethAlgo) Name() hd.PubKeyType { return hd.PubKeyType(KeyType) }

func (ethAlgo) Derive() hd.DeriveFn { return hd.Secp256k1.Derive() }

func (ethAlgo) Generate() hd.GenerateFn {
	return func(bz []byte) cryptotypes.PrivKey {
		key := make([]byte, PrivKeySize)
		copy(key, bz)
		return &PrivKey{Key: key}
	}
}

// Amino names, as Injective registers them.
const (
	PubKeyName  = "injective/PubKeyEthSecp256k1"
	PrivKeyName = "injective/PrivKeyEthSecp256k1"
)

// RegisterLegacyAminoCodec registers PubKey and PrivKey with an amino
// codec. Only a chain that accepts these keys needs it (x/auth amino-
// encodes a signer's pubkey to estimate gas when simulating); Injective
// does this itself, and the local counterparty devnet calls it to stand
// in for Injective.
func RegisterLegacyAminoCodec(cdc *codec.LegacyAmino) {
	cdc.RegisterConcrete(&PubKey{}, PubKeyName, nil)
	cdc.RegisterConcrete(&PrivKey{}, PrivKeyName, nil)
}
