# PQ key rotation: red-team and ICS-23 proof

**Status:** review only. No code. Against `grok/pq-agent-rotation` at
`bef8a4b` and the follow-up brief.

## Red-team

The handler that schedules and cancels is not built. The findings below
are against the decorator, the keeper, and the EndBlocker as they stand,
and against the paths the brief asks about.

### 1. A key that is not the stored key passes the decorator

`RotatingSetPubKeyDecorator.AnteHandle` accepts a pubkey when
`bytes.Equal(acc.GetPubKey().Bytes(), pk.Bytes())`. It does not check
that the signature was made by that key. Signature verification is
`SigVerificationDecorator`, which runs after and uses
`acc.GetPubKey()`.

Sequence: a transaction whose `GetPubKeys()` returns the account's
stored pubkey, but whose signature was produced by a different key.
The decorator accepts. `SigVerificationDecorator` then fails, because
it verifies against the stored pubkey. The transaction is rejected.
This is not a bypass. It is the intended order.

The gap is the opposite case. A transaction that omits the pubkey
(`pk == nil`) skips the decorator's check entirely (`if pk == nil {
continue }`). `SigVerificationDecorator` then uses the stored pubkey.
That is correct for a rotated account. It is also correct for an
account that has never set a pubkey, because the stock path sets it
on first use. No finding.

### 2. Rotation without the current key or guardians

The handler is not built. The proto says `MsgRotateKey` carries
`recovery_signatures` and is signed by `signer`. Nothing in the
current code checks either.

Sequence, once the handler exists and does not check: any account
submits `MsgRotateKey` with an empty `recovery_signatures` and a
`signer` that is not the account. The decorator does not see the
inner message. The handler, if it only checks the outer signature,
accepts. The rotation is scheduled.

This is a finding against the unbuilt handler, not against the
decorator. The handler must reject unless the signer is the account's
stored key, or the recovery signatures meet the threshold.

### 3. Rotation inside authz, an authenticator, or a proposal

`MsgExecAuthenticated` unpacks inner messages and dispatches them
through the message router. `x/authz` `MsgExec` does the same.
Governance proposals execute their message on passage.

Sequence: an account registers a session key. The session key submits
`MsgExecAuthenticated` whose inner message is `MsgRotateKey` for the
account. The decorator sees the session key as the signer of the outer
transaction. It does not see the inner message. If the handler does
not reject `MsgRotateKey` arriving through a router, the rotation is
scheduled by a key that was only authorized to send.

The same sequence works with `x/authz` `MsgExec` and with a governance
proposal whose message is `MsgRotateKey`.

This is a finding. The handler must reject `MsgRotateKey`,
`MsgCancelRotation`, and `MsgRegisterRecovery` unless they arrive as
the top-level message of a transaction signed by the account or by
recovery guardians. A nested dispatch is a bypass.

### 4. Old and new binaries disagree before the activation height

`RotationActivationHeight` is `0`. The decorator checks
`RotationActivationHeight == 0` and delegates to the stock decorator.
Below a non-zero activation height, `gates.On` is false and the
decorator also delegates.

Sequence: activation height is set to H. A transaction at height H-1
includes a pubkey whose address does not match the signer. Both
binaries reject it, because both run the stock check. No disagreement.

At height H, the new binary accepts a pubkey that matches the stored
pubkey. The old binary rejects it. The chains split. That is the
intended gate. No disagreement before the height.

The finding is the placeholder. `RotationActivationHeight = 0` means
the gate is off even after the code is deployed. A node that sets the
height and a node that does not will disagree at the first block where
a rotated key signs. The height must be set before the binary is
released, or the disagreement is uncontrolled.

### 5. EndBlocker skips a bad pubkey

`ApplyDueRotations` continues on `SetPubKey` error. A pending rotation
whose pubkey is not valid ML-DSA stays in the store and is skipped
every block.

Sequence: a handler bug schedules a rotation with a pubkey of the wrong
length. The EndBlocker skips it. The account is not rotated. The
pending entry is never deleted. A later cancel removes it. No theft.
The finding is operational: a bad schedule is silent. The handler must
reject a pubkey that is not valid ML-DSA at schedule time, so the
EndBlocker never sees one.

### 6. Nonce is incremented on schedule, not on apply

`SetPendingRotation` sets the nonce to `p.Nonce+1`. A cancel does not
touch the nonce. A second schedule uses the new nonce.

Sequence: schedule a rotation (nonce becomes 1). Cancel it. Schedule
again (nonce becomes 2). The first signature, which covered nonce 0,
cannot be replayed. Correct. No finding.

## ICS-23 proof of the stored pubkey

### What is proved

The account is stored in the `acc` store under the key
`0x01 || address` (the SDK's `AddressStoreKeyPrefix` is `1`). The value
is the marshaled account, which includes the pubkey as a protobuf
`Any`. The proof shows that this key holds this value at a given
height. The verifier extracts the pubkey from the value.

### How to get the proof

`abci_query` with:

- `path`: `store/acc/key`
- `data`: `0x01` concatenated with the raw address bytes
- `prove`: `true`
- `height`: the height to prove (0 means latest; a verifier should
  pin a height)

The response contains `value` (the account bytes) and `proofOps` (the
ICS-23 proof). The proof is two layers: an IAVL proof that the key
exists in the `acc` store, and a simple-merkle proof that the `acc`
store root exists in the multistore root. The multistore root is the
`AppHash` of the block.

### How to check it

A CometBFT light client verifies a header and yields a trusted
`AppHash`. The verifier:

1. Checks the proof against that `AppHash` using ICS-23
   `VerifyMembership` with the IAVL spec for the inner proof and the
   simple-merkle spec for the outer proof.
2. Unmarshals `value` as the account.
3. Extracts the pubkey from the account's `Any`.
4. Accepts a signature only if it was made by that pubkey.

A proof that verifies against a different `AppHash`, or a value that
does not unmarshal, is rejected. A missing key (non-membership) means
the account has no stored pubkey; the verifier falls back to the
address-derived key.

### What a verifier needs

In Go: `cosmossdk.io/store/types` for the proof ops,
`github.com/cosmos/ics23/go` for `VerifyMembership`, and the account
proto to unmarshal. The light client is CometBFT's `light` package.

In TypeScript: `@confio/ics23` for the proof check, a protobuf decoder
for the account, and a CometBFT light client (or a trusted header from
one). The proof bytes are the same.

### What this is not

Not a proof that the key is the current key. It is a proof that the
key was the stored key at the proved height. A rotation that applied
after that height is not visible. A verifier that needs the current
key must prove a recent height, or accept the lag.
