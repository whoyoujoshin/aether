# Ligase: escrow from another chain, with one transfer

Ligase lets someone on another chain fund and settle an escrow on Aether
with ordinary token transfers carrying an instruction in their memo. They
never need an Aether key or any AETH. It's how someone holding only USDC
on Noble (or anything on any IBC chain) hires an Aether agent.

**Status:** built, tested in-process, and proven on a two-chain devnet
(see [Proof](#proof)). It needs `x/escrow` live and a coordinated
activation height (see [Activation](#activation)).

## How it works

Send an IBC transfer to the **Ligase address**,
`aether1quqt6klj6f8k6q8qr3esm5dcjf89f6jumufhmd`
(`ligase.Address`), with a memo like:

```json
{"aether": {"escrow": {"payee": "aether1...", "expires_in": "72h", "on_expiry": "refund",
                       "arbiter": "aether1...", "terms": "invoice #42"}}}
```

The three instructions:

| Instruction | What it does |
|--|--|
| `escrow` | Locks the transferred tokens in an [escrow](ESCROW.md) for `payee`, paid by your mailbox. Takes `expires_in` (a duration) or `expires_at` (unix seconds). `on_expiry` is `refund` (the default) or `release`. `arbiter` and `terms` are optional. |
| `release` `{"id": "7"}` | Pays escrow 7 to its payee, as its payer (or arbiter). Send any small amount with it. |
| `withdraw` `{}` | Sends everything waiting in your mailbox that came in over this channel back to you. |

**Your mailbox** is an Aether account made from the channel your transfer
arrives on and your address on your own chain (`ligase.Mailbox`). It has
no key. Only a transfer from you, over that channel, can act for it, and
your chain's validators prove it was you, with the same proof that moves
your tokens.

- It's the payer of the escrows you fund.
- Refunds land there: when the payee declines, or the deadline refunds.
- The small amounts sent with `release` and `withdraw` land there too.
- `withdraw` sends it all home.

**The answer comes back in the acknowledgement:**
`{"ligase":{"action":"escrow","escrow_id":"7","mailbox":"aether1..."}}`.
It's also emitted as a `ligase` event.

**An instruction that can't be carried out fails the whole transfer.**
Examples: a bad payee, a deadline out of range, an escrow you're not
payer or arbiter of. The acknowledgement is an error, and your chain
refunds you, so the tokens never go missing.

- A transfer to the Ligase address with no instruction fails the same way.
- A transfer with an instruction to any other address also fails.
- A transfer with no `aether` key in its memo is an ordinary transfer.

## The two-strands rule

An instruction arriving this way is authorized by a key another chain
accepts, not by an ML-DSA-44 signature on Aether. So it only ever moves
tokens that arrived over IBC (`ibc/...` denoms), never AETH:

- **escrow:** AETH coming back from another chain can't fund one. The transfer fails and you're refunded there.
- **release:** refused for an escrow holding AETH, even if someone named your mailbox its arbiter.
- **withdraw:** only returns tokens that came in over this channel. AETH someone sent your mailbox stays put.

In short, AETH, governance, the treasury and mining stay under
post-quantum signatures. USDC and other IBC tokens were only ever as safe
as their home chains' keys (and Circle's), and this path doesn't change
that.

## Proof

**In-process** (`app/ligase_test.go`, two chains over a real IBC channel):

- **Fund, release, refund, withdraw.** A remote sender funds an escrow and releases it to the payee. It funds a second one, which the payee refunds on Aether. It then withdraws the refund home, and ends up out exactly what the payee was paid.
- **The two-strands rule.** AETH coming back can't fund an escrow. A mailbox named arbiter of an AETH escrow can't release it. AETH in a mailbox stays on withdraw. Removing either the escrow check or the release check makes this test fail.
- **Malformed instructions.** Each one is refused and refunded:
  - no instruction;
  - an instruction sent to the wrong address;
  - two actions;
  - an unknown field;
  - a bad payee;
  - no deadline, or one too far out;
  - a missing escrow.

  An ordinary transfer with an ordinary memo is untouched.
- **Before activation,** a transfer to the Ligase address is ordinary.

**Devnet** (`cmd/helicasetest --outbound-key outbound --ligase`),
2026-09-29. It ran against Aether and `counterpartyd` as separate
processes, with Helicase and `cmd/outbound` relaying everything.

- A counterparty user sent 5000stake to the Ligase address with an escrow instruction. Escrow 1 opened on Aether, with the user's mailbox as payer.
- The user then sent 1stake with `release`. The Aether payee received the 5000 (as its `ibc/...` voucher).
- The counterparty user never signed anything on Aether.

## Activation

`LigaseActivationHeight` in `app/ligase.go` is `1,000,000`: a
placeholder. Ligase acts only once `x/escrow` is live and that height is
reached. Before then, a transfer to the Ligase address is an ordinary
transfer. It adds no store, so nothing halts at that height. But it
changes what such a transfer does, so every validator needs the binary
first. It can share a cutover with `x/escrow` and Helicase.

## Not yet

- **Paying an invoice or paywall** in the same way. A plain transfer with a memo already reaches a seller; the paywall doesn't yet check IBC payments.
- **Agent tools** that build these transfers for an agent on another chain.
