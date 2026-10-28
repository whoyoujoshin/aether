# PQ key rotation: red-team of the built code

**Status:** review only. No code. Against `grok/pq-agent-rotation` at
`5d8dfb1`. Covers `app/rotation_ante.go`, `x/accountauth/rotation.go`,
`x/accountauth/rotation_endblock.go`, and the ante/EndBlocker wiring
in `app/app.go`.

The handler that schedules and cancels is not built. Findings against
unbuilt paths are marked as such.

## Findings

### 1. Nested MsgRotateKey bypasses the signer check (unbuilt handler)

`MsgExecAuthenticated` and `x/authz` `MsgExec` unpack inner messages
and dispatch them through the router. Governance proposals execute
their message on passage. The decorator sees only the outer
transaction's signers.

Sequence: an account registers a session key authorized for
`/aether.accountauth.v1.MsgRotateKey` (or for all messages). The
session key submits `MsgExecAuthenticated` whose inner message is
`MsgRotateKey` for the account. The decorator accepts the outer
signature. If the handler does not reject a nested `MsgRotateKey`,
the rotation is scheduled by a key that was only authorized to send.

The same sequence works with `x/authz` `MsgExec` and with a governance
proposal whose message is `MsgRotateKey`.

The handler must reject `MsgRotateKey`, `MsgCancelRotation`, and
`MsgRegisterRecovery` unless they are the top-level message of a
transaction signed by the account's stored key or by recovery
guardians.

### 2. Activation height of 0 makes the gate uncontrolled

`RotationActivationHeight` is `0`. The decorator treats `0` as "gate
off" and delegates to the stock decorator. A node that sets a real
height and a node that does not will disagree at the first block
where a rotated key signs.

Sequence: binary A has `RotationActivationHeight = 511920`. Binary B
has `0`. At height 511920 a rotated account signs. A accepts; B
rejects. The chains split. The height must be set before the binary
is released.

### 3. EndBlocker silently skips a bad pubkey

`ApplyDueRotations` continues on `SetPubKey` error and does not delete
the pending entry. A rotation whose pubkey is not valid ML-DSA stays
in the store and is skipped every block.

Sequence: a handler bug schedules a rotation with a pubkey of the wrong
length. The EndBlocker skips it. The account is not rotated. The entry
is never deleted. A later cancel removes it. No theft, but the failure
is silent. The handler must reject a non-ML-DSA pubkey at schedule
time.

### 4. Apply is not height-gated

The comment in `app/app.go` says the apply is "height-gated inside the
decorator." It is not. `ApplyDueRotations` runs whenever a pending
rotation's apply height has been reached, regardless of
`RotationActivationHeight`.

Sequence: a rotation is scheduled before the activation height (by a
bug or by a test). At the apply height the EndBlocker sets the
account pubkey even though the decorator is still in stock mode. The
new key then fails the stock address check until the gate opens. The
account is rotated but unusable. The apply must check the gate, or
scheduling must be impossible before it.

### 5. Keeper zero-value check is not a presence check

`app.AccountAuthKeeper != (accountauth.Keeper{})` compares structs.
If the keeper is ever partially initialized, or if a zero keeper is
valid, the check is wrong. In practice the keeper is set only when
the store is mounted, so this is unlikely to fire. It is still a
fragile guard. A boolean or a nil-store check would be clearer.

### 6. Nonce is incremented on schedule, not on apply (correct)

`SetPendingRotation` sets the nonce to `p.Nonce+1`. A cancel does not
touch it. A second schedule uses the new nonce. A signature that
covered the old nonce cannot be replayed. No finding.

### 7. Decorator accepts a matching pubkey without verifying the signature (correct)

The decorator accepts a pubkey that equals the stored pubkey and
continues. `SigVerificationDecorator` then verifies the signature
against the stored pubkey. A transaction that presents the stored
pubkey but was signed by a different key is rejected by the later
decorator. No bypass.

### 8. Omitted pubkey skips the decorator (correct)

A transaction that omits the pubkey (`pk == nil`) continues. The later
decorator uses the stored pubkey. This is correct for a rotated
account and for a first-time account. No finding.

## What is not a finding

- The decorator does not verify signatures. That is the next
  decorator's job.
- The EndBlocker does not reset the sequence. The plan says the
  sequence continues.
- Store iteration order in `PendingRotations` is deterministic for a
  given state. No consensus risk from map iteration.

## Summary

The decorator and the EndBlocker are correct for the paths they
implement. The gaps are: nested messages must be rejected by the
handler, the activation height must be set before release, the apply
must be gated or scheduling must be impossible before the gate, and a
bad pubkey must be rejected at schedule time so the EndBlocker never
sees it.
