#!/usr/bin/env bash
set -e
cd "$(dirname "$0")/.."; mkdir -p build
i686-w64-mingw32-gcc -O2 -shared -static-libgcc -o build/NFSTR_Online.asi plugin/nfstr_online.c -lws2_32 -lpsapi -ladvapi32 -lwinhttp
ls -la build
