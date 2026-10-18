# Sourced by the seed's deploy scripts: install prebuilt binaries
# (PREBUILT=<folder from scripts/prebuild.sh>) instead of compiling, and
# refuse to compile on a validator unless told to.

# prebuilt_check verifies $PREBUILT's SHA256SUMS and prints its commit.
prebuilt_check() {
  [ -n "${PREBUILT:-}" ] || return 0
  [ -f "$PREBUILT/SHA256SUMS" ] && [ -f "$PREBUILT/COMMIT" ] || { echo "PREBUILT=$PREBUILT: no SHA256SUMS/COMMIT (make it with scripts/prebuild.sh)" >&2; exit 1; }
  (cd "$PREBUILT" && sha256sum -c --quiet SHA256SUMS) || { echo "PREBUILT=$PREBUILT: checksums don't match" >&2; exit 1; }
  echo "== Prebuilt from $(cat "$PREBUILT/COMMIT") (checksums match)"
}

# no_build_on_validator stops before any compile on a host running a
# validator, unless PREBUILT is set or ALLOW_BUILD_ON_VALIDATOR=yes.
no_build_on_validator() {
  [ -n "${PREBUILT:-}" ] && return 0
  [ "${ALLOW_BUILD_ON_VALIDATOR:-}" = yes ] && return 0
  if systemctl is-active --quiet aetherd 2>/dev/null; then
    echo "aetherd runs here: don't compile on a validator (a build halted the chain on 2026-10-17)." >&2
    echo "Build on a workstation with scripts/prebuild.sh and pass PREBUILT=<folder>," >&2
    echo "or set ALLOW_BUILD_ON_VALIDATOR=yes to compile here anyway." >&2
    exit 1
  fi
}

# place_bin <name> <dest> <go package>: installs $PREBUILT/<name> at
# <dest>, or builds <go package> there from $REPO.
place_bin() {
  if [ -n "${PREBUILT:-}" ]; then
    [ -f "$PREBUILT/$1" ] || { echo "PREBUILT=$PREBUILT has no $1" >&2; exit 1; }
    install -m 755 "$PREBUILT/$1" "$2"
  else
    (cd "$REPO" && go build -o "$2" "$3")
  fi
}
