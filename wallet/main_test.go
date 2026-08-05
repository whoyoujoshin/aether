package wallet_test

import (
	"os"
	"testing"

	"github.com/whoyoujoshin/aether/app"
)

// TestMain ensures Aether's real bech32 prefix is configured before
// any test in this package runs. Each Go test binary is its own
// separate process with its own independent copy of the SDK's global
// bech32 config -- setting this in the real aetherd binary has no
// effect here at all, confirmed the hard way when these tests failed
// against a live devnet already using the correct "aether" prefix.
func TestMain(m *testing.M) {
	app.SetAddressPrefixes()
	os.Exit(m.Run())
}