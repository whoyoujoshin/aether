# Helicase: the proposer relays IBC packets in

Helicase lets Aether bring IBC packets in from other chains itself, so no
relayer ever has to sign a transaction on Aether. It keeps the chain's
rule that every signature is ML-DSA-44, and it lets Aether connect to
chains like Noble and Osmosis without a second chain or a relayer fork.

**Status:** built, tested in-process, and proven on a two-chain devnet
(see [Proof](#proof)). Not active on the testnet: it needs a coordinated
activation height (see [Activation](#activation)).

## Why it's safe

An IBC relay message carries its own proof. That covers a client update,
a packet, an acknowledgement and a timeout. Aether's light client of the
other chain checks each one against the other chain's validator
signatures. The relayer who submits a message adds nothing to its
security: on other chains its signature only pays the fee and limits
spam. The same proofs that protect Aether from a dishonest relayer
protect it from a dishonest proposer.

So on Aether, a relay transaction has **no signature at all**, and only
the block proposer can include one:

- **What counts as one:**
  - no signatures;
  - only `MsgUpdateClient`, `MsgRecvPacket`, `MsgAcknowledgement`, `MsgTimeout` or `MsgTimeoutOnClose`, each naming the Helicase address as signer;
  - memo `helicase`;
  - no fee, no timeout height and no extension options.
- **Only the proposer can include one.** `CheckTx` and simulation refuse it with `app` error 2, so it never enters a mempool. It reaches a block only through the proposer's `PrepareProposal`.
- **Every validator checks it.** `ProcessProposal` rejects a block with a malformed relay transaction, or with more than 100 of them. Execution then checks the proofs. A message whose proof fails fails its own transaction and nothing else.
- **A dishonest proposer can't forge anything.** It can only leave packets out, and the next proposer brings them in.
- **What the Helicase address is:** `aether10mgp0ma7g30rykkwa0379xhkya76knemdne7pk`, derived from the name `helicase` like a module account. It has no key and holds nothing. Relay transactions show it as their sender.

A relay message can only do what the other chain's validators already
signed for: move a light client forward, deliver a packet, or settle one.
It never touches Aether accounts, balances or signatures. Every
transaction a person or agent signs still has to be ML-DSA-44.

## How a node relays

A node started with a counterparty runs a **worker** (package
`helicase`) next to the chain. It holds no key. It reads the other chain
over RPC. Every couple of seconds it:

1. **Reads the client.** It reads Aether's light client of the other chain and every open channel over it.
2. **Gathers work**, as of the other chain's latest block:
   - packets the other chain sent that Aether hasn't received;
   - acknowledgements the other chain wrote for packets Aether sent;
   - packets Aether sent that timed out before the other chain received them.
3. **Proves each one.** Each gets its proof, behind a client update to that block. With no work, the client is still updated once it's a third of the way to expiring, so a quiet channel's client never expires.
4. **Builds one relay transaction per message.** A duplicate, such as a packet the previous proposer already delivered, is a harmless no-op and doesn't take the others with it.

When the node proposes, it puts those transactions at the front of its
block, within half the block's byte limit and 100 transactions. Nodes
without a counterparty configured propose none but still check and
execute everyone else's.

```bash
aetherd start --home C:\aether-data\.aether \
  --helicase.counterparty-rpc https://<the other chain's RPC> \
  --helicase.client-id 07-tendermint-0
```

The same settings can go in `app.toml` under `[helicase]`
(`counterparty-rpc`, `client-id`, `aether-rpc`, `interval`). The other
chain's RPC must serve `tx_search`, because the worker finds a packet's
contents from the transaction that sent it.

## What still needs a signature

- **The handshake.** Creating the clients, connection and channel is done once, by `cmd/relayer` with an ML-DSA-44 key, as before. Those messages aren't proven against an existing client, so they aren't relay messages.
- **The other direction.** Packets going *out* are delivered *to* the other chain, which wants its own kind of signature. Relaying onto Noble or Osmosis takes any key those chains accept. Noble charges no fee for packets, acknowledgements or client updates.
  - The devnet proof does this with `relayer.DeliverPacket` and `relayer.DeliverAcknowledgement` using a secp256k1 key.
  - Running it unattended, as our own daemon or a stock relayer, is the next step.

## Proof

**In-process** (`app/helicase_test.go`, ibc-go's `ibctesting`):

- **Real ICS-20 traffic.** Every message relayed onto Aether goes through `PrepareProposal` → `ProcessProposal` → `FinalizeBlock` as an unsigned relay transaction:
  - client updates;
  - a packet in;
  - an acknowledgement of a packet out;
  - a timeout with its refund.
- **Other tests:**
  - The same traffic runs with and without the app-side mempool.
  - A packet proposed twice is received once.
  - `CheckTx`, recheck and simulation refuse relay transactions.
  - Before activation, nothing changes.
  - `ProcessProposal` rejects each malformed shape and the 101st transaction.
  - The proposer drops invalid transactions from its own proposal.
  - With a block gas limit, relay transactions take at most half of it.
- **The main test depends on Helicase:** with activation moved out of reach, it fails.

**Devnet** (`cmd/helicasetest`), 2026-09-29. The setup was an Aether node
and a `counterpartyd` node, each its own process. The Aether binary was a
local build with small IBC and Helicase activation heights; the source
edit was reverted after building. The Aether node ran with
`--helicase.counterparty-rpc` and with the app-side mempool on
(`--mempool.max-txs 5000`). After a signed handshake:

1. **In.** The counterparty sent 7000stake to an Aether address. Aether received it at height 27, in unsigned relay transactions: a client update and the packet. The counterparty got Aether's acknowledgement from a secp256k1-signed relay onto the counterparty.
2. **Out.** An Aether user sent 12345uaeth, an ordinary ML-DSA-44-signed transfer. The counterparty received it the same way. Aether processed the acknowledgement at height 32, unsigned.
3. **Timeout.** An Aether user sent 12345uaeth with a 15s timeout, and nobody relayed it. At height 37 Aether timed it out, unsigned, and refunded it.

Each of those Aether transactions was checked: 0 signatures, memo
`helicase`, sender the Helicase address. The Aether relayer key stayed at
sequence 5, where the handshake left it. The node logged no rejected
proposals, no failed relay transactions and no panics. An earlier run
without the app-side mempool gave the same result.

To reproduce:

1. Build `aetherd` with `ibcActivationHeight` and `helicaseActivationHeight` set to a small value. This is a local build only; never commit it.
2. Start a single-validator devnet per `docs/DEVNET.md` with a funded `relayer` key and a funded `user` key. Add the two `--helicase.*` flags above, with client `07-tendermint-0`.
3. Restart the node when it halts at IBC's activation height.
4. Start `counterpartyd` on non-default ports with a funded `relayer` key.
5. Run `cmd/helicasetest` pointed at both (see its flags).

## Activation

`HelicaseActivationHeight` in `app/helicase.go` is `1,000,000`: a
placeholder, to be set against the live tip before a cutover. It adds no
store, so nothing halts at it: from that height, nodes accept relay
transactions. That still changes consensus.

**Every validator has to run a binary carrying the height before the
chain reaches it.** A node on an older binary would run a relay
transaction as an unsigned transaction and refuse it. It would then end up
with a different app hash and stop at the next block.

## Limits and next steps

- **One path per node.** A node follows one client, `--helicase.client-id`, and every open channel over it. Following several chains means one worker per client, which is a small change.
- **Only 07-tendermint clients.** Every Cosmos SDK chain uses that client type.
- **The proposer does the work.** Relaying adds RPC reads to the other chain on the node that proposes. The worker runs beside consensus and never blocks it: `PrepareProposal` only takes what's already built.
- **Next:**
  - an unattended relayer for the outbound direction;
  - then Noble and Osmosis testnets (see [USDC-PLAN.md](USDC-PLAN.md)).
