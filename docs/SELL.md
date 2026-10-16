# Sell to agents

Put any HTTP API behind per-request payments on the Aether testnet and list
it where agents look. There's no sign-up and no approval, and nobody takes
a cut: the directory is a transaction anyone can send, and payments go
straight to your address. It takes about ten minutes.

Agents then find your service with `find_services` (on the [public `/mcp`](CONNECT.md),
the [hosted test wallet](TEST-WALLET.md) or a local `agentmcp`), pay with
standard x402 or Aether's own schemes, in AETH or USDC, and get your
response. It's the testnet: AETH has no value, and USDC is Circle's testnet
USDC. Use it to try selling to agents before anything is at stake.

## 1. A payee key

Payments go to this account, and only it can list the service. `aetherd`
and `paywall` are in every platform's
[release archive](https://github.com/whoyoujoshin/aether/releases):

```bash
aetherd keys add payee --keyring-backend test --keyring-dir ./payee-key
```

It prints the address (`aether1…`) and a recovery phrase: keep the phrase.
Ask the faucet for a little test AETH; listing costs 1 uaeth:

```bash
curl -X POST https://faucet.157-245-252-221.sslip.io/request \
  -H 'Content-Type: application/json' -d '{"address":"aether1..."}'
```

## 2. The paywall

`paywall` (in the same archive) is a reverse proxy: unpaid requests get
HTTP 402 with the price, paid ones reach your API. Your API needs no
changes.

```bash
paywall --upstream http://localhost:8000 --listen :8402 \
  --pay-to aether1... --price "0.001 AETH" \
  --grpc grpc.157-245-252-221.sslip.io:443 --chain-id aether-testnet-1 \
  --name "Weather now" --description "Current weather for a city. GET ?city=Paris" \
  --free /help --public-url https://api.example.com/weather
```

- `--name` and `--description` are what agents read in the directory:
  say what the service returns and how to call it.
- `--free /help` serves a path without payment; a short `/help` describing
  the parameters lets agents check before they pay.
- `--public-url` is the address agents use. The directory reads the
  manifest from there (`<URL>/.well-known/x402`), so it must be public
  HTTPS: put the paywall behind Caddy, nginx or a Cloudflare tunnel. Keep
  your API itself reachable only through the paywall, or callers can skip
  paying.
- Each paid request reaches your API with `X-Aether-Payer` (who paid) and
  `X-Aether-Payment-Tx` (the payment).

To charge USDC instead, give the price in USDC and its route:

```bash
  --price "0.01 USDC" --usdc-path transfer/channel-2 \
  --usdc-base-denom erc20:0x0C382e685bbeeFE5d3d9C29e29E341fEE8E84C5d --usdc-issuer Injective
```

One paywall charges one asset; run two for both. Agents holding only USDC
can pay: fees are zero on the testnet, so they need no AETH.

## 3. List it

From where the payee key is:

```bash
paywall list --url https://api.example.com/weather --key payee --keyring-dir ./payee-key
```

It reads your manifest first and stops if the directory wouldn't accept it
(for instance, if `--pay-to` isn't this key's account), then sends the
listing and waits for its block (about a minute). Agents see it within a
few minutes. `paywall list --delist …` withdraws it.

Check it with `find_services` from any of the MCP servers above, or on
the explorer's **Services** page.

## A minimal upstream to try

Any HTTP server works. This one answers with the payer, to see the
whole path:

```python
# upstream.py: python3 upstream.py, then point --upstream at http://localhost:8000
from http.server import BaseHTTPRequestHandler, HTTPServer
import json

class H(BaseHTTPRequestHandler):
    def do_GET(self):
        body = {"hello": "agent", "payer": self.headers.get("X-Aether-Payer"),
                "payment": self.headers.get("X-Aether-Payment-Tx")}
        if self.path.startswith("/help"):
            body = {"usage": "GET / returns a greeting and who paid"}
        data = json.dumps(body).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

HTTPServer(("127.0.0.1", 8000), H).serve_forever()
```

## Without the Go proxy

The TypeScript and Python clients (`aether-chain-client` on npm and PyPI)
include a seller kit that does the same inside a Node or Python service:
the 402 answers, the manifest for the directory, receipts, and USDC
prices. See [the README](../README.md#ai-agent-wallet-mcp) ("Selling,
too"). A service using another x402 server SDK can settle through the
public facilitator ([X402.md](X402.md)). List either with `paywall list`
or by sending the 1 uaeth yourself (memo `x402-service:<URL>` to the
directory address `paywall` prints).

## What else is there

- **Reputation:** agents rate services they paid (`x402-rate`), and the
  directory shows payments and ratings from the last week of blocks.
- **Receipts, prepaid balances, pull payments and withdrawals:** flags on
  `paywall`; see the [README](../README.md#paid-apis-x402).
- **Payments wait for their block,** about a minute; a block is final once
  committed (no confirmations to count, no reorgs).
