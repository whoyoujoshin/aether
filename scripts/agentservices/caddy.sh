#!/usr/bin/env bash
# Routes https://explorer.157-245-252-221.sslip.io/svc/<name> to each
# service's paywall (scripts/agentservices/services.txt), and /facilitator
# to the x402 facilitator (cmd/facilitator on 127.0.0.1:8403), by adding
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
  done < "$LIST"
  echo "	@x402_facilitator path /facilitator /facilitator/*"
  echo "	handle @x402_facilitator {"
  echo "		uri strip_prefix /facilitator"
  echo "		reverse_proxy 127.0.0.1:8403"
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
echo "Caddy reloaded with the /svc and /facilitator routes."
