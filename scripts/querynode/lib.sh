# Helpers for the query node (docs/QUERY-NODE.md): a full node that holds
# no validator key and answers the seed's services instead of the seed's
# validator. Sourced by setup.sh, point.sh and lib_test.sh.

# toml_set <file> <section> <key> <value>: sets key = value inside
# [section] ("" for the keys before the first section). The key must
# already be there, as it is in the files `aetherd init` writes; value is
# written as given, so strings carry their own quotes. Returns 1 if the
# key wasn't found.
toml_set() {
  local file=$1 section=$2 key=$3 value=$4 tmp
  tmp=$(mktemp)
  if ! awk -v sec="$section" -v key="$key" -v val="$value" '
    BEGIN { cur = ""; done = 0 }
    /^[[:space:]]*\[[^]]*\][[:space:]]*$/ {
      cur = $0; gsub(/^[[:space:]]*\[|\][[:space:]]*$/, "", cur)
    }
    !done && cur == sec && $0 ~ "^[[:space:]]*" key "[[:space:]]*=" {
      print key " = " val; done = 1; next
    }
    { print }
    END { exit done ? 0 : 1 }
  ' "$file" >"$tmp"; then
    rm -f "$tmp"
    return 1
  fi
  cat "$tmp" >"$file"
  rm -f "$tmp"
}

# toml_get <file> <section> <key>: prints the raw value of key in [section].
toml_get() {
  awk -v sec="$2" -v key="$3" '
    /^[[:space:]]*\[[^]]*\][[:space:]]*$/ {
      cur = $0; gsub(/^[[:space:]]*\[|\][[:space:]]*$/, "", cur)
    }
    cur == sec && $0 ~ "^[[:space:]]*" key "[[:space:]]*=" {
      sub("^[[:space:]]*" key "[[:space:]]*=[[:space:]]*", ""); print; exit
    }
  ' "$1"
}

# valid_hostport accepts host:port with a plain host (letters, digits,
# dots, dashes) and a numeric port. Anything else could break a unit file.
valid_hostport() {
  [[ "$1" =~ ^[A-Za-z0-9.-]+:[0-9]{1,5}$ ]]
}

# valid_rpc accepts http://host:port.
valid_rpc() {
  [[ "$1" =~ ^http://[A-Za-z0-9.-]+:[0-9]{1,5}$ ]]
}

# qn_target <unit>: whether point.sh moves this unit. The services that
# read the chain for the public move; what watches or feeds the seed's
# own validator (aetherd, the powminer, minerwatch, memwatch, chainwatch,
# ibcwatch) and the faucet stay on the local node.
qn_target() {
  case "$1" in
    aether-explorer | aether-agentmcp | aether-sandbox | aether-agentservices | aether-facilitator | aether-jobboard | aether-paywall-*) return 0 ;;
  esac
  return 1
}

# qn_takes_rpc <unit>: whether the unit's binary has --rpc (the explorer
# and the public agentmcp). The test wallet runs agentmcp with --rpc ""
# (poll instead of push), which stays as it is.
qn_takes_rpc() {
  [[ "$1" == aether-explorer || "$1" == aether-agentmcp ]]
}

_re_escape() { printf '%s' "$1" | sed 's/[.[\*^$/]/\\&/g'; }

# qn_rewrite <file> <takes-rpc yes|no> <grpc host:port> <rpc url>: points
# every non-empty ExecStart= in a unit file or drop-in at the query node.
# A --grpc (or --rpc) naming the local node is replaced; a unit without
# the flag (so on its localhost default) gets it right after the binary.
# A flag naming anything else is left alone. Idempotent.
qn_rewrite() {
  local file=$1 rpc_too=$2 grpc=$3 rpc=$4
  grep -q '^ExecStart=.' "$file" || return 0
  sed -i -E "s#--grpc([ =])(localhost|127\.0\.0\.1):9090\b#--grpc\1${grpc}#g" "$file"
  grep -q -- '--grpc[ =]' "$file" || sed -i -E "s#^(ExecStart=[^ ]+)#\1 --grpc ${grpc}#" "$file"
  if [[ "$rpc_too" == yes ]]; then
    sed -i -E "s#--rpc([ =])\"?http://(localhost|127\.0\.0\.1):26657\b\"?#--rpc\1${rpc}#g" "$file"
    grep -q -- '--rpc[ =]' "$file" || sed -i -E "s#^(ExecStart=[^ ]+)#\1 --rpc ${rpc}#" "$file"
  fi
  return 0
}

# qn_restore <file> <grpc host:port> <rpc url>: the reverse, back to the
# local node. Flags qn_rewrite added stay, naming the local defaults.
qn_restore() {
  local file=$1 g r
  g=$(_re_escape "$2")
  r=$(_re_escape "$3")
  sed -i -E "s#--grpc([ =])${g}\b#--grpc\1localhost:9090#g; s#--rpc([ =])${r}\b#--rpc\1http://localhost:26657#g" "$file"
}

# caddy_upstream <caddyfile> <from-regex> <to>: rewrites `reverse_proxy
# <from>` to `reverse_proxy <to>`, keeping the rest of the line (the gRPC
# site's `{` that opens its h2c transport). <from> must end in a port.
# Prints how many lines changed.
caddy_upstream() {
  local file=$1 from=$2 to=$3 n
  n=$(grep -cE "^[[:space:]]*reverse_proxy[[:space:]]+${from}\b" "$file" || true)
  sed -i -E "s#^([[:space:]]*reverse_proxy[[:space:]]+)${from}\b#\1${to}#" "$file"
  echo "$n"
}
