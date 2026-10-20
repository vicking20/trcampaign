#!/usr/bin/env bash
# The host machine's own game client (separate Wine prefix from the dedicated server).
. "$(dirname "$0")/env.sh"; cd "$GAME" || exit 1
export WINEPREFIX="${NFSTR_CLIENT_PREFIX:-$ROOT/runtime/pfx}"
exec umu-run "./Need For Speed The Run.exe" -NfsOnline.AllowSinglePlayerOnline true $EXTRA_ARGS
