package pow

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"
)

// Fuzz targets for the AuxPoW parser: everything in an AuxPoW submission
// comes from outside the chain (a parent chain's header and coinbase, as
// a pool hands them over), so no input may panic. Run one for longer
// with, for example:
//
//	go test ./x/pow -run '^$' -fuzz '^FuzzCheckAuxPow$' -fuzztime 5m
//
// Plain `go test` runs only the seeds.

// FuzzCheckAuxPow decodes the input as an AuxPowData message, as the
// chain does a transaction's, and checks it where AuxPoWInputGuardHeight
// applies: a fresh chain from block 1, and the testnet from that height.
func FuzzCheckAuxPow(f *testing.F) {
	valid := buildValidAuxPow(&testing.T{}, bytes.Repeat([]byte{0x42}, 32), 4, MergedMiningActivationHeight)
	seed, err := valid.Marshal()
	if err != nil {
		f.Fatal(err)
	}
	f.Add(seed)
	f.Add([]byte{})
	noBranches := *valid
	noBranches.CoinbaseBranch, noBranches.ChainBranch = nil, nil
	if b, err := noBranches.Marshal(); err == nil {
		f.Add(b)
	}

	f.Fuzz(func(t *testing.T, raw []byte) {
		var d AuxPowData
		if err := d.Unmarshal(raw); err != nil {
			return
		}
		_ = CheckAuxPowOn("aether-rc-fuzz", &d, 4, 1)
		_ = CheckAuxPowOn("", &d, 4, AuxPoWInputGuardHeight)
	})
}

// FuzzAuxPowCommitment feeds raw bytes to the coinbase parser that both
// CheckAuxPow and the pool bridge (cmd/auxpowd) use.
func FuzzAuxPowCommitment(f *testing.F) {
	f.Add(buildTestCoinbaseTx(buildTestScriptSig([]byte("pool"), bytes.Repeat([]byte{0x42}, 32), 1, 0)))
	f.Add([]byte{})
	// A scriptSig length of 2^64-1, which int() turns negative.
	f.Add(append(append([]byte{1, 0, 0, 0, 1}, make([]byte, 36)...), 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff))
	f.Fuzz(func(t *testing.T, tx []byte) {
		_, _, _ = AuxPowCommitment(tx)
	})
}

// TestCheckAuxPow_MalformedInputIsRefusedFromTheGuard covers what
// FuzzCheckAuxPow found: each input below is refused with an error where
// AuxPoWInputGuardHeight applies, and below it on the testnet still
// panics as it always did, so the testnet's history replays unchanged.
func TestCheckAuxPow_MalformedInputIsRefusedFromTheGuard(t *testing.T) {
	valid := buildValidAuxPow(t, bytes.Repeat([]byte{0x42}, 32), 4, MergedMiningActivationHeight)
	overflow := append(append([]byte{1, 0, 0, 0, 1}, make([]byte, 36)...), 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff)

	cases := map[string]struct {
		mutate    func(d *AuxPowData)
		wantErr   string
		panicsPre bool
	}{
		"no coinbase branch": {func(d *AuxPowData) { d.CoinbaseBranch = nil }, "coinbase_branch is missing", true},
		"no chain branch":    {func(d *AuxPowData) { d.ChainBranch = nil }, "chain_branch is missing", true},
		"scriptSig length overflows int": {func(d *AuxPowData) {
			d.CoinbaseTx = overflow
			d.ParentHeader = buildTestParentHeader(1, 0, 0, 0, make([]byte, 32), doubleSHA256(overflow))
		}, "scriptSig length extends beyond", true},
		"33-level coinbase branch": {func(d *AuxPowData) {
			d.CoinbaseBranch = &MerkleBranch{Hashes: make([][]byte, 33)}
		}, "coinbase merkle branch too long", false},
		"short branch hash": {func(d *AuxPowData) {
			d.CoinbaseBranch = &MerkleBranch{Hashes: [][]byte{{0x01}}}
		}, "is 1 bytes, not 32", false},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			d := *valid
			c.mutate(&d)
			for _, at := range []struct {
				chainID string
				height  int64
			}{{"aether-rc-test", 1}, {"aether-1", MergedMiningActivationHeight}, {"", AuxPoWInputGuardHeight}} {
				err := CheckAuxPowOn(at.chainID, &d, 4, at.height)
				require.ErrorContains(t, err, c.wantErr, "%s at %d", at.chainID, at.height)
			}
			pre := func() { _ = CheckAuxPowOn("", &d, 4, AuxPoWInputGuardHeight-1) }
			if c.panicsPre {
				require.Panics(t, pre, "below the guard the testnet keeps its old behaviour")
			} else {
				require.NotPanics(t, pre)
				require.Error(t, CheckAuxPowOn("", &d, 4, AuxPoWInputGuardHeight-1))
			}
		})
	}

	// A genuine proof passes either side of the guard.
	require.NoError(t, CheckAuxPowOn("", valid, 4, AuxPoWInputGuardHeight))
	require.NoError(t, CheckAuxPowOn("aether-rc-test", valid, 4, 1))
}
