# Shared settings for the launch scripts (sourced). Override any of these in the environment.
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
GAME="${NFSTR_GAME_DIR:-$ROOT/online_test}"                      # the game folder (a copy or symlinks of your install)
_CT="$HOME/.local/share/Steam/compatibilitytools.d"
export PROTONPATH="${PROTONPATH:-$(ls -d "$_CT"/GE-Proton* 2>/dev/null | sort -V | tail -1)}"
[ -n "$PROTONPATH" ] || export PROTONPATH="$(ls -d "$_CT"/*Proton* 2>/dev/null | sort -V | tail -1)"
export GAMEID=0 WINEDLLOVERRIDES="dinput8=n,b" DISPLAY="${DISPLAY:-:0}"
