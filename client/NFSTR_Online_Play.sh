#!/usr/bin/env bash
# NFSTR Online launcher for Linux / Steam Deck. Put it in the game folder (next to "Need For Speed The Run.exe") and run it.
# It sets the one environment variable the plugin needs (use our dinput8.dll instead of Wine's built-in one) and starts the game.
#   ./NFSTR_Online_Play.sh              desktop
#   ./NFSTR_Online_Play.sh --gamescope  desktop, inside a gamescope window (1280x800; edit GS_ARGS to change)
# Steam Game Mode (gamescope): Add a Non-Steam Game > browse to this script (set "Show all files"), then just launch it; gamescope is already running there.
# Needs umu-run (https://github.com/Open-Wine-Components/umu-launcher) or wine. Optional: PROTONPATH, WINEPREFIX, GAMEID.
cd "$(dirname "$(readlink -f "$0")")" || exit 1
EXE="Need For Speed The Run.exe"
[ -f "$EXE" ] || { echo "run this script from the game folder (no '$EXE' here)"; exit 1; }
export WINEDLLOVERRIDES="${WINEDLLOVERRIDES:+$WINEDLLOVERRIDES;}dinput8=n,b"
export WINEPREFIX="${WINEPREFIX:-$HOME/.local/share/nfstr-online/prefix}"
export GAMEID="${GAMEID:-umu-default}"
GS=()
if [ "$1" = "--gamescope" ]; then
  if [ -z "$GAMESCOPE_WAYLAND_DISPLAY" ] && command -v gamescope >/dev/null; then GS=(gamescope ${GS_ARGS:--W 1280 -H 800 -f} --); fi
fi
if command -v umu-run >/dev/null; then RUN=(umu-run)
elif command -v wine >/dev/null; then RUN=(wine)
else echo "umu-run or wine is required"; exit 1; fi
mkdir -p "$WINEPREFIX"
exec "${GS[@]}" "${RUN[@]}" "./$EXE"
