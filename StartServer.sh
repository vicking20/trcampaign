#!/usr/bin/env bash
# Linux: start the NFSTR Online server for the game folder this file is in.   ./StartServer.sh [address] [modes]   (stop: ./StartServer.sh stop)
cd "$(dirname "$(readlink -f "$0")")" || exit 1
[ -f "Need For Speed The Run.exe" ] || { echo "Put this file in the game folder (next to 'Need For Speed The Run.exe')."; exit 1; }
export NFSTR_GAME_DIR="$PWD"
if command -v umu-run >/dev/null; then HOST=NFSTR_Server/tools/host_server.sh; else HOST=NFSTR_Server/tools/host_headless.sh; fi   # Proton/umu if present, else plain Wine + Xvfb
if [ "$1" = stop ]; then exec $HOST stop; fi
ADDR="${1:-auto}"; MODES="${2:-plague,bounty,streak,cannonball}"
echo "Starting the server (modes: $MODES). Friends use one of this PC's addresses: $(hostname -I)"
if [ "$HOST" = NFSTR_Server/tools/host_headless.sh ]; then exec $HOST start "$ADDR" "$MODES" 3 hold; fi
exec $HOST "$ADDR" "$MODES" 3 hold
