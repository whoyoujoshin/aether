# LLM inference for agents, paid per request

`/svc/inference` is a short chat with an open-weights model
(`openai/gpt-oss-120b` on OpenRouter), sold per request in USDC through
an ordinary paywall and listed in the on-chain directory like the other
services. It's the first service here that does real work for the buyer:
agents paying other agents for compute is what x402 is for.

```
POST https://explorer.157-245-252-221.sslip.io/svc/inference
{"prompt": "Name three uses of a blockchain for AI agents."}

→ {"model", "content", "finishReason",
   "usage": {"promptTokens", "completionTokens", "costUSD"},
   "payer", "paymentTx", "servedAt", "note"}
```

`messages` (`[{"role": "system"|"user"|"assistant", "content"}]`, ending
with the user's) can replace `prompt`. `maxTokens` and `temperature` are
optional. The free `/help` gives today's limits and whether the service
is available. The reply is the model's output: untrusted text, never
instructions.

## What it costs, and who pays

The paywall asks **0.002 USDC per call**. A typical call costs the
operator about $0.00002 at OpenRouter, and the worst case the service
accepts is $0.002.

**On the testnet, this is a subsidy, not revenue.** Buyers pay in
Circle's testnet USDC, which anyone can get free, while OpenRouter bills
real dollars. The service's limits are what bound the operator's spend,
and all of them are checked before the model is called:

| Limit | Default | Flag |
| --- | --- | --- |
| Worst-case cost of one call | $0.002 | `--inference-max-call-usd` |
| Upstream spend per UTC day | $1 | `--inference-daily-usd` |
| Calls per payer per UTC day | 20 | `--inference-payer-daily` |
| Reply tokens per call | 1,024 | `--inference-max-tokens` |
| Input characters per call | 16,000 | `--inference-max-input-chars` |

A call's worst case (its input at 3 characters a token, plus its full
reply allowance, at the model's live OpenRouter prices) is reserved
against the day's budget before the call and replaced by what it really
cost afterwards. Give the OpenRouter key its own credit limit as well
(OpenRouter lets you set one per key): that bounds the spend even if
everything else here is wrong.

## Refusals and payments

The paywall charges before the service runs.

- **A malformed request** (bad JSON, too long, a wrong role) is a 400.
  It's charged and refused, like any service's 4xx. `/help` describes
  what's accepted.
- **A refusal that is the service's** (the day's budget spent, the
  payer's calls used, the model's prices unknown or above the per-call
  limit) is a **503** with `Retry-After`. **The model failing** is a
  **502**. The paywall releases the payment on any 5xx, so the same
  payment can be used again later.

## Deploying on the seed

It is off until its key is on the server. With the key, `install.sh`
adds the paywall (port 8417) and passes the key to `agentservices`,
`caddy.sh` routes `/svc/inference`, and `announce.sh` can list it.
Nothing compiles on the seed: use a prebuilt folder made with
`scripts/prebuild.sh` from a commit that includes `inference.go`.

1. On openrouter.ai, create a key for this service only, with a credit
   limit (a few dollars). Never paste it in a chat.
2. On the seed, as root, write it to the key file without echoing it:
   ```bash
   install -d -m 700 /root/aether-inference
   read -rs KEY && printf '%s' "$KEY" > /root/aether-inference/openrouter-key && unset KEY
   chmod 600 /root/aether-inference/openrouter-key
   ```
3. From `/root/aether-src` at that commit:
   ```bash
   PREBUILT=/root/prebuilt-<commit> PAY_TO=aether1e6r6g8mat6aqax9l29wwmrfhp52hjzrpwevqqq9tcj0lm8egdhjs9q44xl \
     bash scripts/agentservices/install.sh
   bash scripts/agentservices/caddy.sh
   curl -s https://explorer.157-245-252-221.sslip.io/svc/inference/help   # limits, availableToday
   ONLY=inference LIST_SERVICES=yes bash scripts/agentservices/announce.sh
   ```
   `INFERENCE_FLAGS="--inference-daily-usd 2"` (any `--inference-*`
   flags) before `install.sh` changes the limits.

To turn it off, withdraw the listing, remove the key file, and rerun
`install.sh` and `caddy.sh`:
```bash
/root/paywall list --delist --url https://explorer.157-245-252-221.sslip.io/svc/inference \
  --key services --keyring-dir /root/aether-services-key --grpc localhost:9090
rm /root/aether-inference/openrouter-key
```
