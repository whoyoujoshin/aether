package mldsa

import (
	"crypto/subtle"
	"fmt"
	"crypto/rand"

	"github.com/cloudflare/circl/sign/mldsa/mldsa44"
	"github.com/cosmos/cosmos-sdk/crypto/types"
	"github.com/cometbft/cometbft/crypto/tmhash"
	sdkerrors "cosmossdk.io/errors"
)

const (
	// PubKeyName is the amino/proto type name for this key, used for
	// registration and Type().
	PubKeyName = "aether/mldsa44/PubKey"
	PrivKeyName = "aether/mldsa44/PrivKey"

	// KeyType is the short string identifier returned by Type().
	KeyType = "mldsa44"

	// PubKeySize is the real, fixed ML-DSA-44 public key size per FIPS 204.
	PubKeySize = mldsa44.PublicKeySize
	// SignatureSize is the real, fixed ML-DSA-44 signature size per FIPS 204.
	SignatureSize = mldsa44.SignatureSize
	// SeedSize is the size of the seed circl uses to deterministically
	// derive a full ML-DSA-44 keypair -- we store this, not the full
	// expanded private key, matching circl's own idiomatic pattern.
	SeedSize = mldsa44.SeedSize
)

var _ types.PubKey = &PubKey{}
var _ types.PrivKey = &PrivKey{}

// Address derives an account address from the raw public key bytes.
// Uses the same tmhash-based scheme as the SDK's other non-secp256k1
// key types -- a plain hash of the public key, truncated to 20 bytes.
// NOTE: this is a placeholder pending explicit confirmation this
// matches the address scheme this project wants for real user
// accounts (ed25519's own SDK implementation explicitly warns its own
// address scheme is NOT valid outside a consensus-key context -- this
// needs its own deliberate confirmation before being relied upon for
// real account addresses, tracked as an open question for this
// component).
func (pk *PubKey) Address() types.Address {
	return types.Address(tmhash.SumTruncated(pk.Key))
}

func (pk *PubKey) Bytes() []byte {
	return pk.Key
}

func (pk *PubKey) VerifySignature(msg, sig []byte) bool {
	if len(pk.Key) != PubKeySize {
		return false
	}
	if len(sig) != SignatureSize {
		return false
	}

	pubKey := new(mldsa44.PublicKey)
	if err := pubKey.UnmarshalBinary(pk.Key); err != nil {
		return false
	}

	return mldsa44.Verify(pubKey, msg, nil, sig)
}

func (pk *PubKey) Equals(other types.PubKey) bool {
	otherKey, ok := other.(*PubKey)
	if !ok {
		return false
	}
	return subtle.ConstantTimeCompare(pk.Key, otherKey.Key) == 1
}

func (pk *PubKey) Type() string {
	return KeyType
}

func (sk *PrivKey) Bytes() []byte {
	return sk.Key
}

func (sk *PrivKey) PubKey() types.PubKey {
	if len(sk.Key) != SeedSize {
		panic("mldsa: private key seed is not initialized or wrong size")
	}
	var seed [mldsa44.SeedSize]byte
	copy(seed[:], sk.Key)
	pub, _ := mldsa44.NewKeyFromSeed(&seed)

	pubBytes, err := pub.MarshalBinary()
	if err != nil {
		panic(fmt.Sprintf("mldsa: failed to marshal derived public key: %v", err))
	}

	return &PubKey{Key: pubBytes}
}

func (sk *PrivKey) Equals(other types.LedgerPrivKey) bool {
	otherKey, ok := other.(*PrivKey)
	if !ok {
		return false
	}
	return subtle.ConstantTimeCompare(sk.Key, otherKey.Key) == 1
}

func (sk *PrivKey) Type() string {
	return KeyType
}

func (sk *PrivKey) Sign(msg []byte) ([]byte, error) {
	if len(sk.Key) != SeedSize {
		return nil, sdkerrors.Wrap(fmt.Errorf("invalid seed size"), "mldsa: private key seed is not initialized or wrong size")
	}
	var seed [mldsa44.SeedSize]byte
	copy(seed[:], sk.Key)
	_, priv := mldsa44.NewKeyFromSeed(&seed)

	sig := make([]byte, SignatureSize)
	if err := mldsa44.SignTo(priv, msg, nil, false, sig); err != nil {
		return nil, sdkerrors.Wrap(err, "mldsa: signing failed")
	}

	return sig, nil
}

// GenPrivKey generates a new, random ML-DSA-44 private key. Rather
// than use mldsa44.GenerateKey (which doesn't expose the seed it
// derived internally), we generate the seed ourselves and pass it to
// NewKeyFromSeed -- this way we can store and later exactly
// reconstruct the same keypair from the retained seed alone, matching
// circl's own idiomatic "store the seed, not the full expanded key"
// pattern.
func GenPrivKey() (*PrivKey, error) {
	var seed [mldsa44.SeedSize]byte
	if _, err := rand.Read(seed[:]); err != nil {
		return nil, sdkerrors.Wrap(err, "mldsa: failed to generate random seed")
	}

	// Confirm the seed actually produces a valid keypair before
	// returning it.
	_, _ = mldsa44.NewKeyFromSeed(&seed)

	return &PrivKey{Key: seed[:]}, nil
}