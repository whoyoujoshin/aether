# PQ agent identity: rotate the key, keep the account

**Status:** design. Not built. First artifact is this note and a failing
test that encodes the property.

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
2. The old pubkey cannot authorize any message in block N or later.
3. The new pubkey can.
4. Registered authenticators (session keys, guardian thresholds) are
   unchanged.
5. Open escrows that name the account as payer or payee still settle
   to that account.
6. The rotation and the revocation of the old key are the same
   transaction. There is no block in which both keys work, and no
   block in which neither does.

## How

The account stores an override pubkey. Signature verification for that
account uses the override when one is set, and the address-derived key
otherwise. A rotation message, signed by the current key (or by a
guardian threshold that meets the account's recovery policy), installs
the new pubkey and clears any in-flight sequence that the old key
could still use.

The override is consulted by the existing signature path. It is not a
new ante handler and not a bypass of `PostQuantumDecorator`. The
decorator still requires an ML-DSA signature; it verifies it against
the override pubkey instead of the one implied by the address.

A rotation signed only by the current key is the normal case. A
rotation signed by guardians is the recovery case, for when the
primary key is lost. Both land in the same block as the revocation.

## What this is not

- Not a change to how addresses are derived for new accounts.
- Not a classical fallback.
- Not a move of balances to a new address. The address stays.
- Not a change to escrow, reputation, or the seat rule. They already
  key by address; once the address is stable under rotation, they
  follow for free.

## Open questions

- Sequence: does the rotation reset the account sequence, or continue
  it? Continuing avoids a replay window; resetting is simpler and
  matches "new key, fresh start." Prefer continue.
- Guardian recovery: is the existing guardian threshold enough, or
  does recovery need a distinct, higher threshold? Prefer the
  existing one, so an account that set guardians for spending can
  use them for recovery without a second registration.
- Fee: the rotation pays its own fee from the account. A recovery
  rotation, submitted by anyone, needs the fee paid by the submitter
  or by a feegrant. Prefer the submitter, matching how
  `MsgExecAuthenticated` already works for guardians.

## First test

`x/accountauth/rotation_test.go` (this branch) encodes the property
and fails because rotation is not implemented. It should pass, without
changing its assertions, once the override and the rotation message
exist.
