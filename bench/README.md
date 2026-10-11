# paybench: can your agent pay for an API?

A small public task set (`tasks.json`) and a harness (`paybench/`) that
runs it against Claude, GPT and open models through the hosted test
wallet on the Aether testnet, scoring each run from the chain.

```bash
export AETHER_BENCH_KEY=...   # the benchmark's wallet allowance
go run ./paybench run --agent anthropic --model claude-opus-5-5
go run ./paybench run --agent openai --model gpt-5
go run ./paybench report results/*.json
```

Results: [RESULTS.md](RESULTS.md). How it works, how to run each kind of
model, and how to publish results:
[docs/BENCHMARK.md](../docs/BENCHMARK.md).
