# PQ Agent Key Rotation — Implementation Plan

**Branch:** `grok/pq-agent-rotation`
**Status:** design complete, not built
**For:** Claude (or any implementer) picking this up cold

## The problem

An Aether account address is `hash(ML-DSA pubkey)` (ADR-028, `crypto/mldsa`).
Rotating the primary key therefore changes the address. Balances,
authenticators, open escrows, and anything keyed by address stay on the
old account. The agent is a new agent.

`x/accountauth` already provides session keys and spending-guardian
thresholds, both ML-DSA, both registered by the primary key. It does not
let the primary key itself be replaced. An owner who loses the primary
key, or who wants to retire it after a suspected leak, has no path that
keeps the account.

Aether forbids a classical fallback. The rotation has to work with
ML-DSA only.

## The property

After a successful rotation in block N:

1. The account address is unchanged.
2. The old pubkey cannot authorize any message in block N or later,
   including a message already in the mempool that lands in the same
   block.
3. The new pubkey can.
4. Registered authenticators (session keys, spending guardians) are
   unchanged.
5. Open escrows that name the account as payer or payee still settle
   to that account.
6. The rotation and the revocation of the old key are the same
   transaction. There is no block in which both keys work, and no
   block in which neither does.

## Why the ante path has to change

`PostQuantumDecorator` (`app/post_quantum.go`) is a type gate only. It
rejects any signer whose pubkey is not `*mldsa.PubKey`. It does not
verify signatures.

Real verification is the SDK's `SigVerificationDecorator`, which calls
`acc.GetPubKey()` and verifies against that. If `MsgRotateKey` sets the
account's pubkey to the new key, every later transaction verifies
against it. The old key fails, the new key works, the address is
untouched, and the sequence continues because nothing resets it.

That is not sufficient. The SDK's `SetPubKeyDecorator` runs first and
checks `pubkey.Address() == signer` *before* it checks whether the
account already has a pubkey (`x/auth/ante/sigverify.go`, around line
91). A transaction from the new key that includes its pubkey — which
the SDK includes by default — fails that check, because the new key's
address is not the account address.

So the ante path must change. The change is one decorator.

## The height-gate

### What a height-gate is

Aether activates consensus-affecting changes at a specific block height,
not at deploy time. A node running old code halts at the activation
height rather than computing a different AppHash. This is how
`x/accountauth` itself went live (block 122,000), and how IBC, escrow,
and the inflow cap went live.

The mechanism is in `gates/gates.go`:

- `gates.Height(chainID, testnetHeight)` returns the height a change
  applies from. On a fresh chain (`aether-1` or any `aether-rc-*`) it
  returns 1. On the testnet and in unit tests it returns
  `testnetHeight`.
- `gates.On(ctx, testnetHeight)` reports whether the current block is
  at or past that height.
- `gates.At(ctx, testnetHeight)` reports whether the current block is
  exactly that height (for once-only setup).

A height-gated change is a constant (e.g. `RotationActivationHeight`)
plus a check of `gates.On` at the point the new behavior would diverge.

### Why the decorator swap is height-gated

Replacing `SetPubKeyDecorator` changes which transactions are accepted.
A node running the new code and a node running the old code would
compute different results for the same block if the swap were
unconditional. That is a consensus failure.

The swap is therefore gated. Below the activation height, the ante
chain uses the stock `SetPubKeyDecorator`. At and above it, the chain
uses the Aether replacement. A node that has not restarted with the
new binary halts at the activation height, the same way it would for
any other gated change.

### How to wire it

The ante chain is built in `app/app.go` around line 628:

```go
stdAnteHandler, err := authante.NewAnteHandler(authante.HandlerOptions{...})
```

`NewAnteHandler` builds the stock chain, including
`SetPubKeyDecorator`. It does not expose a way to replace one
decorator.

The replacement is therefore not a modification of the stock handler.
It is a choice, made once when the app starts, between two ante
chains:

- Below the activation height (or on a binary that does not yet
  contain the change), use the stock chain.
- At and above the activation height, use a chain that is identical
  except that `SetPubKeyDecorator` is replaced by
  `RotatingSetPubKeyDecorator`.

