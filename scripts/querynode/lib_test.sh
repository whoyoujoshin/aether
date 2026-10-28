#!/usr/bin/env bash
# Tests for the query node helpers and a run of point.sh against fake
# units, a fake Caddyfile and stubbed curl/systemctl. No network, no
# aetherd, nothing outside a temp directory.
set -euo pipefail
HERE=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
# shellcheck source=lib.sh
source "$HERE/lib.sh"

fail() { echo "FAIL: $*" >&2; exit 1; }
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"; [[ -n "${listener:-}" ]] && kill "$listener" 2>/dev/null || true' EXIT

# --- toml_set / toml_get ---
cat >"$tmp/c.toml" <<'EOF'
moniker = "node"
laddr = "top-level, not the rpc one"

[rpc]
laddr = "tcp://127.0.0.1:26657"
cors_allowed_origins = []

[p2p]
laddr = "tcp://0.0.0.0:26656"
persistent_peers = ""
EOF
toml_set "$tmp/c.toml" rpc laddr '"tcp://10.0.0.5:26657"'
[[ $(toml_get "$tmp/c.toml" rpc laddr) == '"tcp://10.0.0.5:26657"' ]] || fail "rpc laddr"
[[ $(toml_get "$tmp/c.toml" p2p laddr) == '"tcp://0.0.0.0:26656"' ]] || fail "p2p laddr touched"
[[ $(toml_get "$tmp/c.toml" "" laddr) == '"top-level, not the rpc one"' ]] || fail "top-level laddr touched"
toml_set "$tmp/c.toml" "" moniker '"aether-query"'
[[ $(toml_get "$tmp/c.toml" "" moniker) == '"aether-query"' ]] || fail "moniker"
toml_set "$tmp/c.toml" rpc cors_allowed_origins '["*"]'
[[ $(toml_get "$tmp/c.toml" rpc cors_allowed_origins) == '["*"]' ]] || fail "cors"
before=$(cat "$tmp/c.toml")
if toml_set "$tmp/c.toml" p2p nosuchkey 1; then fail "a missing key must fail"; fi
[[ $(cat "$tmp/c.toml") == "$before" ]] || fail "a failed toml_set changed the file"

# --- validators of input ---
valid_hostport 10.0.0.5:9090 || fail "host:port"
valid_hostport query.internal:9090 || fail "hostname:port"
if valid_hostport "10.0.0.5:9090 --evil"; then fail "space accepted"; fi
if valid_hostport "10.0.0.5"; then fail "no port accepted"; fi
if valid_hostport 'a#b:1'; then fail "# accepted"; fi
valid_rpc http://10.0.0.5:26657 || fail "rpc url"
if valid_rpc https://10.0.0.5:26657; then fail "https rpc accepted"; fi
if valid_rpc "http://10.0.0.5:26657/x"; then fail "path accepted"; fi

# --- which units move ---
for u in aether-explorer aether-agentmcp aether-sandbox aether-agentservices aether-facilitator aether-jobboard aether-paywall-hello; do
  qn_target "$u" || fail "$u should move"
done
for u in aetherd aether-powminer aether-faucet aether-minerwatch aether-memwatch aether-ibcwatch aether-chainwatch caddy; do
  if qn_target "$u"; then fail "$u must stay"; fi
done
qn_takes_rpc aether-explorer && qn_takes_rpc aether-agentmcp || fail "explorer and agentmcp take --rpc"
if qn_takes_rpc aether-sandbox || qn_takes_rpc aether-paywall-hello; then fail "--rpc on a binary without it"; fi

# --- qn_rewrite / qn_restore ---
G=10.0.0.5:9090 R=http://10.0.0.5:26657
unit() { printf '[Service]\nExecStart=%s\nRestart=always\n' "$1" >"$tmp/u"; }
execstart() { sed -n 's/^ExecStart=//p' "$tmp/u"; }

