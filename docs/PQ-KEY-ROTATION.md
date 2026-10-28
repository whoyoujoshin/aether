# Key rotation (x/accountauth)

**Status (2026-10-28):** built and tested, not active on the testnet.
It is a consensus change: `RotationActivationHeight` is a placeholder
(40,000,000) until an upgrade sets it, and it is **not** part of the
511,920 upgrade (inflow cap, fees, treasury coin spends). On fresh chains
(`aether-1`, `aether-rc-*`) it is on from block 1. Grok's plan
(`grok/pq-agent-rotation`, `docs/PQ-AGENT-ROTATION-PLAN.md` there) was
the design reference. This build is new code.

## Why

An Aether account's address is the hash of its ML-DSA-44 public key. Until
now, an owner who wanted a new key (a key that may have leaked, a new
device, an agent's key being retired) had only one move: send everything
to a new address. Everything bound to the old address stays behind:

- a miner registration and its seat work;
- escrow and its unlock height;
- reputation and the quest;
- directory listings and a paywall's payee;
- authz grants and fee allowances;
- registered authenticators.

Rotation changes the key stored on the account and keeps the address. The
chain already verifies every signature against the stored key
(`SigVerificationDecorator`), so the only thing in the way was
`SetPubKeyDecorator`'s check that a transaction's public key hashes to its
signer.

## How it works

Every account gets a rotation record (gRPC `aether.accountauth.v1.Query/Rotation`;
`wallet rotation <address>`):

| Message | Signed by | Effect |
|---|---|---|
| `MsgRotateKey{account, new_pubkey, new_key_signature}` | the current key | the new key takes over after `RotationDelayBlocks` (15,300, about a day at 5.6 s) |
| `MsgCancelPendingRotation{account}` | the current key | drops a pending key change and a pending recovery-set change |
| `MsgSetRecovery{account, guardian_pubkeys, threshold}` | the current key | after the same delay, M of N guardians (at most 10) may recover the account; no keys and threshold 0 removes the set |
| `MsgRecoverKey{submitter, account, new_pubkey, new_key_signature, guardian_signatures}` | anyone, with M guardian signatures | the new key takes over **at once**, and any pending change is dropped |
| `MsgGuardianCancel{submitter, account, guardian_signatures}` | anyone, with M guardian signatures | drops pending changes |

- **Proof of possession:** every new key signs
  `RotateKeySigningBytes(chain, account, nonce, new_pubkey)`, so nobody can
  rotate an account onto a key they don't hold, such as another account's
  public key.
- **Delayed changes:** changes by the current key apply in the
  `EndBlocker` of their height (`ApplyDueRotations`, with a due index so
  each block reads only what is due), with `key_rotation_applied` (or
  `key_rotation_failed`) and `recovery_set_applied` events.
- **Recovery set versus guardian authenticator:** the recovery set is
  separate from a `guardian_threshold` authenticator. Authenticators never
  rotate: `ExecAuthenticated` refuses all five messages.

### Two nonces

- `nonce` covers the current key's proofs and advances on every action by
  the current key (rotate, cancel, set recovery).
- `recovery_nonce` covers guardian signatures and `MsgRecoverKey`'s proof.
  It advances on every guardian action and whenever a new recovery set
  applies.

They are separate on purpose. A thief holding the current key can rotate
and cancel as often as it likes, but that never moves `recovery_nonce`, so
the guardians' signatures stay valid and the recovery goes through
(`TestRotation_ThiefCannotStaleTheGuardians`). A signature for an old
recovery set stops counting once a new set applies.

### Signing bytes

The sha256 of newline-separated text, domain-separated by its first line:

```
aether-accountauth-rotate/v1\n<chain-id>\n<account>\n<nonce>\n<new pubkey hex>\n
aether-accountauth-recover/v1\n<chain-id>\n<account>\n<recovery nonce>\n<new pubkey hex>\n
aether-accountauth-guardian-cancel/v1\n<chain-id>\n<account>\n<recovery nonce>\n
```

(`accountauth.RotateKeySigningBytes`, `RecoverKeySigningBytes`,
`GuardianCancelSigningBytes`; `MsgRecoverKey`'s own proof uses the rotate
format at the recovery nonce.)

### The ante handler

`app/rotation_ante.go`:

- **`RotatingSetPubKeyDecorator`** replaces the SDK's `SetPubKeyDecorator`
  in a copy of v0.50.14's `NewAnteHandler` decorator list (`newAnteChain`;
  `TestAnteChain_SDKVersion` fails if `go.mod` moves the SDK, so the copy
  is checked against the new list). From the height, a signer may carry a
  key whose address isn't its own only if that key is exactly the one
  stored on the account. For such a transaction it runs the stock
  decorator with the address check off, then turns `IsSigverifyTx` back
  on for the rest of the chain. `SigVerificationDecorator` skips
  verification when that flag is off, so `TestKeyRotation_EndToEnd` sends
  a forged signature over the rotated account's (public) key and checks
  it fails. Any other mismatch goes to the stock decorator, which refuses
  it. Ordinary transactions read nothing extra.
