#!/usr/bin/env bash
# Tests for keyring.sh with stubbed systemd-creds and aetherd.
set -euo pipefail
HERE=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
fail() { echo "FAIL: $*" >&2; exit 1; }
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
mkdir -p "$tmp/bin" "$tmp/creds"
cat >"$tmp/bin/systemd-creds" <<'STUB'
#!/usr/bin/env bash
# decrypt --name=keyring <file> -
[[ "$1" == decrypt && "$2" == --name=keyring && "$4" == - ]] || { echo "bad args: $*" >&2; exit 2; }
printf 'pass-from-%s' "$(basename "$3")"
STUB
cat >"$tmp/bin/aetherd" <<'STUB'
#!/usr/bin/env bash
stdin=""
[ -t 0 ] || stdin=$(cat)
echo "args=[$*] stdin=[$stdin]"
STUB
chmod +x "$tmp/bin"/*
export PATH="$tmp/bin:$PATH"

run() { (CREDSTORE="$tmp/creds" KEYRING_BACKEND="$1" && shift && source "$HERE/keyring.sh" && "$@"); }

# test: no unit lines, the test backend, nothing on stdin.
[[ -z "$(run test keyring_unit_lines aether-x)" ]] || fail "test backend has unit lines"
run test keyring_check aether-x || fail "test needs no credential"
out=$(run test keyring_keys aether-x aetherd show f -a </dev/null)
[[ "$out" == "args=[keys show f -a --keyring-backend test] stdin=[]" ]] || fail "test keys: $out"

# file: the credential must exist.
if run file keyring_check aether-x 2>/dev/null; then fail "file without a credential passed"; fi
touch "$tmp/creds/aether-x"
run file keyring_check aether-x || fail "file with its credential"
lines=$(run file keyring_unit_lines aether-x)
[[ "$lines" == "LoadCredentialEncrypted=keyring:$tmp/creds/aether-x
Environment=AETHER_KEYRING_PASSPHRASE_FILE=%d/keyring" ]] || fail "unit lines: $lines"
out=$(run file keyring_keys aether-x aetherd show f -a)
[[ "$out" == "args=[keys show f -a --keyring-backend file] stdin=[pass-from-aether-x]" ]] || fail "file keys: $out"

if run nope keyring_check aether-x 2>/dev/null; then fail "an unknown backend passed"; fi
echo "keyring tests: ok"
