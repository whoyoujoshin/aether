#!/usr/bin/env bash
# Build agentmcp's MCPB bundle (one file for Windows, macOS and Linux) and
# the MCP Registry's server.json for it. The release workflow runs this for
# every tag; to try it locally:
#
#   bash scripts/package-mcpb.sh v0.2.1-testnet dist/mcpb
#
# Writes <out>/aether-wallet.mcpb and <out>/server.json. Needs Go and npx.
set -euo pipefail

TAG=${1:?usage: package-mcpb.sh <tag, e.g. v0.2.1-testnet> <out dir>}
OUT=${2:?usage: package-mcpb.sh <tag> <out dir>}
VERSION=${TAG#v}
URL="https://github.com/whoyoujoshin/aether/releases/download/$TAG/aether-wallet.mcpb"
MCPB_CLI=@anthropic-ai/mcpb@2.1.2

ROOT=$(cd "$(dirname "$0")/.." && pwd)
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT
B="$WORK/bundle"
mkdir -p "$B/server/darwin" "$OUT"

build() { # goos goarch output
  (cd "$ROOT" && CGO_ENABLED=0 GOOS=$1 GOARCH=$2 go build -trimpath -ldflags "-s -w" -o "$3" ./cmd/agentmcp)
}
build linux amd64 "$B/server/agentmcp"
build windows amd64 "$B/server/agentmcp.exe"
build darwin amd64 "$B/server/darwin/amd64/agentmcp"
build darwin arm64 "$B/server/darwin/arm64/agentmcp"
install -m 0755 "$ROOT/scripts/mcpb/agentmcp.sh" "$B/server/darwin/agentmcp.sh"
cp "$ROOT/scripts/mcpb/icon.png" "$B/icon.png"

(cd "$ROOT" && go run ./cmd/agentmcp mcpb-manifest --version "$VERSION") > "$B/manifest.json"
npx --yes "$MCPB_CLI" validate "$B/manifest.json"
npx --yes "$MCPB_CLI" pack "$B" "$OUT/aether-wallet.mcpb"

SHA=$(sha256sum "$OUT/aether-wallet.mcpb" | cut -d' ' -f1)
(cd "$ROOT" && go run ./cmd/agentmcp server-json --version "$VERSION" --url "$URL" --sha256 "$SHA") > "$OUT/server.json"
echo "aether-wallet.mcpb sha256 $SHA"
