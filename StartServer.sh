#!/usr/bin/env bash
# Linux: start the NFSTR Online server for the game folder this file is in.   ./StartServer.sh [address] [modes]   (stop: ./StartServer.sh stop)
cd "$(dirname "$(readlink -f "$0")")" || exit 1
[ -f "Need For Speed The Run.exe" ] || { echo "Put this file in the game folder (next to 'Need For Speed The Run.exe')."; exit 1; }
export NFSTR_GAME_DIR="$PWD"
if command -v umu-run >/dev/null; then HOST=NFSTR_Server/tools/host_server.sh        # Proton via umu-run
elif command -v wine >/dev/null; then HOST=NFSTR_Server/tools/host_headless.sh     # plain Wine (uses your screen, or xvfb if there is none)
else echo "Install wine (or umu-run) first."; exit 1; fi
if [ "$1" = stop ]; then exec $HOST stop; fi
ADDR="${1:-auto}"; MODES="${2:-plague,bounty,streak,cannonball}"
echo "Starting the server (modes: $MODES). Friends use one of this PC's addresses: $(hostname -I)"
if [ "$HOST" = NFSTR_Server/tools/host_headless.sh ]; then exec $HOST start "$ADDR" "$MODES" 3 hold; fi
exec $HOST "$ADDR" "$MODES" 3 hold
