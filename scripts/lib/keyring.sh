# Sourced by the seed's deploy scripts for a service whose key signs
# unattended (docs/SERVICE-KEYS.md). KEYRING_BACKEND=test (the default)
# keeps today's unencrypted keyring. KEYRING_BACKEND=file uses an encrypted
# keyring whose passphrase is a systemd encrypted credential,
# $CREDSTORE/<name> (systemd-creds encrypt), which only systemd decrypts,
# into the service's own credentials directory while it runs.

KEYRING_BACKEND=${KEYRING_BACKEND:-test}
CREDSTORE=${CREDSTORE:-/etc/credstore.encrypted}

# keyring_check <credential>: the backend is one we know, and with file the
# credential exists.
keyring_check() {
  case "$KEYRING_BACKEND" in
    test) ;;
    file)
      [ -f "$CREDSTORE/$1" ] || { echo "KEYRING_BACKEND=file: no $CREDSTORE/$1; make it first (docs/SERVICE-KEYS.md)" >&2; exit 1; }
      command -v systemd-creds >/dev/null || { echo "KEYRING_BACKEND=file needs systemd-creds (systemd 250 or later)" >&2; exit 1; }
      ;;
    *) echo "KEYRING_BACKEND must be test or file, not $KEYRING_BACKEND" >&2; exit 1 ;;
  esac
}

# keyring_unit_lines <credential>: the [Service] lines that hand the unit
# its passphrase; none for test.
keyring_unit_lines() {
  [ "$KEYRING_BACKEND" = file ] || return 0
  printf 'LoadCredentialEncrypted=keyring:%s/%s\nEnvironment=AETHER_KEYRING_PASSPHRASE_FILE=%%d/keyring\n' "$CREDSTORE" "$1"
}

# keyring_keys <credential> <aetherd> <keys args...>: `aetherd keys ...` on
# this keyring, the passphrase fed from the credential for file.
keyring_keys() {
  local cred=$1 bin=$2
  shift 2
  if [ "$KEYRING_BACKEND" = file ]; then
    systemd-creds decrypt --name=keyring "$CREDSTORE/$cred" - | "$bin" keys "$@" --keyring-backend file
  else
    "$bin" keys "$@" --keyring-backend test
  fi
}
