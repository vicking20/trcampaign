#!/usr/bin/env bash
# DS_LAUNCHER for Linux: starts the race-server exe through Proton in its own clean prefix (never the client's).
# The server calls:  ds_launch.sh <exe> <arguments...>
# Every race server copy gets a prefix of its own (pfx_clean_<N>, copied from pfx_clean on first use): two copies
# starting in one prefix hang each other.
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# Proton to use (override with PROTONPATH)
_CT="$HOME/.local/share/Steam/compatibilitytools.d"
export PROTONPATH="${PROTONPATH:-$(ls -d "$_CT"/GE-Proton* 2>/dev/null | sort -V | tail -1)}"
[ -n "$PROTONPATH" ] || export PROTONPATH="$(ls -d "$_CT"/*Proton* 2>/dev/null | sort -V | tail -1)"
export GAMEID=0 WINEDLLOVERRIDES="dinput8=n,b" DISPLAY="${DISPLAY:-:0}"
BASE="${NFSTR_SERVER_PREFIX:-$ROOT/runtime/pfx_clean}"
N=$(printf '%s\n' "$@" | sed -n 's/^NFSTR_DS_\([0-9]\+\)_port.*/\1/p' | head -1)
if [ -n "$N" ] && [ "$N" != 1 ]; then
  export WINEPREFIX="${BASE}_$N"
  [ -d "$WINEPREFIX" ] || cp -a --reflink=auto "$BASE" "$WINEPREFIX"
else
  export WINEPREFIX="$BASE"
fi
exec umu-run "$@"
