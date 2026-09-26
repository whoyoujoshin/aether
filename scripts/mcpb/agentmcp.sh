#!/bin/sh
# macOS launcher for the MCPB bundle: MCPB picks a command per OS but not
# per CPU, so run this Mac's build of agentmcp.
dir=$(dirname "$0")
case "$(uname -m)" in
arm64) exec "$dir/arm64/agentmcp" "$@" ;;
*) exec "$dir/amd64/agentmcp" "$@" ;;
esac