unit '/root/paywall --listen 127.0.0.1:8411 --grpc localhost:9090 --chain-id aether-testnet-1'
qn_rewrite "$tmp/u" no "$G" "$R"
[[ $(execstart) == "/root/paywall --listen 127.0.0.1:8411 --grpc $G --chain-id aether-testnet-1" ]] || fail "paywall: $(execstart)"
cp "$tmp/u" "$tmp/once"; qn_rewrite "$tmp/u" no "$G" "$R"
cmp -s "$tmp/u" "$tmp/once" || fail "rewrite not idempotent"
qn_restore "$tmp/u" "$G" "$R"
[[ $(execstart) == "/root/paywall --listen 127.0.0.1:8411 --grpc localhost:9090 --chain-id aether-testnet-1" ]] || fail "paywall back: $(execstart)"

unit '/root/agentmcp --http 127.0.0.1:8090 --grpc=127.0.0.1:9090 --rpc "http://127.0.0.1:26657" --usdc-path transfer/channel-2'
qn_rewrite "$tmp/u" yes "$G" "$R"
[[ $(execstart) == "/root/agentmcp --http 127.0.0.1:8090 --grpc=$G --rpc $R --usdc-path transfer/channel-2" ]] || fail "agentmcp: $(execstart)"

# The explorer on its defaults: both flags added after the binary.
unit '/root/aether-explorer --listen :8081 --public-mcp https://x/mcp'
qn_rewrite "$tmp/u" yes "$G" "$R"
[[ $(execstart) == "/root/aether-explorer --rpc $R --grpc $G --listen :8081 --public-mcp https://x/mcp" ]] || fail "explorer: $(execstart)"
qn_restore "$tmp/u" "$G" "$R"
[[ $(execstart) == "/root/aether-explorer --rpc http://localhost:26657 --grpc localhost:9090 --listen :8081 --public-mcp https://x/mcp" ]] || fail "explorer back: $(execstart)"

# The test wallet's --rpc "" (poll) stays; a remote --grpc is left alone.
unit '/root/agentmcp-sandbox --sandbox-http 127.0.0.1:8092 --grpc localhost:9090 --rpc ""'
qn_rewrite "$tmp/u" no "$G" "$R"
[[ $(execstart) == "/root/agentmcp-sandbox --sandbox-http 127.0.0.1:8092 --grpc $G --rpc \"\"" ]] || fail "sandbox: $(execstart)"
unit '/root/jobboard --grpc grpc.example:443'
qn_rewrite "$tmp/u" no "$G" "$R"
[[ $(execstart) == "/root/jobboard --grpc grpc.example:443" ]] || fail "remote grpc changed: $(execstart)"
unit '/root/jobboard --grpc localhost:90900'
qn_rewrite "$tmp/u" no "$G" "$R"
[[ $(execstart) == "/root/jobboard --grpc localhost:90900" ]] || fail "port prefix matched: $(execstart)"

# A drop-in that clears ExecStart then sets it: only the real one changes.
printf '[Service]\nExecStart=\nExecStart=/root/facilitator --listen 127.0.0.1:8403 --grpc localhost:9090\n' >"$tmp/u"
qn_rewrite "$tmp/u" no "$G" "$R"
[[ $(sed -n 2p "$tmp/u") == "ExecStart=" ]] || fail "empty ExecStart touched"
[[ $(sed -n 3p "$tmp/u") == "ExecStart=/root/facilitator --listen 127.0.0.1:8403 --grpc $G" ]] || fail "drop-in: $(sed -n 3p "$tmp/u")"
# A drop-in without ExecStart is left alone.
printf '[Service]\nEnvironment=HOME=/root\n' >"$tmp/u"; cp "$tmp/u" "$tmp/once"
qn_rewrite "$tmp/u" no "$G" "$R"
cmp -s "$tmp/u" "$tmp/once" || fail "drop-in without ExecStart changed"

