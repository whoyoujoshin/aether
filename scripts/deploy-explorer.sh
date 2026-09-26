#!/usr/bin/env bash
# Redeploy the block explorer on the server that runs it (the seed),
# from origin/main. Run it there as root:
#
#   bash scripts/deploy-explorer.sh
#
# It builds the frontend and the explorer binary, keeps the running
# ones as backups, restarts the service and checks it answers. If the
# check fails it puts the backups back and restarts again. It never
# touches aetherd.
#
# Defaults match the seed's aether-explorer.service; override with
# environment variables if yours differ:
#   REPO=/root/aether  BIN=/root/aether-explorer  SERVICE=aether-explorer  PORT=8081
set -euo pipefail

REPO=${REPO:-/root/aether}
BIN=${BIN:-/root/aether-explorer}
SERVICE=${SERVICE:-aether-explorer}
PORT=${PORT:-8081}
DIST=$REPO/explorer-web/dist
STAMP=$(date +%Y%m%d-%H%M%S)

say() { printf '\n== %s\n' "$*"; }
die() { printf '\nERROR: %s\n' "$*" >&2; exit 1; }

say "Checking prerequisites"
command -v go >/dev/null || die "go is not installed"
command -v npm >/dev/null || die "npm is not installed"
[ -d "$REPO/.git" ] || die "$REPO is not a git checkout (set REPO=...)"
systemctl cat "$SERVICE" >/dev/null 2>&1 || die "no systemd unit named $SERVICE (set SERVICE=...)"
systemctl cat "$SERVICE" | grep -q -- "$BIN" || die "$SERVICE doesn't run $BIN (set BIN=...)"
[ -z "$(git -C "$REPO" status --porcelain --untracked-files=no)" ] \
  || die "$REPO has local changes; commit, stash or discard them first"

say "Updating $REPO to origin/main"
git -C "$REPO" fetch origin main
git -C "$REPO" checkout --quiet --detach origin/main
echo "now at $(git -C "$REPO" log -1 --format='%h %s')"

# A build failure stops here, before anything running is touched.
say "Building (the running explorer keeps serving meanwhile)"
go -C "$REPO" build -o "$BIN.new" ./cmd/explorer
(cd "$REPO/explorer-web" && npm ci --no-audit --no-fund && npm run build -- --outDir dist.new --emptyOutDir)
[ -f "$REPO/explorer-web/dist.new/index.html" ] || die "frontend build produced no index.html"

say "Swapping in the new build (backups: $BIN.bak-$STAMP, $DIST.bak-$STAMP)"
[ -e "$BIN" ] && cp -a "$BIN" "$BIN.bak-$STAMP"
[ -d "$DIST" ] && mv "$DIST" "$DIST.bak-$STAMP"
mv "$REPO/explorer-web/dist.new" "$DIST"
mv "$BIN.new" "$BIN"
systemctl restart "$SERVICE"

check() {
  for _ in $(seq 1 15); do
    sleep 2
    if curl -fsS "http://127.0.0.1:$PORT/api/stats" >/dev/null 2>&1 \
      && curl -fsS "http://127.0.0.1:$PORT/services" | grep -q '<div id="root">'; then
      return 0
    fi
  done
  return 1
}

if check; then
  say "Explorer is up on :$PORT"
  curl -fsS "http://127.0.0.1:$PORT/api/stats"; echo
  echo "Backups kept at $BIN.bak-$STAMP and $DIST.bak-$STAMP; delete them once you're happy."
  exit 0
fi

say "The new explorer didn't answer; rolling back"
journalctl -u "$SERVICE" -n 30 --no-pager || true
[ -e "$BIN.bak-$STAMP" ] && cp -a "$BIN.bak-$STAMP" "$BIN"
if [ -d "$DIST.bak-$STAMP" ]; then rm -rf "$DIST" && mv "$DIST.bak-$STAMP" "$DIST"; fi
systemctl restart "$SERVICE"
if check; then die "rolled back to the previous explorer, which is serving again; see the log above"; fi
die "rolled back, but the explorer still isn't answering; check: systemctl status $SERVICE"
