# PQ agent identity: rotate the key, keep the account

**Status:** design. Not built. Addresses the review of the first draft.

## Why

An agent's address is `hash(ML-DSA pubkey)` (ADR-028, `crypto/mldsa`).
Rotating the primary key therefore changes the address. Balances,
authenticators, open escrows, and any reputation keyed by address stay
on the old account. The agent is a new agent.

`x/accountauth` already gives an account session keys and guardian
thresholds, both ML-DSA, both registered by the primary key. It does
not let the primary key itself be replaced. An owner who loses the
primary key, or who wants to retire it after a suspected leak, has no
path that keeps the account.

Most account-abstraction designs assume a classical key exists as a
fallback. Aether forbids that. The rotation has to work with ML-DSA
only.

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

## How

### Ante change (required)

The override is not visible to the stock ante chain, and the design
says so.

`PostQuantumDecorator` is a type gate only. Real verification is
`SigVerificationDecorator`, which uses `acc.GetPubKey()`.
`SetPubKeyDecorator` refuses a pubkey whose `Address()` does not match
the signer, and skips if a pubkey is already set. An override stored
only in `x/accountauth` is never consulted.

Rotation therefore changes the ante path. Two decorators learn to
resolve the pubkey:

- `SetPubKeyDecorator` accepts a pubkey that does not match the
  address when the account has an override set to that pubkey, and
  sets it on the account.
- `SigVerificationDecorator` verifies against the override pubkey when
  one is set, instead of the pubkey stored on the account.

`PostQuantumDecorator` is unchanged: it still rejects any signer whose
pubkey is not ML-DSA. The override must be ML-DSA; a rotation that
installs anything else fails in the decorator.

This is a consensus change. It is height-gated, the same way
`x/accountauth` itself was, and it does not bypass the type gate.

### Override and rotation message

The account stores an override pubkey in `x/accountauth`. Empty means
no override; verification uses the account pubkey as today.

`MsgRotateKey` installs the override. It is signed by the current key
(the account pubkey, or the current override). It carries the new
ML-DSA pubkey. On success the override is the new pubkey, the account
address is unchanged, and the old key is no longer accepted.

The account sequence continues. It is not reset. A reset would make a
previously signed old-key transaction valid again under a fresh
sequence. Continuing closes that window.

### Same-block rule

A transaction signed by the old key may already be in the mempool when
the rotation lands. Tx order is not a guarantee.

The rule: once a rotation for an account is included in a block, any
later message in that block signed by the old key for that account is
rejected. The rotation is processed before any other message from that
account in the same block, or the other message fails verification
against the override. Both keys never work in the same block.

### Recovery

Spending guardians do not rotate the key. A spending guardian that can
rotate can take the account.

Recovery is a distinct guardian threshold, registered separately, with
a higher M of N than the spending set. `MsgRotateKey` signed by the
recovery guardians installs the override the same way a current-key
rotation does. An account with no recovery guardians cannot rotate
without the current key; that is accepted.

The fee for a recovery rotation is paid by the submitter, matching
`MsgExecAuthenticated`. The fee for a current-key rotation is paid by
the account.

## What this is not

- Not a change to how addresses are derived for new accounts.
- Not a classical fallback.
- Not a move of balances to a new address. The address stays.
- Not a change to escrow, reputation, or the seat rule. They already
  key by address; once the address is stable under rotation, they
  follow for free.
- Not a bypass of `PostQuantumDecorator`.

## Decisions

- Sequence continues. It is not reset.
- Recovery is a separate, higher guardian threshold. Spending
  guardians cannot rotate.
- The ante path changes. `SetPubKeyDecorator` and
  `SigVerificationDecorator` resolve through the override. This is
  consensus and height-gated.
- Same-block: after a rotation is included, old-key messages for that
  account in the same block fail.

## First test

`x/accountauth/rotation_test.go` should set up an account with a
balance, an authenticator, and an open escrow, submit `MsgRotateKey`,
and assert the six properties. It fails today because the message and
the override do not exist. The assertions are the property; they are
not weakened when the implementation lands.

## Fix strategy

The ante change is smaller than the first revision said, and it is
still required.

### What works without an ante change

`SigVerificationDecorator` uses `acc.GetPubKey()`. If `MsgRotateKey`
sets the account pubkey to the new key, every later transaction
verifies against it. The old key fails, the new key works, the
address is untouched, and the sequence continues because nothing
resets it. `x/accountauth` needs an `AccountKeeper` to do the set.

### What still needs an ante change

`SetPubKeyDecorator` checks `pubkey.Address() == signer` before it
checks whether the account already has a pubkey. A transaction from
the new key that includes its pubkey — which the SDK includes by
default — fails that check, because the new key's address is not the
account address.

The fix is a replacement for `SetPubKeyDecorator`, used in place of
the stock one when the ante chain is built. It accepts a pubkey that
does not match the signer address only when that pubkey equals the
pubkey already on the account. Any other mismatch is still rejected.
`SigVerificationDecorator` is unchanged. `PostQuantumDecorator` is
unchanged.

This is still a consensus change and still height-gated. It is one
decorator, not a rewrite of the ante chain.

### Same-block rule

Transactions in a block are processed in order. A transaction that
lands after the rotation sees the new pubkey and fails if it was
signed by the old key. A transaction that lands before the rotation
sees the old pubkey and succeeds.

The rotation handler records the account in a transient store. A
check at the start of `MsgRotateKey` rejects the rotation if that
account has already had a message succeed in the same block. The
proposer orders the rotation first, or the rotation fails and can be
resubmitted. Both keys never succeed in the same block.

### Recovery

A separate `RecoveryThreshold` authenticator, registered by the
current key, with its own M of N. `MsgRotateKey` accepts either the
current key or a recovery-threshold signature set. A spending
guardian threshold is not consulted. An account with no recovery
threshold can only rotate with the current key.

### Order of work

1. Add `AccountKeeper` to `x/accountauth` and `MsgRotateKey` that sets
   the account pubkey. Sequence is not touched.
2. Replace `SetPubKeyDecorator` with the version that accepts the
   account's own pubkey. Height-gate the swap.
3. Transient store for the same-block check.
4. `RecoveryThreshold` authenticator and the recovery path on
   `MsgRotateKey`.
5. Remove the `t.Fatal` in `rotation_test.go`. The assertions run
   unchanged.

Each step is separately testable. Step 1 makes the old key fail and
the new key work for transactions that omit the pubkey. Step 2 makes
it work for transactions that include it. Step 3 closes the
same-block gap. Step 4 adds recovery.
