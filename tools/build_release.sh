#!/usr/bin/env bash
# Builds the release zips into dist/:
#   NFSTR_Server_Windows.zip   extract into the game folder -> <game>\NFSTR_Server\
#   NFSTR_Server_Linux.zip     the same on Linux (race servers run through Proton/umu)
#   NFSTR_Client.zip           extract into the game folder (Windows and Linux players)
# Needs: go, python3. The plugin (build/NFSTR_Online.asi) is built by tools/build_plugin.sh (mingw-w64).
set -e
cd "$(dirname "$0")/.."
OUT=dist; rm -rf "$OUT"; mkdir -p "$OUT/stage"
B() { (cd source && GOOS=$1 GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o "../$OUT/stage/$2" "./cmd/$3"); }
zipdir() { python3 - "$1" "$2" "$3" <<'PY'
import os, sys, zipfile
src, prefix, dst = sys.argv[1:4]
with zipfile.ZipFile(dst, "w", zipfile.ZIP_DEFLATED) as z:
    for root, _, files in os.walk(src):
        for f in sorted(files):
            p = os.path.join(root, f)
            info = zipfile.ZipInfo.from_file(p, os.path.join(prefix, os.path.relpath(p, src)))
            if f in ("nfsrun-server", "nfstr-origin", "start.sh", "ds_launch.sh", "NFSTR_Online_Play.sh"):
                info.external_attr = 0o755 << 16
            z.writestr(info, open(p, "rb").read(), zipfile.ZIP_DEFLATED)
PY
}
server_common() { # $1 = folder
  mkdir -p "$1/data" "$1/docs"
  cp -r data/cars.json data/legal data/locker data/news data/news.example.json data/playlists.txt "$1/data/" 2>/dev/null || true
  cp config/server.env.example "$1/server.env.example"
  cp README.md LICENSE.md CREDITS.md "$1/"; cp -r docs/*.md "$1/docs/"
}
# server, Windows
S="$OUT/stage/win/NFSTR_Server"; mkdir -p "$S"; B windows nfsrun-server.exe nfsrun-server; mv "$OUT/stage/nfsrun-server.exe" "$S/"
server_common "$S"; cp tools/open_ds_firewall.bat "$S/"
printf '@echo off\r\ncd /d "%%~dp0"\r\nnfsrun-server.exe\r\npause\r\n' > "$S/Start_Server.bat"
zipdir "$OUT/stage/win" "" "$OUT/NFSTR_Server_Windows.zip"
# server, Linux
S="$OUT/stage/lin/NFSTR_Server"; mkdir -p "$S/tools-linux"; B linux nfsrun-server nfsrun-server; mv "$OUT/stage/nfsrun-server" "$S/"
server_common "$S"; cp tools-linux/ds_launch.sh tools-linux/install_services.sh "$S/tools-linux/"
printf '#!/usr/bin/env bash\ncd "$(dirname "$(readlink -f "$0")")" && exec ./nfsrun-server\n' > "$S/start.sh"
zipdir "$OUT/stage/lin" "" "$OUT/NFSTR_Server_Linux.zip"
# client
C="$OUT/stage/client"; mkdir -p "$C/Scripts"
B windows nfstr-origin.exe nfstr-origin; B linux nfstr-origin nfstr-origin; mv "$OUT/stage/nfstr-origin.exe" "$OUT/stage/nfstr-origin" "$C/Scripts/"
cp third_party/dinput8.dll "$C/"; cp third_party/LICENSE-Ultimate-ASI-Loader.txt "$C/"
cp build/NFSTR_Online.asi "$C/Scripts/"
cp client/NFSTR_Online_Play.bat client/NFSTR_Online_Play.sh "$C/"
cat > "$C/Scripts/NFSTR_Online.ini" <<'INI'
[Server]
Host=

[Player]
Name=
Password=

[Origin]
Enable=1
StartDelayMs=20000
Embedded=0
INI
zipdir "$C" "" "$OUT/NFSTR_Client.zip"
rm -rf "$OUT/stage"; ls -l "$OUT"
