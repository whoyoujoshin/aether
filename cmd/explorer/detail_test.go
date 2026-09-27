package main

import (
	"math/big"
	"testing"

	abci "github.com/cometbft/cometbft/abci/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	"github.com/whoyoujoshin/aether/x/pow"
)

func ev(typ string, kv ...string) abci.Event {
	e := abci.Event{Type: typ}
	for i := 0; i+1 < len(kv); i += 2 {
		e.Attributes = append(e.Attributes, abci.EventAttribute{Key: kv[i], Value: kv[i+1]})
	}
	return e
}

func TestMainTransfer_SkipsTheFeePayment(t *testing.T) {
	events := []abci.Event{
		ev("transfer", "recipient", "feecollector", "sender", "alice", "amount", "30uaeth"),
		ev("tx", "fee", "30uaeth", "fee_payer", "alice"),
		ev("transfer", "recipient", "bob", "sender", "alice", "amount", "3200000uaeth"),
	}
	to, amount := mainTransfer(events)
	require.Equal(t, "bob", to)
	require.Equal(t, "3200000uaeth", amount)

	// No fee: the first transfer is the payment itself, even when it
	// happens to equal what a fee would be.
	to, amount = mainTransfer([]abci.Event{ev("transfer", "recipient", "bob", "sender", "alice", "amount", "30uaeth")})
	require.Equal(t, "bob", to)
	require.Equal(t, "30uaeth", amount)

	to, amount = mainTransfer(nil)
	require.Empty(t, to)
	require.Empty(t, amount)
}

// A header mined the way cmd/powminer mines it (brute-forcing the nonce
// against the same scrypt parameters) must come out valid, with its hash
// below the target shown next to it.
func TestNativeProof_RecomputesTheMinersHash(t *testing.T) {
	miner := sdk.AccAddress([]byte("miner_address_twenty"))
	n := &pow.NativeSubmission{Height: 20, Timestamp: 1790483459, PrevHash: make([]byte, 32), MerkleRoot: make([]byte, 32), Difficulty: 16}
	var p powProofDTO
	for n.Nonce = 0; n.Nonce < 1000; n.Nonce++ {
		p = powProofDTO{}
		nativeProof(&p, n, miner)
		if p.Valid {
			break
		}
	}
	require.True(t, p.Valid, "no nonce under 1000 met difficulty 16")
	require.Len(t, p.Hash, 64)
	require.Len(t, p.Target, 64)
	h, _ := new(big.Int).SetString(p.Hash, 16)
	target, _ := new(big.Int).SetString(p.Target, 16)
	require.Negative(t, h.Cmp(target))
	require.NotEmpty(t, p.Margin)

	// Change the miner and the same nonce no longer holds (with
	// overwhelming probability at this difficulty, it must not match).
	var other powProofDTO
	nativeProof(&other, n, sdk.AccAddress([]byte("someone_else_address")))
	require.NotEqual(t, p.Hash, other.Hash)
}

func TestMinerFor(t *testing.T) {
	miners := map[string]string{"AABB": "aether1registered"}
	acct, bootstrap := minerFor(miners, "AABB")
	require.Equal(t, "aether1registered", acct)
	require.False(t, bootstrap)

	// Unregistered: the address x/pow's BootstrapValidator derives.
	consensus := "494CF1118AC52FB154C164725A7FB3BE727185F8"
	acct, bootstrap = minerFor(miners, consensus)
	require.True(t, bootstrap)
	bz, _ := sdk.AccAddressFromBech32(acct)
	require.Equal(t, consensus, upperHex(bz))

	acct, _ = minerFor(miners, "not hex")
	require.Empty(t, acct)
}

func upperHex(b []byte) string {
	const digits = "0123456789ABCDEF"
	out := make([]byte, 0, len(b)*2)
	for _, c := range b {
		out = append(out, digits[c>>4], digits[c&15])
	}
	return string(out)
}

func TestEffectiveEpochLength(t *testing.T) {
	require.Equal(t, int64(1), effectiveEpochLength(0))
	require.Equal(t, int64(1440), effectiveEpochLength(1440))
}
