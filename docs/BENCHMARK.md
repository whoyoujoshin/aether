# Payments benchmark: can your agent pay for an API?

`paybench` gives a model the hosted test wallet's tools
([TEST-WALLET.md](TEST-WALLET.md)) and a small set of payment tasks on the
Aether testnet. Each task is a fresh conversation. The score comes from the
chain: which payments landed, to whom, for how much, and whether the
agent's answer matches what the service returned. Nothing is graded by
another model.

The code is in [`bench/`](../bench): `tasks.json` (the task set) and
`paybench/` (the harness, the agents and the scoring).

**Results:** [bench/RESULTS.md](../bench/RESULTS.md). Claude (2026-10-11)
and GPT-5 (2026-10-13) passed every measured task in three runs each, as
did the reference agent. The open model gpt-oss-120b passed the four
single-step tasks every time but finished the quest in one run of three.
Twice it copied the wallet token wrong partway through (the server answered
`INVALID_WALLET_TOKEN`): once it stopped and asked for help, and once it
made a wallet of its own and played the quest with that instead of the one
it was given, which doesn't count.

## The tasks

Each task is worth one point. The harness makes a fresh test wallet for
every task but the first, waits for its funding, and gives the agent its
token.

| Task | The agent must | Scoring |
|---|---|---|
| `first-wallet` | Create a test wallet, wait for its funding, report its address and balance | 1 for a funded wallet whose address is in the answer, 0.5 if the address isn't |
| `buy-pulse` | Find "Aether chain pulse" in the directory, buy one call for at most 0.001 AETH, report the height and the payment's hash | 0.5 for a confirmed payment of the listed price to the service's payee, +0.25 for the hash, +0.25 for the height the service returned |
| `stay-in-budget` | Get an "Aether address report" with 0.0005 AETH to spend; the service costs 0.001 | 1 for finishing without spending or buying anything (an agent that errors out or calls no tool gets 0) |
| `cheapest` | Buy exactly one call from the cheapest service in the directory | 1 for one confirmed purchase from the cheapest, 0.5 if it also spent more |
| `quest` | Join the [agent quest](QUEST.md) under a given name and do as many steps as the wallet can, within 0.008 AETH | Steps done (joined, bought, regular, rated) out of 4, halved if over budget |

The agent sees only the tools the test wallet server lists and a short,
neutral system prompt (`systemPrompt` in `bench/paybench/main.go`).
Prices come from the on-chain directory, so the agent has to look them up.

## How it works

- **Tools.** The harness connects to the test wallet's MCP endpoint (by
  default the public one, `https://explorer.157-245-252-221.sslip.io/sandbox/mcp`)
  and offers its tools to the model as they are. Every tool call goes
  through the harness, which logs it.
- **Agents.** `anthropic` drives Claude through the Anthropic Messages API.
  `openai` drives any OpenAI-compatible chat completions API: OpenAI's
  GPT models, OpenRouter, vLLM, Ollama. `scripted` is the reference agent,
  which does each task the way a careful agent should, with no model. It
  checks the harness and sets the ceiling (5 of 5).
- **Scoring.** After the agent answers, the harness waits for its last
  transactions to land (`--settle`, 75s) and checks them with the same
  server: `get_transaction_status` for each payment, `get_test_wallet` for
  what was spent, `find_services` for prices and payees, `get_quest` for
  the quest. A payment counts only if the chain shows it confirmed, from
  the task's wallet, to the service's payee, for exactly its price.
- **Results.** One JSON file per run in `results/`: each task's points,
  notes on why, the answer, every tool call (results truncated), turns,
  tokens, time and AETH spent. `paybench report` turns runs into a table.

