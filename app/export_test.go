package app

// SetAuthzFeegrantActivationHeight lets external tests (package
// app_test, which can import wallet without an import cycle) run a
// chain with x/authz and x/feegrant live from genesis.
func SetAuthzFeegrantActivationHeight(h int64) (restore func()) {
	orig := authzFeegrantActivationHeight
	authzFeegrantActivationHeight = h
	return func() { authzFeegrantActivationHeight = orig }
}
