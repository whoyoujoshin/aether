# Paid agent services

Five small paid APIs run on the seed so the on-chain service directory has
real services for agents to find with `find_services` and buy with
`fetch_paid`. They're `cmd/agentservices`, one process on `127.0.0.1:8500`,
each behind its own `cmd/paywall` and published under the explorer's host.
They read public chain data only and hold no key.

| Service | URL | Price | What it returns |
| --- | --- | --- | --- |
| hello | `https://explorer.157-245-252-221.sslip.io/svc/hello` | 0.000001 AETH | who paid and with which transaction: a practice purchase |
| address | `…/svc/address?addr=aether1…` | 0.001 AETH | balances (AETH, USDC), recent transfers, grants, session keys, miner standing, escrows |
| verify | `…/svc/verify` (POST) | 0.0005 AETH | whether an ML-DSA-44 signature is valid, and the key's aether1 address |
| pulse | `…/svc/pulse` | 0.0005 AETH | height, block time, difficulty, reward, epoch, blocks until selection |
| miners | `…/svc/miners?top=N` | 0.0005 AETH | this epoch's proof-of-work leaderboard |

Every service answers `<URL>/help` free, with what it takes and returns, and
`<URL>/.well-known/x402` (its manifest). `scripts/agentservices/services.txt`
holds the list: name, paywall port, price, title and description.

Each paywall also takes standard x402 v2 `exact` payments (network
`cosmos:aether-testnet-1`), so any x402 v2 client can buy, for example
Coinbase's `@x402/fetch` with `aether-chain-client/x402`. The same install
runs the public x402 facilitator, `aether-facilitator`, at `/facilitator`
(see [X402.md](X402.md)).

## Layout on the seed

```
Caddy  explorer.157-245-252-221.sslip.io/svc/<name>  (strips /svc/<name>)
  -> aether-paywall-<name>   127.0.0.1:8411-8415    (/root/paywall)
    -> aether-agentservices  127.0.0.1:8500/<name>  (/root/agentservices)
      -> aetherd gRPC localhost:9090
```

Payments go to one payee account whose key is in
`/root/aether-services-key` (keyring-backend test). It's a disposable
testnet key: it only receives service payments and pays the 1 uaeth
listings. Its recovery phrase is in `created.json` there.

## Rollout

In the DigitalOcean web console, as root (`bind 'set enable-bracketed-paste off'` first):

```bash
cd /root/aether-src && git fetch origin && git checkout --detach origin/main

# 1. The payee account: creates and funds it, prints its address.
bash scripts/agentservices/announce.sh

# 2. The services and their paywalls (systemd units), checked locally.
PAY_TO=<address from step 1> bash scripts/agentservices/install.sh

# 3. The public routes: adds the /svc blocks to the explorer's site in
#    /etc/caddy/Caddyfile (backup first; restored if Caddy rejects it).
bash scripts/agentservices/caddy.sh

# 4. List all five in the on-chain directory (1 uaeth each).
LIST_SERVICES=yes bash scripts/agentservices/announce.sh
```

Then `find_services` on the public `/mcp` lists them.

## Updating

To deploy new code, check out the new `origin/main` in `/root/aether-src`
and rerun step 2 with the same `PAY_TO`: it rebuilds the binaries and
restarts the seven units (the six above and `aether-facilitator`). Rerun
`caddy.sh` too the first time after it added the `/facilitator` route. Listings stay; they don't need announcing again
unless a URL changes.

## Undoing

```bash
systemctl disable --now aether-agentservices aether-paywall-{hello,address,verify,pulse,miners}
cp /etc/caddy/Caddyfile.bak-svc-<stamp> /etc/caddy/Caddyfile && systemctl reload caddy
```

Delisting is a 1 uaeth payment to the directory address with memo
`x402-delist:<URL>` from the payee account.
