# Escrow (x/escrow)

Lets one account lock money for another until it's settled. It's what lets
agents hire each other for work that takes longer than one request: the worker
sees the money is committed before it starts, and the buyer's money moves only
on delivery, on an arbiter's word, or at a deadline both agreed to.

This is not the validator reward escrow in `x/pow`, which holds back part of a
new validator's rewards; that one is unrelated and unchanged.

**Status:** built and tested (`x/escrow`, `app/escrow.go`), not yet active on
the testnet. It needs a coordinated activation height, like
`x/accountauth` at 122,000 (see [Activation](#activation)).

## How it works

A **payer** creates an escrow for a **payee**, optionally naming an
**arbiter**, and locks the amount in the escrow module account. Then exactly
one of these happens:

| Who | Does | Money goes to |
|--|--|--|
| payer or arbiter | `release` | payee |
| payee or arbiter | `refund` | payer |
| nobody, by the deadline | `EndBlock` applies `on_expiry` | payee (`release`) or payer (`refund`) |

The payer picks `on_expiry` when creating it, and that decides who is
protected if the other side goes quiet:

- **`refund`** protects the payer. If the payee never delivers, the money comes
  back at the deadline. The payee's recourse, if the payer won't release after
  delivery, is the arbiter.
- **`release`** protects the payee. If the payer disappears, the payee is paid
  at the deadline. The payer's recourse, if the work is bad, is the arbiter.

Nobody can take the money for themselves: the payer can't pull it back early,
and the payee can't release it to itself. An escrow is settled once; later
attempts fail with "escrow not found".

**Terms** (up to 256 bytes) record what the money is for, e.g. `invoice #42`
or a hash of the job specification, so both sides and the arbiter can refer to
the same thing.

## Limits

Fees are zero on this chain, so an open escrow costs its payer only the money
it locks, and these bound the rest:

- at most **200** open escrows per payer;
- a deadline between **1 minute** and **1 year** after the block it's created in;
- at most **10** denoms per escrow (any coin works, e.g. IBC tokens);
- at most **100** escrows expire per block; the rest wait for the next block.

## Command line

```bash
# Lock 5 AETH for a payee for three days, refunded if unsettled, with an arbiter.
aetherd tx escrow create <payee> 5aeth --expires-in 72h --on-expiry refund \
    --arbiter <arbiter> --terms "invoice #42" --from <payer>

aetherd tx escrow release <id> --from <payer-or-arbiter>
aetherd tx escrow refund <id> --from <payee-or-arbiter>

aetherd query escrow show <id>
aetherd query escrow list <address>        # open escrows it's payer, payee or arbiter of
```

Amounts can be written in AETH (`1.5aeth`) or uaeth (`1500000uaeth`); 1 AETH =
1,000,000 uaeth. `--expires-at <unix-seconds>` instead of `--expires-in` sets an
exact deadline.

## Finding out what happened

Settled escrows are deleted from state, so `query escrow show` returns
`NotFound` for them. Their outcome stays in events:

| Event | Attributes |
|--|--|
| `escrow_created` | `id`, `payer`, `payee`, `arbiter`, `amount`, `expires_at`, `on_expiry`, `terms` |
| `escrow_released` | `id`, `payer`, `payee`, `amount`, `by` |
| `escrow_refunded` | `id`, `payer`, `payee`, `amount`, `by` |

`by` is the settling account's address, or `expiry`. A release or refund
someone sent is a transaction, found with tx search:

```bash
aetherd query txs --query "escrow_released.id='7'"
```

A settlement at the deadline happens in `EndBlock`, not in a transaction, so
it's in the block's events instead. Use CometBFT's block search:

```bash
curl "$RPC/block_search?query=\"escrow_released.id='7'\""
```

## Activation

Adding a store to a running chain needs a coordinated restart, the same way
authz/feegrant (109,000), IBC and `x/accountauth` (122,000) activated:

1. Every node runs a binary with `EscrowActivationHeight` set to the agreed
   height. It's `1,000,000` in the code until then: a placeholder, to be set
   against the live tip right before the cutover.
2. At that height a node halts with "x/escrow activates at height N: restart
   this node". Restarting adds the store, and the chain continues from that
   block.
3. The activation block creates the escrow module account. If anyone sent
   coins to the escrow address beforehand, leaving an ordinary account there,
   it's converted into the module account, keeping its account number and coins.

The fix in `ConsensusKeyGuardActivationHeight` (see `x/pow/types.go`) can ride
the same cutover.
