#!/usr/bin/env bash
# TLS for the seed: installs Caddy and points it at this Caddyfile. Run once,
# on the seed, as root:
#
#   bash scripts/tls/install.sh
#
# Needs nothing you don't already have: no domain purchase, no DNS change.
# It uses sslip.io "magic DNS" -- rpc.157-245-252-221.sslip.io already
# resolves to this server, so Let's Encrypt's HTTP-01 challenge (which Caddy
# runs automatically on port 80) just works. See scripts/tls/Caddyfile for
# what's proxied and why.
#
# After this runs, the same services are reachable both ways -- nothing
# already using the plain HTTP/gRPC ports needs to change:
#   https://rpc.157-245-252-221.sslip.io       (was http://<ip>:26657)
#   https://grpc.157-245-252-221.sslip.io:443  (was <ip>:9090, plaintext gRPC)
#   https://faucet.157-245-252-221.sslip.io    (was http://<ip>:8080)
#   https://explorer.157-245-252-221.sslip.io  (was http://<ip>:8081)
#
# Tell Claude once this is confirmed live (curl -I each URL above): the
# explorer, agentmcp and the README default to these once they're real.
set -euo pipefail

if ! command -v caddy >/dev/null; then
  echo "==> installing Caddy from the distro repo"
  # Ubuntu (20.04+) and Debian (12+) both package Caddy directly -- confirmed
  # against this repo's own noble image (2.6.2, universe). Older distros
  # without a packaged Caddy: see https://caddyserver.com/docs/install for
  # their apt/yum repo, this script doesn't guess at that.
  apt-get update -y
  apt-get install -y caddy
fi

DIR=$(cd "$(dirname "$0")" && pwd)
install -m 0644 "$DIR/Caddyfile" /etc/caddy/Caddyfile
caddy validate --config /etc/caddy/Caddyfile --adapter caddyfile

systemctl enable caddy
systemctl restart caddy
sleep 2
systemctl --no-pager status caddy | head -10

echo
echo "==> checking each endpoint (a redirect or JSON/HTML body is success; a"
echo "    connection error or cert warning on the first request is normal --"
echo "    Let's Encrypt issuance takes a few seconds on first use)"
for h in rpc faucet explorer; do
  echo "-- https://$h.157-245-252-221.sslip.io --"
  curl -sS -o /dev/null -w "%{http_code}\n" --max-time 15 "https://$h.157-245-252-221.sslip.io/" || true
done
