package app

import (
	"fmt"

	sdk "github.com/cosmos/cosmos-sdk/types"
)

// PostQuantumDecorator is a placeholder ante handler decorator for post-quantum
// signature schemes (NIST Dilithium or Falcon). From genesis we intend to require
// PQ signatures for all txs to achieve quantum resistance.
//
// Current status: STUB ONLY. Always returns nil (accepts classical signatures).
// TODO (Quantum Sentinel):
//   - Integrate github.com/cloudflare/circl or similar for Dilithium3/Falcon-512
//   - Replace default SigVerificationGasConsumer
//   - Add hybrid mode (classical + PQ) during transition if needed
//   - Update account pubkeys to support PQ key types
type PostQuantumDecorator struct{}

func NewPostQuantumDecorator() PostQuantumDecorator {
	return PostQuantumDecorator{}
}

func (pqd PostQuantumDecorator) AnteHandle(ctx sdk.Context, tx sdk.Tx, simulate bool, next sdk.AnteHandler) (newCtx sdk.Context, err error) {
	// Placeholder: In production this will verify Dilithium/Falcon signatures
	// on every signer. For now we log and pass through so classical txs work
	// during devnet.
	if !simulate {
		// Future: inspect signatures via tx.GetSignaturesV2() or similar
		// and enforce PQ pubkeys once the real crypto is wired.
		_ = fmt.Sprintf("PQ stub: accepting tx (quantum-ready path pending)")
	}

	return next(ctx, tx, simulate)
}

// IsPostQuantumReady returns whether the chain is enforcing PQ signatures.
// Controlled by governance / params later.
func IsPostQuantumReady() bool {
	return false // flip to true after real crypto integration
}
