# Shared helpers for the halt monitor's cutover scripts.
# Sourced, not executed. The voting-power rule matches SafeToStop and
# HasQuorum in cmd/chainwatch/power.go: CometBFT commits only when the
# signed power is strictly more than 2/3 of the total.

REQUIRED_GO=go1.26.10

# go_version_ok TEXT WANT: TEXT is `go version` output, WANT is go1.26.10.
go_version_ok() {
  local got=$1 want=$2
  [[ "$got" =~ (^|[[:space:]])"${want}"($|[[:space:]]) ]]
}

# safe_to_stop TOTAL POWER: success when removing POWER leaves strictly
# more than 2/3 of TOTAL. Same inequality as chainwatch.SafeToStop.
safe_to_stop() {
  awk -v t="$1" -v p="$2" 'BEGIN {
    if (t !~ /^[0-9]+$/ || p !~ /^[0-9]+$/) exit 1
    if ((t + 0) <= 0 || (p + 0) < 0 || (p + 0) > (t + 0)) exit 1
    rem = t - p
    if ((rem * 3) > (t * 2)) exit 0
    exit 1
  }'
}

# quorum_ok SIGNED TOTAL: success when SIGNED is strictly more than 2/3.
quorum_ok() {
  awk -v s="$1" -v t="$2" 'BEGIN {
    if (s !~ /^[0-9]+$/ || t !~ /^[0-9]+$/) exit 1
    if ((s * 3) > (t * 2)) exit 0
    exit 1
  }'
}

# rollback_allowed HEIGHT ACTIVATION: success only while HEIGHT is still
# below a positive activation height. At and after that height an old
# binary can disagree with the chain, so it stays where it is.
rollback_allowed() {
  awk -v h="$1" -v a="$2" 'BEGIN {
    if (h !~ /^[0-9]+$/ || a !~ /^[0-9]+$/) exit 1
    if ((a + 0) > 0 && (h + 0) < (a + 0)) exit 0
    exit 1
  }'
}

verify_sha256() {
  local file=$1 want=$2 got
  [[ -f "$file" ]] || return 1
  command -v sha256sum >/dev/null 2>&1 || return 1
  got=$(sha256sum "$file" | awk '{print tolower($1)}')
  [[ "$got" == "$(printf '%s' "$want" | tr 'A-F' 'a-f')" ]]
}

# refuse_build_on_validator: a compile on a live validator has halted the
# chain before. This script has no override.
refuse_build_on_validator() {
  if command -v systemctl >/dev/null 2>&1 && systemctl is-active --quiet aetherd 2>/dev/null; then
    echo "refusing to build: systemd unit aetherd is active. Build on a workstation or in CI, then copy the binary." >&2
    return 1
  fi
  if command -v pgrep >/dev/null 2>&1 && pgrep -x aetherd >/dev/null 2>&1; then
    echo "refusing to build: an aetherd process is running on this machine. Build on a workstation or in CI." >&2
    return 1
  fi
  return 0
}