Because the ante handler is built once at startup, the height check
cannot be inside `NewAnteHandler`. It is inside the decorator itself:
`RotatingSetPubKeyDecorator.AnteHandle` checks `gates.On` and either
applies the new rule or delegates to the stock behavior. That way one
binary handles both sides of the gate, and a replay from genesis
computes what the chain did.

### What the replacement decorator does

`RotatingSetPubKeyDecorator` is the stock `SetPubKeyDecorator` with one
difference. Where the stock decorator rejects a pubkey whose
`Address()` does not match the signer, the replacement accepts it when
all of these hold:

- The gate is on (`gates.On`).
- The account already has a pubkey.
- The pubkey in the transaction equals the pubkey on the account.

Any other mismatch is still rejected. A first-time pubkey set (account
has no pubkey yet) still requires `pubkey.Address() == signer`. A
pubkey that matches neither the address nor the account's current
pubkey is still rejected.

`SigVerificationDecorator` is not replaced. It already uses
`acc.GetPubKey()`, which `MsgRotateKey` has set to the new key.
`PostQuantumDecorator` is not replaced. It still rejects non-ML-DSA
signers.

## Same-block rule

Transactions in a block are processed in order. A transaction that
lands after the rotation sees the new pubkey and fails if it was
signed by the old key. A transaction that lands before the rotation
sees the old pubkey and succeeds.

To close that gap, `MsgRotateKey` records the account in a transient
store (cleared at the end of every block). At the start of the
handler, if the account is already in the store, the rotation is
rejected. The proposer must order the rotation before any other
message from that account in the same block, or the rotation fails and
can be resubmitted in a later block. Both keys never succeed in the
same block.

## Recovery

Spending guardians do not rotate the key. A spending guardian that can
rotate can take the account.

Recovery is a separate authenticator, `RecoveryThreshold`, registered
by the current key, with its own M of N that is higher than the
spending set. `MsgRotateKey` accepts either:

- A signature from the current key (the account pubkey, or the current
  override), or
- A set of recovery-guardian signatures that meets the threshold.

A spending-guardian threshold is not consulted. An account with no
recovery threshold can only rotate with the current key. That is
accepted.

The fee for a current-key rotation is paid by the account. The fee for
a recovery rotation is paid by the submitter, matching
`MsgExecAuthenticated`.

## Order of work

Each step is separately testable.

1. **AccountKeeper and MsgRotateKey.** Add an `AccountKeeper` to
   `x/accountauth`. `MsgRotateKey` sets the account pubkey to the new
   ML-DSA key. Sequence is not touched. After this step, the old key
   fails and the new key works for transactions that omit the pubkey.

2. **RotatingSetPubKeyDecorator.** The replacement decorator, gated on
   `gates.On`. After this step, the new key works for transactions that
   include the pubkey.

3. **Transient store.** The same-block check. After this step, a
   rotation fails if the account already signed in the block.

4. **RecoveryThreshold.** The separate authenticator and the recovery
   path on `MsgRotateKey`.

5. **Remove the t.Fatal.** `x/accountauth/rotation_test.go` contains
   `TestRotation_OldKeyCannotSpendAndEscrowFollows`. It sets up an
   account with an authenticator, then fails because `MsgRotateKey`
   does not exist. The six assertions are written below the Fatal.
   Remove the Fatal; the assertions run unchanged.

## What this is not

- Not a change to how addresses are derived for new accounts.
- Not a classical fallback.
- Not a move of balances to a new address. The address stays.
- Not a change to escrow, reputation, or the seat rule. They key by
  address; once the address is stable under rotation, they follow.
- Not a bypass of `PostQuantumDecorator`.
- Not a rewrite of the ante chain. One decorator is replaced, and the
  replacement is height-gated.

## Files

| File | What changes |
|---|---|
| `x/accountauth/msg_server.go` | `MsgRotateKey` handler |
| `x/accountauth/keeper.go` | `AccountKeeper` field, transient store key |
| `proto/aether/accountauth/v1/accountauth.proto` | `MsgRotateKey`, `RecoveryThreshold` |
| `app/app.go` | ante chain uses `RotatingSetPubKeyDecorator` |
| `app/rotation_ante.go` | the replacement decorator |
| `gates/gates.go` | no change; the decorator calls `gates.On` |
| `x/accountauth/rotation_test.go` | remove `t.Fatal` when step 5 lands |
| `docs/PQ-AGENT-ROTATION.md` | the design note this plan implements |
