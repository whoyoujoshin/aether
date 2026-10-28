package accountauth

import "testing"

// TestRotation_OldKeyCannotSpendAndEscrowFollows encodes the property
// from docs/PQ-AGENT-ROTATION.md. It fails because primary-key rotation
// is not implemented. When the override pubkey and the rotation message
// exist, replace the body with the real assertions; do not weaken them.
//
// After a successful rotation in block N:
//   - the account address is unchanged;
//   - the old pubkey cannot authorize any message in block N or later;
//   - the new pubkey can;
//   - registered authenticators are unchanged;
//   - open escrows that name the account still settle to it;
//   - the rotation and the revocation are the same transaction.
func TestRotation_OldKeyCannotSpendAndEscrowFollows(t *testing.T) {
	t.Fatal("not implemented: primary ML-DSA key rotation that keeps the account address (docs/PQ-AGENT-ROTATION.md)")
}
