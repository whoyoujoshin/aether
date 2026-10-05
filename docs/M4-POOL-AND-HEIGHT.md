# M4 decision: first Scrypt pool + testnet activation height

Tracking doc opened because the GitHub Issues create UI did not land from the bot.

## Context
M1–M3 are done (`auxpowd`, chain changes A–E, proven with real `litecoind` on regtest). Nothing yet sends real merged-mining work to live Aether. `MergedMiningActivationHeight` is still parked at **20,000,000** until a release sets a real testnet height.

Live tip when filed: **height ~186742** on `aether-testnet-1` (seed `157.245.252.221`). Explorer redeployed to `d326118` (PR #72).

See `docs/MERGED-MINING-PLAN.md` milestone **M4**.

## Decision needed
Pick the **first Scrypt stratum / pool stack** to point at `auxpowd` for a small testnet trial (config-only merge-mine path, AuxPoW chain ID **17776**).

- [ ] A known Litecoin-family stratum that already supports Namecoin/Dogecoin-style aux RPCs
- [ ] Other: _name + why_

## Also propose
A **testnet activation height** (or calendar date) far enough ahead for operators to upgrade, close enough that we do not wait weeks. Rough guide from tip ~186k at ~60s target.

## Height: decided

**205,000** (`October2026UpgradeHeight`; set to 225,000 on 3 October 2026, moved to 205,000 on 4 October), with the smooth
retarget and the randomness beacon, after governance proposals #3 and #4
close. See docs/UPGRADE-2026-10.md. The pool choice is still open.

## Acceptance
- [ ] Chosen pool/stratum named
- [x] Proposed testnet activation height (or date) written down: 205,000
- [ ] Follow-up PR can wire the height into a release