- **`checkRotationMsgs`** runs before the chain, in every mode:
  - From the height, a rotation message must be at the top level of a
    transaction. It is refused inside authz `MsgExec`,
    `MsgExecAuthenticated` or a governance proposal, and so is an authz
    grant for one: a grantee could otherwise move the granter's address
    onto its own key. Messages nested more than 6 deep are refused.
  - Before the height, any transaction reaching a rotation type is
    refused, nested or not, and so is a grant for one. See activation
    below.

## Delay versus balance

The delay protects the **address**, not the balance. A thief holding the
current key can spend the balance at once, as with any key. What the delay
stops is the thief taking the address over for good: rotating onto its
own key, or removing the recovery set. For a day the owner can still:

- cancel it with the current key, if they still have it (`wallet
  cancel-rotation`);
- have the guardians cancel it (`guardian-cancel`);
- have the guardians recover the account onto a fresh key at once
  (`recover-key`), which drops the thief's pending change.

The guardians act at once and the current key waits, because the
guardians are the ones defending the account from a stolen key. Choose
guardians who will notice: watch for `key_rotation_scheduled` and
`recovery_set_scheduled` events on your accounts.

## Using it: the `wallet` CLI

```
wallet pubkey <name>                                          # hex key, for guardians and set-recovery
wallet rotation <name-or-address>
wallet rotate-key <name> <new-key-name>                       # applies after the delay
wallet sign-for <new-key-name> <account>                      # once it has applied
wallet cancel-rotation <name>
wallet set-recovery <name> <threshold> <guardian>...          # hex keys or key names
wallet guardian-sign <guardian> <account> recover <new-pubkey> [--out sig.json]
wallet guardian-sign <guardian> <account> cancel [--out sig.json]
wallet recover-key <submitter> <account> <new-key-name> <sig.json>...
wallet guardian-cancel <submitter> <account> <sig.json>...
```

- **Guardian signatures:** each guardian signs on their own machine at
  the account's current recovery nonce. The submitter collects the files,
  which name the account, the action, the nonce and the approved new
  key, and are refused if they don't match.
- **`signs-for.json`:** after a rotation the new key signs for an account
  whose address isn't its own. The wallet records the pairing in
  `signs-for.json` next to the keyring (mode 0600). `recover-key` writes
  it; after `rotate-key`, run `sign-for` once the change has applied.
  From then on, `GetAccount`, `ListAccounts` and everything built on them
  (agentmcp, the faucet, powminer, `paywall list`) send from the account.
- **Copying keys:** `copy-key` and `CopyTestKey` carry the record with
  the key.
- **`aetherd tx ... --from <key>`** reads the same `signs-for.json` in its
  keyring directory (`cmd/aetherd/signsfor.go`). When `--from` names a
  rotated key, or the account itself, the transaction is sent from the
  account and signed by the key, and aetherd says so on stderr.
  - `bank send <key> ...` does the same.
  - The other commands that take their sender as the first argument
    (`bank multi-send`, `feegrant grant`) refuse a rotated key,
    rather than send from the key's own address.
  - `aetherd tx sign` still refuses: it checks the signer by the key's own
    address. Sign and broadcast in one step instead.

## The signing-key rule, off chain

Every check made off chain applies the chain's rule (`wallet.SignsFor`):
the key that signs for an account is the one stored on it; only an
account with no stored key yet is signed for by the key its address hashes
from. Checking only the hash is wrong both ways after a rotation: it
refuses the new key, and it **accepts the retired one**, whose hash is
still the address.

