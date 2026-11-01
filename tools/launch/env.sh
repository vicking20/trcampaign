# Shared settings for the launch scripts (sourced). Override any of these in the environment.
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
if [ -n "${NFSTR_GAME_DIR:-}" ]; then
  GAME="$NFSTR_GAME_DIR"
elif [ -d "$ROOT/online_test/Core" ]; then
  GAME="$ROOT/online_test"
elif [ -d "$ROOT/runtime/nfstr_host/Core" ]; then
  GAME="$ROOT/runtime/nfstr_host"
else
  GAME="$ROOT/online_test"
fi
_CT="$HOME/.local/share/Steam/compatibilitytools.d"
export PROTONPATH="${PROTONPATH:-$(ls -d "$_CT"/GE-Proton* 2>/dev/null | sort -V | tail -1)}"
[ -n "$PROTONPATH" ] || export PROTONPATH="$(ls -d "$_CT"/*Proton* 2>/dev/null | sort -V | tail -1)"
export GAMEID=0 WINEDLLOVERRIDES="dinput8=n,b" DISPLAY="${DISPLAY:-:0}"