# --- caddy_upstream ---
cat >"$tmp/Caddyfile" <<'EOF'
rpc.example {
	reverse_proxy 127.0.0.1:26657
}
grpc.example {
	reverse_proxy localhost:9090 {
		transport http {
			versions h2c
		}
	}
}
explorer.example {
	handle /mcp* {
		reverse_proxy 127.0.0.1:8090
	}
}
EOF
cp "$tmp/Caddyfile" "$tmp/Caddyfile.orig"
[[ $(caddy_upstream "$tmp/Caddyfile" '(127\.0\.0\.1|localhost):26657' 10.0.0.5:26657) == 1 ]] || fail "caddy rpc count"
[[ $(caddy_upstream "$tmp/Caddyfile" '(127\.0\.0\.1|localhost):9090' 10.0.0.5:9090) == 1 ]] || fail "caddy grpc count"
grep -q '^	reverse_proxy 10.0.0.5:26657$' "$tmp/Caddyfile" || fail "caddy rpc"
grep -q '^	reverse_proxy 10.0.0.5:9090 {$' "$tmp/Caddyfile" || fail "caddy grpc keeps its block"
grep -q 'reverse_proxy 127.0.0.1:8090$' "$tmp/Caddyfile" || fail "caddy /mcp touched"

# --- point.sh end to end, with stubs ---
stub=$tmp/bin units=$tmp/units
mkdir -p "$stub" "$units/aether-faucet.service.d" "$units/aether-facilitator.service.d"
LOG=$tmp/systemctl.log
cat >"$stub/systemctl" <<EOF
#!/usr/bin/env bash
echo "\$*" >>"$LOG"
exit 0
EOF
cat >"$stub/journalctl" <<'EOF'
#!/usr/bin/env bash
exit 0
EOF
# curl: /status answers by host (query node 10.0.0.5, local 127.0.0.1);
# health checks get 200; the MCP call gets a result.
cat >"$stub/curl" <<EOF
#!/usr/bin/env bash
args="\$*"
case "\$args" in
  *10.0.0.5:26657/status*) cat "$tmp/qstatus" ;;
  *127.0.0.1:26657/status*) cat "$tmp/lstatus" ;;
  *"/mcp"*) echo '{"jsonrpc":"2.0","id":1,"result":{"content":[]}}' ;;
  *"%{http_code}"*) printf 200 ;;
  *) exit 7 ;;
