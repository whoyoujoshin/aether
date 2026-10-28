#!/usr/bin/env bash
# Installs (or updates) the paid agent services on the seed:
# cmd/agentservices on 127.0.0.1:8500 and one cmd/paywall per service
# (scripts/agentservices/services.txt), each a systemd unit; the
# public x402 facilitator (cmd/facilitator) on 127.0.0.1:8403; and the
# job board's host (cmd/jobboard, docs/JOBS.md) on 127.0.0.1:8404, its
# texts in $JOBS_DIR. The
# paywalls also take standard x402 v2 exact payments. Run as root
# from the repo checkout, after creating the payee account (announce.sh
# prints it the first time):
#
#   cd /root/aether-src && PAY_TO=aether1... bash scripts/agentservices/install.sh
#
# It builds from the current checkout (or installs PREBUILT=<folder from
# scripts/prebuild.sh>: the seed is a validator, so it won't compile where
# aetherd runs unless ALLOW_BUILD_ON_VALIDATOR=yes), never touches aetherd,
# and checks each service answers its free /help through its paywall.
set -euo pipefail
REPO=${REPO:-$(pwd)}
PAY_TO=${PAY_TO:?set PAY_TO to the payee address (bash scripts/agentservices/announce.sh shows it)}
PUBLIC=${PUBLIC:-https://explorer.157-245-252-221.sslip.io/svc}
# Once scripts/querynode/point.sh has moved the services to the query node
# (docs/QUERY-NODE.md), redeploys keep them there.
QUERY_ENV=${QUERY_ENV:-/root/aether-query.env}
[ -r "$QUERY_ENV" ] && . "$QUERY_ENV"
GRPC=${GRPC:-${QUERY_GRPC:-localhost:9090}}
CHAIN_ID=${CHAIN_ID:-aether-testnet-1}
JOBS_DIR=${JOBS_DIR:-/root/aether-jobs}
USDC="--usdc-path transfer/channel-2 --usdc-base-denom erc20:0x0C382e685bbeeFE5d3d9C29e29E341fEE8E84C5d --usdc-issuer Injective"
LIST="$REPO/scripts/agentservices/services.txt"
[ -f "$LIST" ] || { echo "run from the repo checkout (no $LIST)" >&2; exit 1; }
# The payments benchmark's decoys, once decoys.sh has made their account:
# served by agentservices and behind their own paywalls, never announced.
DECOYS="$REPO/scripts/agentservices/decoys.txt"
DECOY=$(cat "${DECOY_KEYDIR:-/root/aether-decoy-key}/address" 2>/dev/null || true)
# every paywall: name|port|price|title|description|payee
ALL=$(mktemp)
trap 'rm -f "$ALL"' EXIT
grep -v '^#' "$LIST" | grep . | sed "s/\$/|$PAY_TO/" > "$ALL"
# The paid LLM inference service (docs/INFERENCE.md), only once its
# OpenRouter key is on this server; INFERENCE_FLAGS adds agentservices
# --inference-* flags (limits, model).
INFERENCE_KEY_FILE=${INFERENCE_KEY_FILE:-/root/aether-inference/openrouter-key}
INFERENCE=""
if [ -s "$INFERENCE_KEY_FILE" ]; then
  grep -v '^#' "$REPO/scripts/agentservices/inference.txt" | grep . | sed "s/\$/|$PAY_TO/" >> "$ALL"
  INFERENCE="--inference-key-file $INFERENCE_KEY_FILE${INFERENCE_FLAGS:+ $INFERENCE_FLAGS}"
  echo "== LLM inference, with the key in $INFERENCE_KEY_FILE"
fi
if [ -n "$DECOY" ] && [ -f "$DECOYS" ]; then
  grep -v '^#' "$DECOYS" | grep . | sed "s/{{decoy}}/$DECOY/g; s/\$/|$DECOY/" >> "$ALL"
  echo "== Decoys for the benchmark, naming $DECOY"
fi

. "$(dirname "$0")/../lib/prebuilt.sh"
prebuilt_check
no_build_on_validator
[ -n "${PREBUILT:-}" ] || echo "== Building from $(git -C "$REPO" log -1 --format='%h %s')"
place_bin agentservices /root/agentservices.new ./cmd/agentservices
place_bin paywall /root/paywall.new ./cmd/paywall
place_bin facilitator /root/facilitator.new ./cmd/facilitator
place_bin jobboard /root/jobboard.new ./cmd/jobboard
mv /root/agentservices.new /root/agentservices
mv /root/paywall.new /root/paywall
mv /root/facilitator.new /root/facilitator
mv /root/jobboard.new /root/jobboard

echo "== Units"
cat > /etc/systemd/system/aether-agentservices.service <<UNIT
[Unit]
Description=Aether paid agent services (upstream of the paywalls)
After=network-online.target aetherd.service

[Service]
ExecStart=/root/agentservices --listen 127.0.0.1:8500 --grpc $GRPC $USDC${DECOY:+ --decoy-address $DECOY}${INFERENCE:+ $INFERENCE}
Environment=HOME=/root
Restart=always
RestartSec=2
NoNewPrivileges=true

[Install]
WantedBy=multi-user.target
UNIT

cat > /etc/systemd/system/aether-facilitator.service <<UNIT
[Unit]
Description=Aether x402 facilitator (exact on cosmos:$CHAIN_ID; holds no key)
After=network-online.target

[Service]
ExecStart=/root/facilitator --listen 127.0.0.1:8403 --grpc $GRPC --chain-id $CHAIN_ID
Environment=HOME=/root
Restart=always
RestartSec=2
NoNewPrivileges=true

[Install]
WantedBy=multi-user.target
UNIT

# The seed is short of memory (CLAUDE.md, "Seed out of memory"): the
# host is capped well below what it needs.
cat > /etc/systemd/system/aether-jobboard.service <<UNIT
[Unit]
Description=Aether job board host (stores job texts named on chain; holds no key)
After=network-online.target

[Service]
ExecStart=/root/jobboard --listen 127.0.0.1:8404 --grpc $GRPC --dir $JOBS_DIR
Environment=HOME=/root
Restart=always
RestartSec=2
NoNewPrivileges=true
MemoryMax=256M

[Install]
WantedBy=multi-user.target
UNIT

units=(aether-agentservices aether-facilitator aether-jobboard)
while IFS='|' read -r name port price title desc payee; do
  case "$name" in ''|'#'*) continue;; esac
  unit=aether-paywall-$name
  units+=("$unit")
  cat > /etc/systemd/system/$unit.service <<UNIT