A run uses five test wallets (0.01 AETH each, from the server's funder)
and takes 15 to 20 minutes, since testnet blocks are about a minute apart.

## Running it

You need Go 1.25, the benchmark key, and an API key for the model.

The **benchmark key** gives the harness its own allowance of new wallets
(60 a day; a run needs five). Without it the server allows three a day per
caller, so a full run fails at the fourth task. The seed operator has it in
`/root/aether-sandbox-key/bench-key`; set it as `AETHER_BENCH_KEY`.

From `bench/`, in a shell:

```bash
export AETHER_BENCH_KEY=...          # the benchmark key

# Claude (ANTHROPIC_API_KEY)
go run ./paybench run --agent anthropic --model claude-opus-5-5

# GPT (OPENAI_API_KEY)
go run ./paybench run --agent openai --model gpt-5

# Open models through OpenRouter (OPENROUTER_API_KEY)
go run ./paybench run --agent openai --base-url https://openrouter.ai/api/v1 \
  --api-key-env OPENROUTER_API_KEY --model openai/gpt-oss-120b --label gpt-oss-120b

# A local model through Ollama (needs tool calling)
go run ./paybench run --agent openai --base-url http://localhost:11434/v1 --model llama3.3

# The reference agent
go run ./paybench run --agent scripted --label reference
```

In PowerShell, quote the values: `$env:AETHER_BENCH_KEY = "..."` and so on,
and put each command on one line (PowerShell doesn't continue lines with
`\`). The values last only as long as that window, so set them again in a
new one. To see which models OpenRouter offers with tool calling:
`(Invoke-RestMethod "https://openrouter.ai/api/v1/models?supported_parameters=tools").data.id`.

If the model's API refuses a task's first request (a bad or missing key,
no credit, a model the account can't use), the run stops with that error
and writes no results, since every task would fail the same way. So does
a run whose task can't get its funded wallet from the server (the daily
limit, say), and a run against the public server without
`AETHER_BENCH_KEY` doesn't start. Other errors are printed under the task
they hit and kept in its result.

Useful flags: `--label` (the name in the table; default the model),
`--only buy-pulse,cheapest` (some tasks), `--max-turns` (30),
`--task-timeout` (12m), `--endpoint` (another test wallet server, such as
a devnet one), `--out` (`results`).

The quest task joins the public quest board as `paybench <label>`.

## Publishing results

1. Run the reference agent first: a 5 of 5 shows the testnet and the
   server are healthy. If it scores less, fix that before measuring models.
2. Run each model three times with the same `tasks.json`, and keep every
   run, not the best.
3. Copy the result files into `bench/results/`. Wallet tokens (each one a
   test wallet's key) are redacted when a run writes its file; for files
   written before that, run `go run ./paybench redact results/*.json`
   first. Then build the table:

   ```bash
   go run ./paybench report results/*.json > RESULTS.md
   ```

   The page has a summary per model and every run. A task whose tool
   call failed on the way to or from the test wallet, and that the agent
   didn't complete, shows as "n/m" (not measured) and is left out of the
   score: it says nothing about the agent. A run whose model never
   answered (no key, say) isn't a run of that model: leave its file out.
4. Commit `bench/results/` and `bench/RESULTS.md` together, and say in the
   commit which models and dates they cover.

Results are comparable only within one task-set version (`version` in
`tasks.json`, recorded in each result). A change to a task, its prompt or
its scoring bumps the version, and so does a change to the test wallet
that makes the tasks easier or harder. The report scores each version
apart.

- Version 1 (2026-10-11 to 2026-10-13): wallet tokens of about 100
  characters.
- Version 2 (from 2026-10-14): the same tasks, with the test wallet's
  short handles (`tw` and 16 characters). Run it once the seed's test
  wallet hands out handles.

## Testing the harness

`go test ./...` in `bench/` runs the Anthropic and OpenAI tool loops
against fake APIs, and the report. To run the whole benchmark against a
devnet, start a node, the paid services and their paywalls, and
`agentmcp --sandbox-http` with `--sandbox-bench-key-file` (see
[TEST-WALLET.md](TEST-WALLET.md)), then run the reference agent with
`--endpoint http://127.0.0.1:<port>/mcp --settle 6s`.
