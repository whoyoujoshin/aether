# Numbers

What an agent paying on the Aether testnet can expect, with each figure
labelled as measured on the live testnet, set in configuration, or
published by someone else. This is a testnet: these are not mainnet
promises.

## Aether testnet (`aether-testnet-1`)

| | Figure | Source |
|---|---|---|
| Block interval | **5.6 s** average, 6 s slowest, over 200 blocks | Measured 2026-10-16, 05:06–05:24 UTC |
| Finality | **One block.** CometBFT commits a block final: no confirmations to count, no reorgs | Consensus design |
| A paid request | **About one block** after the payment is broadcast: the service answers once the payment is in a block | Paywall design; per-call timings below |
| Fees | **Zero.** Validators run with a minimum gas price of `0uaeth` | Configuration |
| Paying in USDC | **No AETH needed** while fees are zero | Tested on a devnet, 2026-10-16 |
| Validators | **4**, run by **2** operators; each proposed 50 of those 200 blocks | Measured, same window |
| Fault tolerance | Blocks need more than 2/3 of the voting power, so one validator of the four can be down | Consensus rule |
| Account keys | ML-DSA-44 (FIPS 204), post-quantum, from genesis | Chain design |

**Before 2026-10-16 the numbers were worse.** One validator committed
with a 60-second timeout instead of 5 seconds. Every round it was meant
to propose timed out, and the next block waited for it. Blocks averaged
24.7 s, and about a third of paid requests took a minute. The fix took
effect at 04:15 UTC; the window above is after it.

### Paid requests in the benchmark

From the [payments benchmark](BENCHMARK.md) runs of 2026-10-11 to 10-14,
before the fix. The time is one `buy_service` call, from the agent's
request to the service's response, payment included:

| | Calls | Median | Slowest 10% |
|---|---|---|---|
| Reference agent | 21 | 6.1 s | 62 s |
| Models | 80 | 8.2 s | 60 s |

The slow group is the 60-second validator above. The next benchmark
runs will give the figures after the fix.

## Next to Base and Solana

Figures from others, for scale. Aether's soft confirmations aren't
faster: Base gives one in 200 ms. What Aether offers is that its one
confirmation is final.

| | Aether testnet | Base | Solana (today) |
|---|---|---|---|
| First confirmation | 5.6 s (final) | 200 ms preconfirmation, 2 s block | about 400 ms slot |
| Final | 5.6 s, one block | about 15–20 min, once on Ethereum | about 12.8 s |
| An x402 payment, end to end | about one block | about 2–5 s reported | not compared here |
| Account keys | post-quantum (ML-DSA-44) | secp256k1 | ed25519 |

Sources:
- **Base:** [Flashblocks spec](https://docs.base.org/specifications/flashblocks.md); [finality tiers](https://chainstack.com/base-transaction-lifecycle-sequencer-finality-rpc/) (Chainstack, May 2026); [x402 timings](https://build.avax.network/academy/blockchain/x402-payment-infrastructure/03-technical-architecture/06-blockchain-settlement) (Avalanche Academy, secondary).
- **Solana:** [today's finality and Alpenglow](https://www.helius.dev/blog/alpenglow) (Helius). Alpenglow targets about 150 ms finality and is expected on mainnet in late 2026. Those are targets, not measurements.

## Measure it yourself

- **Block interval:** read two block headers from any node, e.g.
  `curl -s https://rpc.157-245-252-221.sslip.io/block | jq .result.block.header.time`.
- **A paid request:** run the [benchmark](BENCHMARK-V3.md) against the
  hosted test wallet with your own model or the reference agent. Results
  from outside the project are welcome: open a PR with the result file in
  `bench/results/`.
