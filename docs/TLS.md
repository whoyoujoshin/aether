# TLS for the seed

**Live.** RPC, gRPC, faucet and explorer are all reachable over real,
Let's Encrypt-issued TLS, confirmed by Gitty (the seed's operator) running
`scripts/tls/install.sh`:

```
$ curl -I https://rpc.157-245-252-221.sslip.io/status        # HTTP/2 200
$ curl -I https://faucet.157-245-252-221.sslip.io/           # HTTP/2 404 (expected: GET / on a POST-only faucet; POST /request works)
$ curl -I https://explorer.157-245-252-221.sslip.io/api/stats # HTTP/2 200
$ # a real gRPC call, cmtservice.GetLatestBlock, through grpc.157-245-252-221.sslip.io:443 -> height 119485
```

`agentmcp`, the explorer's `/api/agents`, and the README's agent card now
default to these HTTPS hostnames. The plain `http://157.245.252.221:port`
endpoints still work too -- Gitty left them open -- for anything not yet
switched over.

## How: `scripts/tls/`

```bash
bash scripts/tls/install.sh
```

Run once, on the seed, as root. It installs Caddy (packaged directly by
Ubuntu 20.04+/Debian 12+) and points it at `scripts/tls/Caddyfile`, which
reverse-proxies each service and gets a real certificate automatically.

**No domain purchase, no DNS change.** It uses
[sslip.io](https://sslip.io) "magic DNS": a hostname that embeds this
server's IP -- `rpc.157-245-252-221.sslip.io` -- already resolves to it,
with nothing to configure. That's a genuine public hostname, so Let's
Encrypt's HTTP-01 challenge (which Caddy runs itself, on port 80) works
exactly as it would for a bought domain.

| Was | Became |
|---|---|
| `http://157.245.252.221:26657` (RPC) | `https://rpc.157-245-252-221.sslip.io` |
| `157.245.252.221:9090` (gRPC) | `grpc.157-245-252-221.sslip.io:443` |
| `http://157.245.252.221:8080` (faucet) | `https://faucet.157-245-252-221.sslip.io` |
| `http://157.245.252.221:8081` (explorer) | `https://explorer.157-245-252-221.sslip.io` |

gRPC needed its own line: `aetherd`/`agentmcp`/the explorer all speak
plaintext gRPC (h2c) on `:9090`. Caddy terminates real TLS on the public
side and still speaks h2c to that same backend -- the
`transport http { versions h2c }` block in the Caddyfile is exactly Caddy's
documented way to front a plaintext gRPC server.

## Calling the TLS gRPC endpoint from code

Use `wallet.GRPCCredentials(endpoint)` (or just `wallet.NewClient(endpoint)`,
which already does): it picks TLS for a `:443` endpoint and plaintext for
everything else, so the same call works against `grpc.157-245-252-221.sslip.io:443`
and a plaintext local devnet's `localhost:9090` without a flag. If you roll
your own client instead, **set `ServerName` explicitly** to the bare host
(no port):

```go
host, _, _ := net.SplitHostPort(endpoint) // "grpc.157-245-252-221.sslip.io"
grpc.NewClient(endpoint, grpc.WithTransportCredentials(
    credentials.NewTLS(&tls.Config{ServerName: host})))
```

An empty `tls.Config{}` here is a real trap: with `grpc.NewClient` (unlike
the older `grpc.Dial`), leaving `ServerName` unset sends the *literal*
`"host:443"` string as the TLS SNI, port included. Every real TLS server
(Caddy included) rejects that outright -- a reset connection, no cert ever
served, with a misleading `error reading server preface` error that looks
like an HTTP/2 problem, not the TLS/SNI problem it actually is. This is
exactly the bug `wallet.GRPCCredentials` was written to not have; if you
still hit that error, check `ServerName` first.

## What was and wasn't verified from this session

This session's container can't reach the seed at all (`157.245.252.221` is
egress-blocked for it, sslip.io hostnames included), so Gitty ran and
confirmed the install and the live curl/gRPC checks above. From here:

- `caddy validate` accepted the Caddyfile as written, and its generated
  config matches Caddy's own documented recipe for proxying a plaintext
  gRPC backend.
- Against a local devnet with Caddy fronting it exactly this way (real TLS,
  correct SNI-routed hostname, h2c to the backend), the **real, unmodified
  `wallet.NewClient`** -- the same function `agentmcp`, `walletapi` and the
  explorer all call -- made a real gRPC call (`AllBalances`) over TLS on
  port 443 and got a correctly decoded response back. That's what caught
  the `ServerName` bug above: an earlier version of `GRPCCredentials` left
  it unset and failed with exactly the reset-connection symptom described.