# parse_nodes_file FILE fills N_NAME N_SSH N_RPC N_VAL N_UNIT.
# Lines: name ssh rpc validator [unit]
# ssh is "local" or user@host. validator is the 40-hex consensus address.
parse_nodes_file() {
  local file=$1 line
  N_NAME=()
  N_SSH=()
  N_RPC=()
  N_VAL=()
  N_UNIT=()
  [[ -f "$file" ]] || { echo "no nodes file $file" >&2; return 1; }
  while IFS= read -r line || [[ -n "$line" ]]; do
    line=${line%$'\r'}
    line=${line%%#*}
    line=${line#"${line%%[![:space:]]*}"}
    line=${line%"${line##*[![:space:]]}"}
    [[ -z "$line" ]] && continue
    local -a f=()
    read -r -a f <<<"$line"
    if [[ ${#f[@]} -lt 4 || ${#f[@]} -gt 5 ]]; then
      echo "bad nodes line (want: name ssh rpc validator [unit]): $line" >&2
      return 1
    fi
    if [[ ! "${f[3]}" =~ ^[0-9A-Fa-f]{40}$ ]]; then
      echo "validator address must be 40 hex characters (status .result.validator_info.address): ${f[3]}" >&2
      return 1
    fi
    N_NAME+=("${f[0]}")
    N_SSH+=("${f[1]}")
    N_RPC+=("${f[2]%/}")
    N_VAL+=("${f[3]^^}")
    if [[ ${#f[@]} -eq 5 ]]; then
      if [[ ! "${f[4]}" =~ ^[A-Za-z0-9@._-]+$ ]]; then
        echo "bad unit name: ${f[4]}" >&2
        return 1
      fi
      N_UNIT+=("${f[4]}")
    else
      N_UNIT+=("aetherd")
    fi
  done <"$file"
  [[ ${#N_NAME[@]} -gt 0 ]] || { echo "no nodes in $file" >&2; return 1; }
}

# bin_from_execstart LINE: the aetherd path inside systemctl's ExecStart.
bin_from_execstart() {
  printf '%s\n' "$1" | grep -oE '/[^ ;]*aetherd[^ ;]*' | head -1
}

# ssh_quote ARGS...: ARGS as one string the remote shell splits back
# into exactly ARGS. ssh joins its arguments with spaces and hands the
# result to the remote shell, so an unquoted `bash -c 'a "$b"'` arrives
# as `bash -c a ...` and an `&` in a URL backgrounds the command.
ssh_quote() {
  local out="" a
  for a in "$@"; do
    out+="$(printf '%q' "$a") "
  done
  printf '%s' "${out% }"
}

remote_exec() {
  local dest=$1
  shift
  if [[ -n "${REMOTE_EXEC_OVERRIDE:-}" ]]; then
    "$REMOTE_EXEC_OVERRIDE" "$dest" "$@"
    return
  fi
  if [[ "$dest" == local ]]; then
    "$@"
  else
    command ssh -o BatchMode=yes -o ConnectTimeout=20 "$dest" "$(ssh_quote "$@")"
  fi
}

# unit_exe_sha DEST UNIT: sha256 of the binary the unit's main process
# runs. Found through systemd, not pidof: the binaries are named
# aetherd-<commit>, and `pidof aetherd` doesn't match those.
unit_exe_sha() {
  local dest=$1 unit=$2
  # shellcheck disable=SC2016 # expanded on the node
  remote_exec "$dest" bash -c 'pid=$(systemctl show -p MainPID --value "$1"); [ "${pid:-0}" -gt 0 ] || exit 1; sha256sum "/proc/$pid/exe"' _ "$unit" \
    | awk '{print tolower($1)}'
}

# unit_running DEST UNIT: success when the unit has a main process.
unit_running() {
  local dest=$1 unit=$2
  # shellcheck disable=SC2016 # expanded on the node
  remote_exec "$dest" bash -c 'pid=$(systemctl show -p MainPID --value "$1"); [ "${pid:-0}" -gt 0 ]' _ "$unit"
}

# remote_copy SRC DEST PATH
remote_copy() {
  local src=$1 dest=$2 path=$3
  if [[ -n "${REMOTE_COPY_OVERRIDE:-}" ]]; then
    "$REMOTE_COPY_OVERRIDE" "$src" "$dest" "$path"
    return
  fi
  if [[ "$dest" == local ]]; then
    cp -- "$src" "$path"
  else
    command scp -o BatchMode=yes -o ConnectTimeout=20 -- "$src" "$dest:$path"
  fi
}

# remote_script DEST SCRIPT args... runs SCRIPT on DEST as root, on stdin.
remote_script() {
  local dest=$1 script=$2
  shift 2
  if [[ -n "${REMOTE_SCRIPT_OVERRIDE:-}" ]]; then
    "$REMOTE_SCRIPT_OVERRIDE" "$dest" "$script" "$@"
    return
  fi
  if [[ "$dest" == local ]]; then
    bash -s -- "$@" <"$script"
  else
    command ssh -o BatchMode=yes -o ConnectTimeout=20 "$dest" "$(ssh_quote bash -s -- "$@")" <"$script"
  fi
}

die() {
  echo "$*" >&2
  exit 1
}
