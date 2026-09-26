# Reference demo: an agent pays another agent's tool call

The pitch for agent-to-agent money on Aether isn't "another chain" -- it's that one
agent can pay another for a tool call, under a spend cap it (or its owner) sets,
with no human in the payment loop. `scripts/demo-agent-payment.sh` proves that with
a real transaction on the public testnet, not a mock.

## What it does

Two fresh, disposable keypairs, minted and funded by the script itself:

- **A buyer**, run as `cmd/agentmcp` and driven exactly as an MCP client (Claude,
  or any other) would drive it: over stdio, one tool call at a time.
- **A seller**, run as `cmd/paywall` in front of a tiny paid API, offering
  `aether-pull` (a capped, expiring, revocable on-chain allowance -- see
  [README: pull, for agents](../README.md#ai-agent-wallet-mcp)).

The buyer's agent:

1. asks the testnet faucet to fund itself (`request_testnet_funds`);
2. calls the paid API three times with `fetch_paid`. The first call finds the
   service offers `aether-pull` and grants the seller's collector an allowance --
   **one on-chain transaction** (`MsgGrant`). The other two settle **instantly by
   signature**, no block wait and no further grant.

The seller's collector then batches what's owed into **one** `MsgExec`, spending
under that grant -- the same batching a real seller would run continuously.

Every number in the write-up the script produces is checked against the chain
afterward (a `query bank balances`), not just echoed from what a tool claimed.

## Running it

From a checkout of this repo, against the public testnet:

```bash
bash scripts/demo-agent-payment.sh
```

It builds `aetherd`, `paywall` and `agentmcp` from the checkout, so it always
demos the code actually in the repo. Needs Go, Python 3, and network access to
the endpoints below. Takes about a minute; it prints progress as it goes.

Output lands in `./demo-out/` (override with `OUT_DIR`):

- `README.md` -- the write-up: what happened, the three addresses, and links to
  the explorer for the buyer, the seller, the collector and the collection
  transaction.
- `transcript.json` -- every MCP tool call and its exact response, including the
  seller's signed receipts.
- `paywall.log`, `agentmcp-driver.log` -- raw process output, for debugging a
  run that didn't go as expected.

Other networks (a local devnet, for instance) via environment variables:

```bash
RPC=http://127.0.0.1:26657 GRPC=127.0.0.1:9090 FAUCET=http://127.0.0.1:8080/request \
CHAIN_ID=aether-devnet EXPLORER=http://127.0.0.1:8081 \
  bash scripts/demo-agent-payment.sh
```

A local devnet needs `x/authz`/`x/feegrant` already active (see
`app/AuthzFeegrantActivationHeight`); a normal chain reaches that height on its
own, well before you'd run this.

## The last real run

<!-- Replace this section with the write-up from ./demo-out/README.md after
     running the script against the public testnet, and commit transcript.json
     alongside it (e.g. as docs/agent-demo-transcript.json). -->

Not yet run against the public testnet from this environment (its RPC/gRPC/faucet
are not reachable from here). Verified instead against a local devnet with the
same binaries and the same script, unmodified except for which endpoints it
points at: all three purchases settled, the collector's single `MsgExec` moved
exactly what was owed, and the seller's on-chain balance matched to the uaeth.
