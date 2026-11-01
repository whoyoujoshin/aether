#!/usr/bin/env bash
# Builds aetherd so that every operator who builds the same commit gets
# the same file, byte for byte, and can compare sha256 sums with the
# others before a launch or an upgrade (docs/OPERATOR.md).
#
#   git checkout <tag or commit>
#   bash scripts/rc/build-aetherd.sh          # linux/amd64 by default
#   GOOS=windows bash scripts/rc/build-aetherd.sh
#
# What makes it reproducible:
#   - the Go toolchain is the one go.mod names, fetched by Go itself
#     (GOTOOLCHAIN), whatever Go the machine has;
#   - -trimpath drops the checkout's path and the module cache's;
#   - no cgo, so no system C compiler or libraries in the binary;
#   - no VCS stamp (-buildvcs=false) and an empty build ID, which would
#     otherwise differ with a dirty tree or another builder's cache;
#   - modules come from go.sum only (-mod=readonly).
# It refuses a tree with uncommitted changes to tracked files.
set -euo pipefail
cd "$(dirname "$0")/../.."

if [ -n "$(git status --porcelain --untracked-files=no)" ]; then
  echo "build-aetherd: the checkout has uncommitted changes; build from a clean tag or commit" >&2
  exit 1
fi

GO_VERSION=$(awk '$1 == "go" { print $2; exit }' go.mod)
export GOTOOLCHAIN="go${GO_VERSION}" CGO_ENABLED=0 GOFLAGS=-mod=readonly
export GOOS="${GOOS:-linux}" GOARCH="${GOARCH:-amd64}"
ext=""; [ "$GOOS" = windows ] && ext=.exe
out="dist/rc/aetherd-${GOOS}-${GOARCH}${ext}"
mkdir -p dist/rc

go build -trimpath -buildvcs=false -ldflags="-buildid=" -o "$out" ./cmd/aetherd

commit=$(git rev-parse --short=12 HEAD)
sum=$(sha256sum "$out" | cut -d' ' -f1)
echo "commit  $commit"
echo "go      $(go env GOVERSION)  ${GOOS}/${GOARCH}"
echo "binary  $out"
echo "sha256  $sum"
