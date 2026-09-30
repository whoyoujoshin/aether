package pow

import storetypes "cosmossdk.io/store/types"

// BuildValidAuxPowForTest lets the external test package build real,
// internally consistent AuxPoW proofs for handler tests.
var BuildValidAuxPowForTest = buildValidAuxPow

// StoreKeyForTest lets the external test package check exactly what a
// handler wrote.
func (k Keeper) StoreKeyForTest() storetypes.StoreKey { return k.storeKey }
