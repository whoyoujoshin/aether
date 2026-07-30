package mldsa

import (
	"crypto/sha256"
	"fmt"

	"github.com/cosmos/go-bip39"

	"github.com/cosmos/cosmos-sdk/crypto/hd"
	"github.com/cosmos/cosmos-sdk/crypto/types"
)

// AlgoName is the keyring-facing identifier for this key type.
const AlgoName = hd.PubKeyType("mldsa44")

// Algo is the keyring SignatureAlgo for ML-DSA-44. Locked design:
// each account is an INDEPENDENT keypair, not derived from a shared
// HD path -- ML-DSA's lattice structure makes genuine BIP-32-style
// derivation cryptographically undefined (public key rounding breaks
// the parent-plus-offset property BIP-32 depends on; see
// pq-signatures-decision.md and the real, current academic research
// confirming this is still an open, unaudited problem even in the
// newest published constructions). A BIP-39 mnemonic can still back up
// ONE key, it just never derives multiple accounts from that mnemonic.
type mldsaAlgo struct{}

var Algo = mldsaAlgo{}

func (m mldsaAlgo) Name() hd.PubKeyType {
	return AlgoName
}

// Derive intentionally rejects any real HD path rather than silently
// ignoring it -- a user must never be misled into thinking
// "m/44'/.../1" and "m/44'/.../2" would produce two different,
// independently-recoverable accounts from the same mnemonic. Only an
// empty (or bare "m") path is accepted, producing the chain's single
// independent key deterministically from the mnemonic + passphrase.
func (m mldsaAlgo) Derive() hd.DeriveFn {
	return func(mnemonic string, bip39Passphrase, hdPath string) ([]byte, error) {
		if hdPath != "" && hdPath != "m" {
			return nil, fmt.Errorf(
				"mldsa44 does not support hierarchical derivation paths -- each account is an independent keypair, not part of a derivation tree (rejected hdPath %q)",
				hdPath,
			)
		}

		seed, err := bip39.NewSeedWithErrorChecking(mnemonic, bip39Passphrase)
		if err != nil {
			return nil, err
		}

		// BIP-39 seeds are 64 bytes; ML-DSA-44 needs a 32-byte seed.
		// Reduced deterministically via SHA-256 so the same mnemonic
		// always reproduces the exact same single key -- this is a
		// mnemonic-backed key, not a master seed for a key tree.
		reduced := sha256.Sum256(seed)
		return reduced[:], nil
	}
}

func (m mldsaAlgo) Generate() hd.GenerateFn {
	return func(bz []byte) types.PrivKey {
		if len(bz) != SeedSize {
			panic(fmt.Sprintf("mldsa44: invalid seed length for key generation: got %d, want %d", len(bz), SeedSize))
		}
		key := make([]byte, SeedSize)
		copy(key, bz)
		return &PrivKey{Key: key}
	}
}