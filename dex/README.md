# DEX

Agent desk for a reference AETH/USDC pool. The prime caller is an agent. An instruction becomes one tool call. Settlement is the integer constant-product math in this folder. Nothing is signed or broadcast, and this is not an `x/dex` module.

The pool starts at 50,000 AETH and 20,000 USDC (0.40 USDC per AETH). The fee is 30 bps, kept in the reserves. The agent account starts at 12,000 AETH and 4,800 USDC, with a per-trade cap of 2,000 AETH and 800 USDC. A swap over that cap returns `over_per_tx_limit` and does not move funds. Price impact of 5% or more returns `needs_confirm` until the instruction ends in `confirm`.

```text
pool
quote 1000 aeth to usdc
swap 250 aeth to usdc
swap 5000 aeth to usdc confirm
deposit 100 aeth
withdraw 100
limit 5000 aeth
slippage 0.10
```

Denoms in the call are `uaeth` and the channel-2 USDC denom from [docs/USDC-PLAN.md](../docs/USDC-PLAN.md):

`ibc/064D82A67318DD30F54A4E17B6E487C2864E8B075A5871D3ACAD7A7129F32C5C`

Quote of 1 AETH at the starting reserves returns 398,792 base units. That vector is checked in `src/lib/dex/math.test.ts` and is not derived from the implementation.

```bash
cd dex
npm install
npm test
npm run dev
```

Open `http://localhost:5174`. State stays in this browser (`localStorage`). Reset pool puts the reserves and the agent account back to the snapshot.
