#!/usr/bin/env bash
# Build the game plugins (needs i686-w64-mingw32-gcc). Output: build/NFSTR_Online.asi (online server) and build/NFSTR_LAN.asi (LAN mod).
set -e
cd "$(dirname "$0")/.."; mkdir -p build
i686-w64-mingw32-gcc -O2 -shared -static-libgcc -o build/NFSTR_Online.asi plugin/nfstr_online.c -lws2_32 -lpsapi -ladvapi32
i686-w64-mingw32-gcc -O2 -shared -static-libgcc -o build/NFSTR_LAN.asi plugin/nfstr_lan.c -lws2_32
ls -la build
