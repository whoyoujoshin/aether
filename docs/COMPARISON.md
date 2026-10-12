# How Aether compares

As of 2026-10-12. Projects move fast: corrections are welcome as an issue.

Aether is a testnet where AI agents pay for APIs today, with a
post-quantum key on every account. Two other chains people mention
alongside it, QRL and Nervos CKB, are mature mainnets with post-quantum
signatures in their own ways; neither has agent payments live yet. The
short version: choose Aether to build and test agent payments now; choose
QRL or CKB for a post-quantum chain with a mainnet and a market.

## At a glance

| | Aether | QRL | Nervos CKB |
|---|---|---|---|
| Network | Public testnet (`aether-testnet-1`); no mainnet, no token sale | Mainnet since 2018; QRL 2.0 ("Project Zond") on testnet since March 2026, mainnet after its audits | Mainnet since 2019 |
| Consensus | Cosmos SDK and CometBFT, validators chosen each epoch by tracked mining work (no staking) | Today proof of work; QRL 2.0 is proof of stake | Proof of work (Eaglesong) |
| Account signatures | ML-DSA-44 (FIPS 204), required for every account since genesis | Today XMSS (hash-based); QRL 2.0 uses ML-DSA-87 | secp256k1 by default; SPHINCS+ (FIPS 205) as an opt-in lock script |
| Post-quantum by default | Yes, for accounts | Yes | No, per address by choice |
| Validator keys | ed25519 (classical) | QRL 2.0 validators use ML-DSA-87 | Not applicable (miners) |
| Smart contracts | Cosmos modules (escrow, authz, fee grants, IBC) | QRL 2.0 is EVM-compatible | RISC-V VM (CKB-VM) |
| Agent payments | Live on the testnet: x402 v2 `exact`, a public facilitator, an MCP wallet, a hosted test wallet, an on-chain service directory, escrow | None announced that we found | A draft design (April 2026) for agent payments on Fiber, its payment-channel network |
| x402 | Supported (`exact` on `cosmos:aether-testnet-1`) | None announced | Planned in that draft as the first track: an x402 facilitator with Fiber as a backend |
| Stablecoin | Testnet USDC from Injective over IBC | None announced that we found | RGB++ assets; stablecoins named in the Fiber draft |
| Security audit | None yet | QRL 2.0 audits in progress | SPHINCS+ lock audited (ScaleBit) |

## Where each is stronger

**Aether** lets an agent pay for an API in a minute with nothing installed:
point an MCP client at the [hosted test wallet](TEST-WALLET.md), get a funded
wallet, and pay a listed service with standard x402. Every account signs
with ML-DSA, so no user has to opt in to post-quantum keys. The
[payments benchmark](BENCHMARK.md) runs real models against it. It is a
testnet, unaudited and small: use test funds only.

**QRL** is the longest-running post-quantum chain, with a live mainnet,
exchange listings and a hardware wallet app. QRL 2.0 adds EVM contracts and
ML-DSA-87 (a higher security level than Aether's ML-DSA-44) for accounts
and validators, once its audits finish.

**Nervos CKB** is a large proof-of-work mainnet whose RISC-V VM can verify
any signature scheme, so post-quantum locks arrived without a fork. Its
audited SPHINCS+ lock is live on mainnet for addresses that choose it.
Fiber, its payment-channel network, gives fast off-chain payments, and its
team is designing agent payments on top, with x402 first.

## What this means for builders

- To test an agent that pays for things, Aether works today; its x402
  scheme for Cosmos SDK chains is written up as a
  [spec draft](x402/scheme_exact_cosmos.md) for the x402 project.
- If Fiber ships its x402 facilitator, an x402 client could pay on both
  chains with the same protocol. That is the point of a shared standard.
- For holding value on a post-quantum chain, QRL and CKB have mainnets;
  Aether doesn't yet.

## Sources

Checked 2026-10-12.

- Aether: this repository ([README](../README.md), [X402.md](X402.md),
  [TEST-WALLET.md](TEST-WALLET.md), [BENCHMARK.md](BENCHMARK.md)).
- QRL: [Project Zond](https://www.theqrl.org/project-zond/),
  [roadmap](https://www.theqrl.org/roadmap/),
  [press release on Testnet V2](https://www.theqrl.org/press/qrl-launches-testnet-v2-for-its-postquantum-evmfriendly-blockchain/),
  [QRL Weekly, 2026-09-04](https://www.theqrl.org/weekly/2026-september-04/),
  [QRL 2.0 Testnet V2 (QRL Hub)](https://qrlhub.com/en/zond).
- Nervos CKB: [Native quantum resistance (docs)](https://docs.nervos.org/docs/ckb-features/native-quantum-resistance),
  [quantum-resistant lock script](https://github.com/cryptape/quantum-resistant-lock-script),
  [Fiber agent payments design, issue #1255](https://github.com/nervosnetwork/fiber/issues/1255),
  [SPHINCS+ announcement, 2026-02-02](https://cryptodaily.co.uk/2026/02/nervos-ckb-adapts-with-post-quantum-protection-via-sphincs).
