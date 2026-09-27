# Account abstraction (x/accountauth)

Pluggable, native account abstraction -- no CosmWasm, no changes to the
ante handler or `PostQuantumDecorator` -- built, wired into `app/app.go`,
and covered by real unit and integration tests
(`x/accountauth/msg_server_test.go`, `app/accountauth_test.go`) -- not
yet live on any deployed chain. See `app/accountauth.go`'s
`AccountAuthActivationHeight` doc comment for why: like `x/authz`/
`x/feegrant` and IBC before it, adding a new store to an already-running
chain needs a coordinated, height-gated cutover, not a plain code
deploy. The constant is currently a placeholder (`300_000`); activating
it for real means picking a real height near the live chain's tip, the
same way `AuthzFeegrantActivationHeight` was finalized at 109,000, and is
not done by this doc.

## What this is

An account can register alternate ML-DSA-44 **authenticators** --
additional ways to authorize actions on its behalf, without ever handing
out its own primary key:

- **Session key**: a secondary ML-DSA-44 keypair, scoped to specific
  message types, capped to a total lifetime `uaeth` spend, and expiring.
  Useful for an agent acting on an owner's account: the owner registers
  the agent's own key as a session key instead of granting it broad
  authority.
- **Guardian threshold**: M of N registered ML-DSA-44 guardians can
  jointly authorize an action -- social recovery, or any multi-party
  spending policy -- without any guardian needing a funded account of
  their own. They only ever sign; anyone may submit the resulting
  transaction.

Both are registered by the account's own primary key
(`MsgRegisterAuthenticator`), a completely ordinary signed message --
registering never needs a new ante-handler path. Using one
(`MsgExecAuthenticated`) is modeled directly on `x/authz`'s own
`MsgExec`/`DispatchActions`: it unpacks the inner messages, checks
authorization *in the handler*, then dispatches through the app's real
`baseapp.MessageRouter` -- so a session key's or a guardian threshold's
exec looks, to every other module, exactly like the account's own
ordinary transaction.

## How authorization is checked

- **Session key**: the outer transaction's own signer -- the session
  key itself -- already went through the normal
  `PostQuantumDecorator` + signature-verification path proving whoever
  submitted it controls that key. The handler additionally checks:
  not expired, every inner message's type is on the key's allow-list,
  and (for `MsgSend`) the cumulative lifetime spend stays under the
  key's `spend_limit_uaeth` (tracked in `spent_uaeth`, persisted after
  every accepted exec).
- **Guardian threshold**: the outer transaction's signer can be
  *anyone* (a relayer with no stake in the outcome) -- authorization
  instead comes from `guardian_signatures` attached to the message,
  each checked against a domain-separated, sequence-bound digest
  (`accountauth.GuardianExecSigningBytes`: covers the chain ID, account,
  authenticator ID, a replay-protection sequence, and every inner
  message's type URL and raw bytes). At least `threshold` valid,
  distinct guardian signatures are required; accepting an exec advances
  `next_sequence`, so the exact same signatures can never be replayed.

Every inner message is also checked to be signed (its own signer field)
as the authorizing account -- an authenticator can only ever act *as*
the account that registered it, never redirect a message to someone
else's.

## CLI

```
aetherd tx accountauth register-session-key [pubkey-hex] [expires-at-unix] [spend-limit-uaeth] [allowed-msg-type...] --from <owner>
aetherd tx accountauth register-guardian-threshold [threshold] [guardian-pubkey-hex...] --from <owner>
aetherd tx accountauth revoke [id] --from <owner>
aetherd tx accountauth exec-authenticated [account] [authenticator-id] [msgs-json-file] --from <signer> [--guardian-sig pubkey-hex:signature-hex ...]
aetherd tx accountauth sign-guardian-exec [account] [authenticator-id] [msgs-json-file] --from <guardian-key>
aetherd query accountauth authenticators [account]
```

`msgs-json-file` is a JSON array of messages in proto JSON form
(including each one's own `"@type"`), the same convention
`x/governance`'s `submit-param-change-proposal` uses for a single
message. `sign-guardian-exec` queries the authenticator's current
on-chain `next_sequence` (never a caller-supplied one, so a stale local
copy can't produce a signature that silently fails once the real exec
lands), signs the resulting digest with a local keyring key, and prints
`pubkey-hex:signature-hex` ready to paste into `--guardian-sig`.

## walletapi / agentmcp

`cmd/walletapi` exposes the same operations over HTTP
(`/api/accountauth`, `/api/accountauth/session-key`,
`/api/accountauth/guardian-threshold`, `/api/accountauth/revoke`,
`/api/accountauth/exec`) via `wallet.RegisterSessionKeyMsg` /
`RegisterGuardianThresholdMsg` / `RevokeAuthenticatorMsg` /
`ExecAuthenticatedSendMsg` (`wallet/accountauth.go`), mirroring
`grants.go`'s existing shape for `x/authz`.

`cmd/agentmcp` exposes one read-only tool, `get_account_authenticators`,
so an agent (or its operator) can inspect what's registered for any
address, including its own. This server doesn't act as a session key or
guardian itself -- that would be a third spending mode alongside its
existing hot-wallet/grant modes, deliberately out of scope for this
milestone (its `send_aeth`/grant-mode plumbing already covers the "agent
spends under a chain-enforced cap" use case via `x/authz` + `x/feegrant`;
a session key is a second, independent way to reach the same outcome,
not one this server needs to also implement to be useful).

## Activating this for real

1. Agree a real `AccountAuthActivationHeight` with the operator based on
   the live chain's tip (same process as the authz/feegrant and IBC
   cutovers).
2. Every node must run a binary with this height before it arrives, and
   restart once at `activation-1` to mount the new store -- see
   `app/accountauth.go` and `app/authz_feegrant.go`'s own doc comments
   for the mechanics.
