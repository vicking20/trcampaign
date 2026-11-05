#!/usr/bin/env bash
# Linux: host a game and play it on this PC. Put this file in the game folder (next to "Need For Speed The Run.exe").
cd "$(dirname "$(readlink -f "$0")")" || exit 1
python3 Scripts/lsx.py --setup 127.0.0.1 || exit 1
trap './NFSTR_Server/StartServer.sh stop' EXIT
./NFSTR_Server/StartServer.sh || exit 1
./NFSTR_Online_Play.sh