esac
EOF
cat >"$stub/caddy" <<'EOF'
#!/usr/bin/env bash
exit 0
EOF
chmod +x "$stub"/*
status_doc() { printf '{"result":{"node_info":{"id":"%s","network":"%s"},"sync_info":{"latest_block_height":"%s","catching_up":%s},"validator_info":{"voting_power":"%s"}}}' "$@"; }
status_doc aaaa aether-testnet-1 1000 false 0 >"$tmp/qstatus"
status_doc bbbb aether-testnet-1 1001 false 23943212 >"$tmp/lstatus"

printf '[Service]\nExecStart=/root/aether-explorer --listen :8081\n' >"$units/aether-explorer.service"
printf '[Service]\nExecStart=/root/agentmcp --http 127.0.0.1:8090 --grpc localhost:9090\n' >"$units/aether-agentmcp.service"
printf '[Service]\nExecStart=/root/paywall --listen 127.0.0.1:8411 --grpc localhost:9090\n' >"$units/aether-paywall-hello.service"
printf '[Service]\nExecStart=/root/facilitator --listen 127.0.0.1:8403 --grpc localhost:9090\n' >"$units/aether-facilitator.service"
printf '[Service]\nExecStart=\nExecStart=/root/facilitator --listen 127.0.0.1:8403 --grpc localhost:9090 --chain-id x\n' >"$units/aether-facilitator.service.d/override.conf"
printf '[Service]\nExecStart=/root/faucet --grpc localhost:9090\n' >"$units/aether-faucet.service"
printf '[Service]\nExecStart=\nExecStart=/root/faucet --grpc localhost:9090 --gas-prices 0uaeth\n' >"$units/aether-faucet.service.d/gas-price.conf"
cp -a "$units" "$tmp/units.orig"
cp "$tmp/Caddyfile.orig" "$tmp/Caddyfile"

# A TCP listener for the gRPC port probe.
python3 -c 'import socket,sys,time
s=socket.socket(); s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1); s.bind(("127.0.0.1",0)); s.listen(8)
open(sys.argv[1],"w").write(str(s.getsockname()[1])); time.sleep(120)' "$tmp/port" &
listener=$!
for _ in $(seq 1 50); do [[ -s "$tmp/port" ]] && break; sleep 0.1; done
GPORT=$(cat "$tmp/port")
[[ -n "$GPORT" ]] || fail "listener didn't start"

point() {
  PATH="$stub:$PATH" QN_TEST=1 QN_SLEEP=0 UNIT_DIR="$units" QUERY_ENV="$tmp/query.env" CADDYFILE="$tmp/Caddyfile" \
    QUERY_GRPC="127.0.0.1:$GPORT" QUERY_RPC=http://10.0.0.5:26657 bash "$HERE/point.sh" "$@"
}

# Dry run: diffs, nothing written.
out=$(point services --dry-run 2>&1) || fail "dry run: $out"
grep -q -- "+ExecStart=/root/aether-explorer --rpc http://10.0.0.5:26657 --grpc 127.0.0.1:$GPORT --listen :8081" <<<"$out" || fail "dry run diff: $out"
diff -r "$tmp/units.orig" "$units" >/dev/null || fail "dry run changed a unit"
[[ ! -e "$tmp/query.env" ]] || fail "dry run wrote the env file"
[[ ! -s "$LOG" ]] || fail "dry run ran systemctl: $(cat "$LOG")"

# Refusals before anything changes.
status_doc aaaa aether-testnet-1 1000 false 1000000 >"$tmp/qstatus"
if out=$(point services 2>&1); then fail "a query node with power was accepted"; fi
grep -q "must not hold a validator key" <<<"$out" || fail "power refusal: $out"
status_doc aaaa aether-testnet-1 900 false 0 >"$tmp/qstatus"
if out=$(point services 2>&1); then fail "a lagging query node was accepted"; fi
status_doc aaaa other-chain 1000 false 0 >"$tmp/qstatus"
if out=$(point services 2>&1); then fail "another chain was accepted"; fi
status_doc bbbb aether-testnet-1 1000 false 0 >"$tmp/qstatus"
if out=$(point services 2>&1); then fail "the local node itself was accepted"; fi
status_doc aaaa aether-testnet-1 1000 true 0 >"$tmp/qstatus"
if out=$(point services 2>&1); then fail "a catching-up node was accepted"; fi
diff -r "$tmp/units.orig" "$units" >/dev/null || fail "a refusal changed a unit"

# The real run.
status_doc aaaa aether-testnet-1 1000 false 0 >"$tmp/qstatus"
out=$(point services 2>&1) || fail "services: $out"
G2=127.0.0.1:$GPORT
grep -qx "ExecStart=/root/aether-explorer --rpc http://10.0.0.5:26657 --grpc $G2 --listen :8081" "$units/aether-explorer.service" || fail "explorer unit"
grep -qx "ExecStart=/root/agentmcp --rpc http://10.0.0.5:26657 --http 127.0.0.1:8090 --grpc $G2" "$units/aether-agentmcp.service" || fail "agentmcp unit: $(cat "$units/aether-agentmcp.service")"
grep -qx "ExecStart=/root/paywall --listen 127.0.0.1:8411 --grpc $G2" "$units/aether-paywall-hello.service" || fail "paywall unit"
grep -qx "ExecStart=/root/facilitator --listen 127.0.0.1:8403 --grpc $G2 --chain-id x" "$units/aether-facilitator.service.d/override.conf" || fail "facilitator drop-in"
diff -r "$tmp/units.orig/aether-faucet.service.d" "$units/aether-faucet.service.d" >/dev/null || fail "the faucet must stay"
cmp -s "$tmp/units.orig/aether-faucet.service" "$units/aether-faucet.service" || fail "the faucet must stay"
ls "$units"/aether-explorer.service.bak-query-* >/dev/null 2>&1 || fail "no backup"
grep -qx "QUERY_GRPC=$G2" "$tmp/query.env" || fail "env file"
grep -q "restart aether-jobboard\|restart aether-explorer" "$LOG" || fail "no restarts: $(cat "$LOG")"
[[ $(grep -c '^restart ' "$LOG") == 4 ]] || fail "expected 4 restarts: $(cat "$LOG")"
if grep -q 'faucet\|aetherd' "$LOG"; then fail "touched the faucet or aetherd: $(cat "$LOG")"; fi

# Again: nothing to change, nothing restarted.
: >"$LOG"
out=$(point services 2>&1) || fail "second run: $out"
[[ $(grep -c '^restart ' "$LOG" || true) == 0 ]] || fail "second run restarted: $(cat "$LOG")"

# Public hostnames, then back.
point public >/dev/null 2>&1 || fail "public"
grep -q "reverse_proxy 10.0.0.5:26657$" "$tmp/Caddyfile" && grep -q "reverse_proxy $G2 {" "$tmp/Caddyfile" || fail "caddy after public: $(cat "$tmp/Caddyfile")"
grep -q '^reload caddy' "$LOG" || fail "caddy not reloaded"
point back-public >/dev/null 2>&1 || fail "back-public"
grep -q "reverse_proxy 127.0.0.1:26657$" "$tmp/Caddyfile" && grep -q "reverse_proxy 127.0.0.1:9090 {" "$tmp/Caddyfile" || fail "caddy after back: $(cat "$tmp/Caddyfile")"

# Back: units name the local node again, the env file is set aside.
out=$(point back-services 2>&1) || fail "back-services: $out"
grep -qx "ExecStart=/root/paywall --listen 127.0.0.1:8411 --grpc localhost:9090" "$units/aether-paywall-hello.service" || fail "paywall back"
grep -qx "ExecStart=/root/aether-explorer --rpc http://localhost:26657 --grpc localhost:9090 --listen :8081" "$units/aether-explorer.service" || fail "explorer back"
[[ ! -e "$tmp/query.env" && -e "$tmp/query.env.off" ]] || fail "env file not set aside"

# A unit that fails its check is put back and the run stops.
for f in "$tmp/units.orig"/*.service; do cp "$f" "$units/"; done
cp "$tmp/units.orig/aether-facilitator.service.d/override.conf" "$units/aether-facilitator.service.d/"
cat >"$stub/curl" <<EOF
#!/usr/bin/env bash
case "\$*" in
  *10.0.0.5:26657/status*) cat "$tmp/qstatus" ;;
  *127.0.0.1:26657/status*) cat "$tmp/lstatus" ;;
  *8403/healthz*) printf 503 ;;
  *"%{http_code}"*) printf 200 ;;
  *) exit 7 ;;
esac
EOF
chmod +x "$stub/curl"
if out=$(point services 2>&1); then fail "a failing check was accepted"; fi
grep -q "aether-facilitator didn't answer" <<<"$out" || fail "failure message: $out"
cmp -s "$tmp/units.orig/aether-facilitator.service.d/override.conf" "$units/aether-facilitator.service.d/override.conf" || fail "facilitator not put back"
cmp -s "$tmp/units.orig/aether-paywall-hello.service" "$units/aether-paywall-hello.service" || fail "a unit after the failure was changed"

echo "querynode tests: ok"
