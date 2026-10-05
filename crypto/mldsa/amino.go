package mldsa

import (
	"github.com/cosmos/cosmos-sdk/codec"
	"github.com/cosmos/cosmos-sdk/codec/legacy"
)

// RegisterLegacyAminoCodec registers PubKey and PrivKey with an amino
// codec under PubKeyName and PrivKeyName.
//
// Nothing in consensus encodes keys with amino, but the SDK still does
// in a few places outside it. The one that matters: when simulating a
// tx (`--gas auto`), x/auth's ConsumeTxSizeGasDecorator amino-encodes
// each signer's on-chain pubkey to estimate signature size, and panics
// with "Cannot encode unregistered concrete type mldsa.PubKey" if the
// type isn't registered.
func RegisterLegacyAminoCodec(cdc *codec.LegacyAmino) {
	cdc.RegisterConcrete(&PubKey{}, PubKeyName, nil)
	cdc.RegisterConcrete(&PrivKey{}, PrivKeyName, nil)
}

// The SDK's global amino codec is the one ConsumeTxSizeGasDecorator
// uses; nothing passes it in, so register on it as soon as this package
// is linked.
func init() {
	RegisterLegacyAminoCodec(legacy.Cdc)
}
