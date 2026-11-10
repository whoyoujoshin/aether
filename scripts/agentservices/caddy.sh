#!/usr/bin/env bash
# Routes https://explorer.157-245-252-221.sslip.io/svc/<name> to each
# service's paywall (scripts/agentservices/services.txt, and the benchmark's
# unlisted decoys in decoys.txt, and inference.txt once its key is here), /facilitator
# to the x402 facilitator (cmd/facilitator on 127.0.0.1:8403), and
# /sandbox/mcp to the hosted test wallet (sandbox.sh, 127.0.0.1:8092), and
# /a2a to the public read-only agentmcp's A2A endpoint (aether-agentmcp,
# 127.0.0.1:8090, the same process as /mcp; docs/A2A.md), and /jobs to
# the job board's host (install.sh, 127.0.0.1:8404; docs/JOBS.md), by adding
# one block per route to the explorer's site in /etc/caddy/Caddyfile, before
# its catch-all handle. Backs the file up first, validates, and puts the
# backup back if Caddy rejects the result. Safe to run again: it replaces
# its own blocks rather than adding them twice.
#
#   cd /root/aether-src && bash scripts/agentservices/caddy.sh
set -euo pipefail
REPO=${REPO:-$(pwd)}
CADDYFILE=${CADDYFILE:-/etc/caddy/Caddyfile}
LIST="$REPO/scripts/agentservices/services.txt"
[ -f "$LIST" ] || { echo "run from the repo checkout (no $LIST)" >&2; exit 1; }
INFERENCE_LIST=/dev/null
[ -s "${INFERENCE_KEY_FILE:-/root/aether-inference/openrouter-key}" ] && INFERENCE_LIST="$REPO/scripts/agentservices/inference.txt"
BACKUP="$CADDYFILE.bak-svc-$(date +%Y%m%d-%H%M%S)"
cp "$CADDYFILE" "$BACKUP"
echo "backup: $BACKUP"

block=$(mktemp)
{
  echo "	# BEGIN agentservices: paid agent services, one paywall each (scripts/agentservices)."
  while IFS='|' read -r name port _; do
    case "$name" in ''|'#'*) continue;; esac
    echo "	@svc_$name path /svc/$name /svc/$name/*"
    echo "	handle @svc_$name {"
    echo "		uri strip_prefix /svc/$name"
    echo "		reverse_proxy 127.0.0.1:$port"
    echo "	}"
  done < <(cat "$LIST" "$REPO/scripts/agentservices/decoys.txt" "$INFERENCE_LIST" 2>/dev/null)
  echo "	@x402_facilitator path /facilitator /facilitator/*"
  echo "	handle @x402_facilitator {"
  echo "		uri strip_prefix /facilitator"
  echo "		reverse_proxy 127.0.0.1:8403"
  echo "	}"
  echo "	@test_wallet path /sandbox/mcp /sandbox/mcp/*"
  echo "	handle @test_wallet {"
  echo "		uri strip_prefix /sandbox"
  echo "		reverse_proxy 127.0.0.1:8092"
  echo "	}"
  echo "	@a2a path /a2a /a2a/*"
  echo "	handle @a2a {"
  echo "		reverse_proxy 127.0.0.1:8090"
  echo "	}"
  echo "	@jobs path /jobs /jobs/*"
  echo "	handle @jobs {"
  echo "		request_body {"
  echo "			max_size 300KB"
  echo "		}"
  echo "		reverse_proxy 127.0.0.1:8404"
  echo "	}"
  echo "	# END agentservices"
} > "$block"

# Drop any earlier copy of the block, then insert it before the explorer
# site's catch-all "handle {" (the only bare one in the file).
python3 - "$CADDYFILE" "$block" <<'PY'
import re, sys
path, blockfile = sys.argv[1], sys.argv[2]
s = open(path).read()
block = open(blockfile).read()
s = re.sub(r"\t# BEGIN agentservices.*?# END agentservices\n", "", s, flags=re.S)
lines = s.split("\n")
site = next((i for i, l in enumerate(lines) if l.startswith("explorer.") and l.rstrip().endswith("{")), None)
if site is None:
    sys.exit("no explorer site block in the Caddyfile")
catch = next((i for i in range(site, len(lines)) if lines[i].strip() == "handle {"), None)
if catch is None:
    sys.exit("no catch-all 'handle {' in the explorer site block")
lines[catch:catch] = block.rstrip("\n").split("\n")
open(path, "w").write("\n".join(lines))
PY
rm -f "$block"

if ! caddy validate --config "$CADDYFILE" --adapter caddyfile >/dev/null 2>&1; then
  caddy validate --config "$CADDYFILE" --adapter caddyfile || true
  cp "$BACKUP" "$CADDYFILE"
  echo "Caddy rejected the new config; the backup is back in place and nothing was reloaded." >&2
  exit 1
fi
systemctl reload caddy
echo "Caddy reloaded with the /svc, /facilitator, /sandbox/mcp, /a2a and /jobs routes."
