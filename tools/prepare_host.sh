#!/usr/bin/env bash
# One-time host setup:  tools/prepare_host.sh <path-to-your-game-folder>
# Makes online_test/ (a symlink farm of your install, so the original is not modified), adds the ASI loader and the plugin.
set -e
SRC="${1:?usage: prepare_host.sh <game-folder>}"; ROOT="$(cd "$(dirname "$0")/.." && pwd)"; DST="$ROOT/online_test"
[ -f "$SRC/Need For Speed The Run.exe" ] || { echo "$SRC has no 'Need For Speed The Run.exe'"; exit 1; }
mkdir -p "$DST/Scripts"
for f in "$SRC"/*; do b="$(basename "$f")"; [ "$b" = Scripts ] || ln -sfn "$f" "$DST/$b"; done
python3 "$ROOT/tools/make_server_exe.py" "$SRC" "$DST"          # NFSTR_Server.exe = your game exe with 3 bytes changed
[ -f "$ROOT/third_party/dinput8.dll" ] && cp "$ROOT/third_party/dinput8.dll" "$DST/dinput8.dll" || echo "WARNING: put the Ultimate ASI Loader at third_party/dinput8.dll (see third_party/README) and rerun"
[ -f "$ROOT/build/NFSTR_Online.asi" ] || "$ROOT/tools/build_plugin.sh" >/dev/null
cp "$ROOT/build/NFSTR_Online.asi" "$DST/Scripts/"
printf '[Server]\nHost=127.0.0.1\n\n[Origin]\nEnable=1\nStartDelayMs=20000\n' > "$DST/Scripts/NFSTR_Online.ini"
echo "ready: $DST  (next: tools/host_server.sh <address> ...)"
