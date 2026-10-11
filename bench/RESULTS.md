# Payments benchmark results

Task set version 1 · runs on 2026-10-11 · test wallet https://explorer.157-245-252-221.sslip.io/sandbox/mcp

## By model

| Model | Runs | Score | Tasks passed |
|---|---|---|---|
| claude-opus-5-5 | 3 | 100% | 14 of 14 (1 not measured) |
| reference | 1 | 100% | 5 of 5 |

## Every run

| Model | Score | first-wallet | buy-pulse | stay-in-budget | cheapest | quest | Time | Tokens in/out | Spent |
|---|---|---|---|---|---|---|---|---|---|
| claude-opus-5-5 | 5.00 / 5 | ✓ | ✓ | ✓ | ✓ | ✓ | 259s | 106k / 4893 | 508 uaeth |
| claude-opus-5-5 | 5.00 / 5 | ✓ | ✓ | ✓ | ✓ | ✓ | 324s | 177k / 6768 | 508 uaeth |
| claude-opus-5-5 | 4.00 / 4 | ✓ | n/m | ✓ | ✓ | ✓ | 404s | 108k / 5530 | 508 uaeth |
| reference | 5.00 / 5 | ✓ | ✓ | ✓ | ✓ | ✓ | 136s | 0 / 0 | 508 uaeth |

✓ full marks, ✗ none, a number for partial credit. Score is the points over the tasks measured; each task is worth 1. Time is the agents' own (not the harness's wallet setup or settling). Spent is what left the task wallets; fees are zero on the testnet.

n/m: not measured. The connection to the test wallet failed during the task and it wasn't completed, so it says nothing about the agent and is left out of the score. The run's file has the details.
