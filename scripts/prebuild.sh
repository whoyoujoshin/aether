#!/usr/bin/env bash
# Builds what the seed runs, on a workstation: never on a validator
# (CLAUDE.md, "Never build on a validator": compiling on sync3 halted the
# chain for 27 minutes on 2026-10-17). Static linux/amd64 binaries, the
# explorer's frontend, the commit and a SHA256SUMS file, in one folder:
#
#   bash scripts/prebuild.sh                 # from a clean checkout of what you deploy
#   scp -r dist/prebuilt-<commit> root@<seed>:/root/
#
# then on the seed, from /root/aether-src at the same commit:
#
#   PREBUILT=/root/prebuilt-<commit> bash scripts/agentservices/sandbox.sh
#   PREBUILT=/root/prebuilt-<commit> PAY_TO=... bash scripts/agentservices/install.sh
#   PREBUILT=/root/prebuilt-<commit> bash scripts/deploy-explorer.sh
#
# Each checks SHA256SUMS before installing anything. NO_WEB=1 skips the
# frontend (it needs npm); deploy-explorer.sh then refuses the folder.
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"
[ -z "$(git status --porcelain --untracked-files=no)" ] || { echo "local changes: build from a clean checkout of what you deploy" >&2; exit 1; }
COMMIT=$(git rev-parse --short HEAD)
OUT=${1:-dist/prebuilt-$COMMIT}
mkdir -p "$OUT"
echo "== Building $(git log -1 --format='%h %s') into $OUT"
for c in agentmcp agentservices paywall facilitator jobboard explorer aetherd chainwatch wallet faucet powminer; do
  CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -o "$OUT/$c" "./cmd/$c"
  echo "   $c"
done
if [ -z "${NO_WEB:-}" ]; then
  web=$(cd "$OUT" && pwd)/explorer-web-dist
  (cd explorer-web && npm ci --no-audit --no-fund && npm run build -- --outDir "$web" --emptyOutDir)
  [ -f "$web/index.html" ] || { echo "frontend build produced no index.html" >&2; exit 1; }
  echo "   explorer-web-dist"
fi
git rev-parse HEAD > "$OUT/COMMIT"
(cd "$OUT" && rm -f SHA256SUMS && find . -type f ! -name SHA256SUMS -print0 | sort -z | xargs -0 sha256sum > SHA256SUMS)
echo "== Done: $(wc -l < "$OUT/SHA256SUMS") files, commit $(cat "$OUT/COMMIT")"
echo "   scp -r $OUT root@<seed>:/root/"
