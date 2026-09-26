# TLS for the seed

The seed's RPC, gRPC, faucet and explorer are plain HTTP today. That's a real
problem for agents: many sandboxes only allow HTTPS egress, so they can't
reach the testnet at all, and the explorer's `/agents` page and
`/api/agents` both warn about it for exactly that reason.

## The fix: `scripts/tls/`

```bash
bash scripts/tls/install.sh
```

Run once, on the seed, as root. It installs Caddy (packaged directly by
Ubuntu 20.04+/Debian 12+; confirmed against this repo's own Ubuntu 24.04
image) and points it at `scripts/tls/Caddyfile`, which reverse-proxies each
service and gets a real certificate automatically.

**No domain purchase, no DNS change.** It uses
[sslip.io](https://sslip.io) "magic DNS": a hostname that embeds this
server's IP -- `rpc.157-245-252-221.sslip.io` -- already resolves to it,
today, with nothing to configure. That's a genuine public hostname, so
Let's Encrypt's HTTP-01 challenge (which Caddy runs itself, on port 80)
works exactly as it would for a bought domain. Verified from a machine with
no special DNS setup:

```
$ python3 -c "import socket; print(socket.gethostbyname('rpc.157-245-252-221.sslip.io'))"
157.245.252.221
```

After it runs, these are live alongside the existing plain endpoints (nothing
that already points at the old ones needs to change):

| Was | Becomes |
|---|---|
| `http://157.245.252.221:26657` (RPC) | `https://rpc.157-245-252-221.sslip.io` |
| `157.245.252.221:9090` (gRPC) | `grpc.157-245-252-221.sslip.io:443` |
| `http://157.245.252.221:8080` (faucet) | `https://faucet.157-245-252-221.sslip.io` |
| `http://157.245.252.221:8081` (explorer) | `https://explorer.157-245-252-221.sslip.io` |

gRPC needs its own line: `aetherd`/`agentmcp`/the explorer all speak
plaintext gRPC (h2c) to `:9090` today. Caddy terminates real TLS on the
public side and still speaks h2c to that same backend -- the
`transport http { versions h2c }` block in the Caddyfile is exactly Caddy's
documented way to front a plaintext gRPC server. A client reaches it with
ordinary TLS credentials:

```go
grpc.NewClient("grpc.157-245-252-221.sslip.io:443", grpc.WithTransportCredentials(credentials.NewTLS(nil)))
```

## Verifying it

```bash
curl -I https://rpc.157-245-252-221.sslip.io/status
curl -I https://faucet.157-245-252-221.sslip.io/
curl -I https://explorer.157-245-252-221.sslip.io/api/stats
```

Each should come back over a certificate issued to that hostname (`curl -v`
to see it), not a self-signed or default cert. `install.sh` checks these
itself as its last step.

For gRPC, the small Go program below (drop it anywhere, `go run` it from a
checkout so it can import the SDK) confirms a real call round-trips through
the proxy:

```go
package main

import (
	"context"
	"crypto/tls"
	"fmt"

	tmservice "github.com/cosmos/cosmos-sdk/client/grpc/cmtservice"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

func main() {
	conn, err := grpc.NewClient("grpc.157-245-252-221.sslip.io:443", grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{})))
	if err != nil {
		panic(err)
	}
	resp, err := tmservice.NewServiceClient(conn).GetLatestBlock(context.Background(), &tmservice.GetLatestBlockRequest{})
	if err != nil {
		panic(err)
	}
	fmt.Println("height:", resp.SdkBlock.Header.Height)
}
```

## After it's confirmed live

Tell Claude. The public-endpoint defaults in `agentmcp`, the explorer's
`/api/agents`, and the README's agent card all currently point at the plain
`http://157.245.252.221:...` addresses; switching them to the HTTPS
hostnames above is a small follow-up PR once these are verified working,
not a redeploy of anything already running. The plain ports can stay open
afterward (existing integrations keep working) or be firewalled to
localhost once everything's moved -- that's a separate decision, not part
of this change.

## What was and wasn't tested from here

This session's container can't reach the seed at all (`157.245.252.221` is
egress-blocked for it), so the install and cert issuance can only be run
and confirmed by whoever operates the seed. Before handing this off:

- `caddy validate` accepts the Caddyfile as written.
- Its generated config matches Caddy's own documented recipe for proxying a
  plaintext gRPC backend (`transport http { versions h2c }`).
- Against a local devnet, with Caddy fronting it exactly this way (real TLS,
  correct SNI-routed hostname, h2c to the backend): a live JSON-RPC call and
  a live gRPC call (`cmtservice.GetLatestBlock`, a real protobuf round trip)
  both succeeded and returned the chain's actual height.

Not tested: Let's Encrypt issuance itself, since that needs the challenge to
reach this exact public IP on port 80, which only the seed can do.
