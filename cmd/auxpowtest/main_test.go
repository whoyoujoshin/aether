package main

import (
	"bytes"
	"testing"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/whoyoujoshin/aether/x/pow"
)

// The tool computes the template hash on its own, so a proof it builds is
// an independent check of x/pow. The two must still agree.
func TestTemplateHashMatchesChain(t *testing.T) {
	reward := sdk.AccAddress("pool_payout_address_")
	blockHash := bytes.Repeat([]byte{0x5c}, 32)
	got := templateHash("aether-testnet-1", 161_024, blockHash, reward)
	want := pow.AuxPoWTemplateHash("aether-testnet-1", 161_024, blockHash, reward)
	if !bytes.Equal(got, want) {
		t.Fatalf("auxpowtest template hash %x, x/pow %x", got, want)
	}
}
