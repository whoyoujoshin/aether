# Payments benchmark results

Task set version 1, 2 · runs on 2026-10-11 to 2026-10-14 · test wallet https://explorer.157-245-252-221.sslip.io/sandbox/mcp

## By model

| Model | Runs | Score | Tasks passed |
|---|---|---|---|
| claude-opus-5-5 (v1) | 3 | 100% | 14 of 14 (1 not measured) |
| gpt-5 (v1) | 3 | 100% | 15 of 15 |
| gpt-oss-120b (v2) | 3 | 100% | 15 of 15 |
| reference (v1) | 1 | 100% | 5 of 5 |
| reference (v2) | 2 | 100% | 10 of 10 |
| gpt-oss-120b (v1) | 3 | 88% | 13 of 15 |

## Every run

| Model | Score | first-wallet | buy-pulse | stay-in-budget | cheapest | quest | Time | Tokens in/out | Spent |
|---|---|---|---|---|---|---|---|---|---|
| claude-opus-5-5 (v1) | 5.00 / 5 | ✓ | ✓ | ✓ | ✓ | ✓ | 259s | 106k / 4893 | 508 uaeth |
| claude-opus-5-5 (v1) | 5.00 / 5 | ✓ | ✓ | ✓ | ✓ | ✓ | 324s | 177k / 6768 | 508 uaeth |
| claude-opus-5-5 (v1) | 4.00 / 4 | ✓ | n/m | ✓ | ✓ | ✓ | 404s | 108k / 5530 | 508 uaeth |
| gpt-5 (v1) | 5.00 / 5 | ✓ | ✓ | ✓ | ✓ | ✓ | 444s | 44k / 15k | 508 uaeth |
| gpt-5 (v1) | 5.00 / 5 | ✓ | ✓ | ✓ | ✓ | ✓ | 362s | 55k / 18k | 508 uaeth |
| gpt-5 (v1) | 5.00 / 5 | ✓ | ✓ | ✓ | ✓ | ✓ | 272s | 54k / 18k | 508 uaeth |
| gpt-oss-120b (v2) | 5.00 / 5 | ✓ | ✓ | ✓ | ✓ | ✓ | 752s | 92k / 7281 | 2506 uaeth |
| gpt-oss-120b (v2) | 5.00 / 5 | ✓ | ✓ | ✓ | ✓ | ✓ | 531s | 67k / 6446 | 1007 uaeth |
| gpt-oss-120b (v2) | 5.00 / 5 | ✓ | ✓ | ✓ | ✓ | ✓ | 608s | 87k / 7637 | 1506 uaeth |
| reference (v1) | 5.00 / 5 | ✓ | ✓ | ✓ | ✓ | ✓ | 136s | 0 / 0 | 508 uaeth |
| reference (v2) | 5.00 / 5 | ✓ | ✓ | ✓ | ✓ | ✓ | 134s | 0 / 0 | 508 uaeth |
| reference (v2) | 5.00 / 5 | ✓ | ✓ | ✓ | ✓ | ✓ | 144s | 0 / 0 | 508 uaeth |
| gpt-oss-120b (v1) | 4.25 / 5 | ✓ | ✓ | ✓ | ✓ | 0.25 | 148s | 34k / 2906 | 502 uaeth |
| gpt-oss-120b (v1) | 4.00 / 5 | ✓ | ✓ | ✓ | ✓ | ✗ | 294s | 80k / 7763 | 501 uaeth |
| gpt-oss-120b (v1) | 5.00 / 5 | ✓ | ✓ | ✓ | ✓ | ✓ | 347s | 70k / 6540 | 1007 uaeth |

✓ full marks, ✗ none, a number for partial credit. Score is the points over the tasks measured; each task is worth 1. Time is the agents' own (not the harness's wallet setup or settling). Spent is what left the task wallets; fees are zero on the testnet.

n/m: not measured. The connection to the test wallet failed during the task and it wasn't completed, so it says nothing about the agent and is left out of the score. The run's file has the details.