[Unit]
Description=Aether paywall: $title
After=network-online.target aether-agentservices.service

[Service]
ExecStart=/root/paywall --listen 127.0.0.1:$port --upstream http://127.0.0.1:8500/$name --pay-to $payee --price "$price" --grpc $GRPC --chain-id $CHAIN_ID --name "$title" --description "$desc" --free /help --public-url $PUBLIC/$name $USDC
Environment=HOME=/root
Restart=always
RestartSec=2
NoNewPrivileges=true

[Install]
WantedBy=multi-user.target
UNIT
done < "$ALL"

systemctl daemon-reload
for u in "${units[@]}"; do
  systemctl enable "$u" >/dev/null 2>&1
  systemctl restart "$u"
done

echo "== Checks (the facilitator; each paywall's free /help, its 402 with the x402 v2 header, its manifest)"
# curl -w prints 000 when nothing answers; "|| true" keeps set -e from
# ending the script there, so every service gets a line.
code() { curl -s -o /dev/null -w '%{http_code}' --max-time 5 "$1" || true; }
hdr() { curl -s -o /dev/null -D - --max-time 5 "$1" | grep -ci '^payment-required:' || true; }
fail=0
for _ in $(seq 1 10); do [ "$(code http://127.0.0.1:8403/supported)" = 200 ] && break; sleep 1; done
printf '%-8s supported %s  healthz %s\n' facilitator "$(code http://127.0.0.1:8403/supported)" "$(code http://127.0.0.1:8403/healthz)"
[ "$(code http://127.0.0.1:8403/healthz)" = 200 ] || fail=1
for _ in $(seq 1 10); do [ "$(code http://127.0.0.1:8404/jobs/healthz)" = 200 ] && break; sleep 1; done
printf '%-8s healthz %s  board %s\n' jobboard "$(code http://127.0.0.1:8404/jobs/healthz)" "$(code http://127.0.0.1:8404/jobs)"
[ "$(code http://127.0.0.1:8404/jobs/healthz)" = 200 ] || fail=1
while IFS='|' read -r name port price title desc payee; do
  case "$name" in ''|'#'*) continue;; esac
  for _ in $(seq 1 10); do [ "$(code "http://127.0.0.1:$port/help")" = 200 ] && break; sleep 1; done
  help=$(code "http://127.0.0.1:$port/help")
  paid=$(code "http://127.0.0.1:$port/")
  man=$(code "http://127.0.0.1:$port/.well-known/x402")
  v2=$(hdr "http://127.0.0.1:$port/")
  printf '%-8s help %s  unpaid %s  v2 header %s  manifest %s\n' "$name" "$help" "$paid" "$v2" "$man"
  [ "$help" = 200 ] && [ "$paid" = 402 ] && [ "$v2" = 1 ] && [ "$man" = 200 ] || fail=1
done < "$ALL"
if [ $fail = 1 ]; then
  echo "Some checks failed (000: not answering). Why: journalctl -u aether-paywall-<name> -n 5 --no-pager" >&2
  exit 1
fi
echo "All answer: the facilitator and the job board; each service's help 200, unpaid 402 with the v2 header, manifest 200."
