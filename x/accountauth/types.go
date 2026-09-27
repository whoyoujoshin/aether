// Package accountauth implements pluggable account abstraction: an
// account may register alternate ML-DSA-44 "authenticators" -- a
// session key (its own keypair, expiring, capped to specific message
// types and a lifetime uaeth spend) or a guardian threshold (M of N
// ML-DSA-44 guardians jointly authorizing an action, for social
// recovery) -- that can act on its behalf without ever handling the
// account's own primary key.
//
// This is deliberately NOT a change to core ante-handler logic or to
// PostQuantumDecorator. It follows the exact same shape x/authz's own
// MsgExec already uses: a real, separately-signed message
// (MsgExecAuthenticated) that this module's own Msg handler verifies
// against the target account's registered authenticator, then
// dispatches the inner messages via the app's MsgServiceRouter as that
// account. See msg_server.go's ExecAuthenticated for exactly why that
// keeps every existing signature-verification path untouched.
package accountauth

const (
	ModuleName = "accountauth"
	StoreKey   = ModuleName
)

var (
	// KeyNextID/1 (one counter per account) -> next Authenticator.Id
	// this account will be assigned.
	KeyNextIDPrefix = []byte("next_id/")
	// KeyAuthenticatorPrefix/<account bytes>/<id big-endian> -> a
	// marshaled Authenticator.
	KeyAuthenticatorPrefix = []byte("auth/")
)

func nextIDKey(account []byte) []byte {
	return append(append([]byte{}, KeyNextIDPrefix...), account...)
}

func authenticatorKey(account []byte, id uint64) []byte {
	key := append(append([]byte{}, KeyAuthenticatorPrefix...), account...)
	return append(key, idBytes(id)...)
}

func authenticatorPrefix(account []byte) []byte {
	return append(append([]byte{}, KeyAuthenticatorPrefix...), account...)
}

func idBytes(id uint64) []byte {
	b := make([]byte, 8)
	for i := 7; i >= 0; i-- {
		b[i] = byte(id)
		id >>= 8
	}
	return b
}

// GenesisState is empty at genesis by design: every authenticator is
// registered by a real signed MsgRegisterAuthenticator after the chain
// is already running, the same way x/authz grants aren't part of any
// chain's genesis either. There is deliberately no field here yet --
// Authenticator's oneof doesn't round-trip through plain
// encoding/json (unlike x/pow's own hand-marshaled genesis, nothing
// here needs protobuf-aware JSON today), and a real chain-halt/export
// need for existing authenticators is exactly the kind of thing to add
// a real, tested genesis format for when it's first needed rather than
// speculatively now.
type GenesisState struct{}

func DefaultGenesisState() GenesisState {
	return GenesisState{}
}

func (g GenesisState) Validate() error {
	return nil
}
