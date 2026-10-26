#!/usr/bin/env bash
# Build aetherd on a workstation or in CI. Refuses to run where an aetherd
# validator is active: compiling on a validator has halted the chain.
# Requires Go 1.26.10, the version in go.mod. Writes GO_VERSION and
# SHA256SUMS next to the binaries for cutover.sh --prebuilt.
set -euo pipefail
HERE=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
# shellcheck source=lib.sh
source "$HERE/lib.sh"

usage() {
  cat <<EOF
Usage: build-aetherd.sh [--out DIR] [--no-windows]

Builds linux/amd64 aetherd with $REQUIRED_GO. Also builds aetherd.exe
unless --no-windows. Does not install or restart anything.
EOF
}

main() {
  local out="" windows=1
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --out) out=$2; shift 2 ;;
      --no-windows) windows=0; shift ;;
      -h|--help) usage; return 0 ;;
      *) echo "unknown argument: $1" >&2; usage >&2; return 2 ;;
    esac
  done
  local root
  root=$(git rev-parse --show-toplevel)
  [[ -n "$out" ]] || out="$root/dist/aetherd-cutover"
  refuse_build_on_validator
  local gv
  gv=$(go version)
  if ! go_version_ok "$gv" "$REQUIRED_GO"; then
    echo "need $REQUIRED_GO to build aetherd, have: $gv" >&2
    return 1
  fi
  mkdir -p "$out"
  printf '%s\n' "$REQUIRED_GO" >"$out/GO_VERSION"
  (
    cd "$root"
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -o "$out/aetherd" ./cmd/aetherd
    if [[ "$windows" -eq 1 ]]; then
      CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -o "$out/aetherd.exe" ./cmd/aetherd
    fi
  )
  (
    cd "$out"
    sha256sum aetherd >SHA256SUMS
    if [[ -f aetherd.exe ]]; then
      sha256sum aetherd.exe >>SHA256SUMS
    fi
  )
  echo "built $out with $REQUIRED_GO"
  cat "$out/SHA256SUMS"
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
  main "$@"
fi