- **The rule's users:**
  - the x402 facilitator;
  - paywall prepaid and pull sign-in;
  - mandates;
  - receipts and receipt delegations;
  - agentmcp's `--approver` decisions, its `--mandate-file` and the
    receipts it checks.
- **Automatic revocation:** a rotation revokes everything the retired key
  signed. Mandates, approvals and receipts it signed stop verifying.
- **Reading keys:**
  - `wallet.CachedAccountKeys` keeps each answer for 30 seconds
    (`DefaultAccountKeyTTL`), so a verifier can accept a retired key for up
    to that long after a rotation applies.
  - A node that can't answer is a refusal (retryable where the protocol
    has a code for it), never a pass.
  - `cmd/paywall` and agentmcp read keys from their `--grpc` node.
- **Without chain access:** `Mandate.Verify`, `Receipt.Verify` and
  `CheckMandate`/`CheckReceipt` keep the hash-only check. The `...With`
  forms take the chain's keys.
- **Creating them:** `NewMandate` and `NewReceiptDelegation` check only the
  signature, since only the chain knows a rotated owner's key.
- **Receipts after a payee rotates:** those its retired key signed stop
  verifying (idea C in the follow-up plan would keep them valid when their
  payment predates the rotation).

## Limits and follow-ups

- **Clients:** the TypeScript and Python clients don't build rotation
  messages yet, and their sellers' sign-in checks only the hash. Their
  vectors are `clients/testdata/rotation.json`:
  - signing bytes and signatures;
  - every message's encoding;
  - a signed `MsgRotateKey` transaction;
  - a `Rotation` query answer;
  - the signs-for rule's cases.

  `clients/vectors` checks the file against the Go code.
- **x402 spec:** `docs/x402/scheme_exact_cosmos.md` step 5 states the rule
  for x402-foundation/x402#3788; `@x402/cosmos` still needs it.
- **Shared keys:** a key may sign for several accounts: its own address
  and any account rotated onto it. Each transaction is still bound to one
  account by its account number.

## Activation

Rotation adds new message types and no store (the records live in
`x/accountauth`'s store, mounted since 122,000), so activation is a plain
height gate rather than a restart at the height. As with every new message
type, nodes on the old binary can't decode a rotation transaction while
nodes on the new binary can. If a block carried one before every
validator had upgraded, they would get different results and halt.
`checkRotationMsgs` refuses such transactions before the height in
CheckTx, so they never reach the new binary's mempool, and the old binary
refuses them on decode. As a result, no honest proposer includes one.
Still, send nothing that touches rotation until every validator runs the
new binary.

Upgrade checklist:

1. Choose H with Joshua and set `RotationActivationHeight`.
2. Build with Go 1.26.10 (`scripts/prebuild.sh`, plus peer-1's `.exe`) and
   swap the validators one at a time, with the abort line about 600
   blocks before H.
3. After H, run `wallet rotation <address>` against a validator and check
   it reports `active`.
4. Then rotate a test account end to end: `rotate-key`, wait the delay,
   `sign-for`, send.

## Tests

- **`x/accountauth/rotation_test.go`:**
  - the gate;
  - proof checks;
  - the delay and the apply;
  - cancel;
  - recovery-set validation, apply and removal;
  - recovery threshold, distinct guardians, outsiders and replay
    (including after a later rotation);
  - the two-nonce griefing case;
  - guardian cancel and stale signatures after a set change;
  - `ExecAuthenticated` refusing rotation.
- **`app/rotation_e2e_test.go`** (through the real app on `aether-rc-rot`):
  - a scheduled rotation applies;
  - simulation works;
  - the new key signs, with the SDK's `tx.acc_seq`/`tx.signature` events;
  - the old key, an unrelated key and a forged signature on the account's
    key all fail;
  - an authz grant for `MsgRotateKey` is refused;
  - before the height on a testnet chain ID, the stock refusal applies;
  - the SDK version guard.
- **`app/rotation_walk_test.go`:** every nesting case before and after
  the height.
- **`wallet/rotation_test.go`:** the signs-for record (persisted, copied,
  removed, bad file refused) and the message builders' proofs.
- **`x402/rotation_test.go`:** a retired key is refused; a rotated payer is accepted only with its
  on-chain key.
