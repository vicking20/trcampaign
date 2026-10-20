#!/usr/bin/env python3
"""Create NFSTR_Server.exe from the game's own exe: the dedicated-server build is the client exe with 3 bytes changed at file offset 0x180CA
(`6a 00 52` -> `52 6a 01`). Nothing is downloaded or redistributed: it is derived from your copy.
Usage: tools/make_server_exe.py <game-dir> [out-dir]   (out-dir defaults to game-dir; use it when game-dir is read-only)"""
import os, sys
CLIENT, SERVER = "Need For Speed The Run.exe", "NFSTR_Server.exe"
def make(game, out=None):
    out = out or game
    src = os.path.join(game, CLIENT); dst = os.path.join(out, SERVER)
    if not os.path.isfile(src): sys.exit("no '%s' in %s" % (CLIENT, game))
    data = bytearray(open(src, "rb").read())
    if data[0x180CA:0x180CD] == b"\x52\x6a\x01": print(SERVER, "source is already a server exe"); 
    elif data[0x180CA:0x180CD] != b"\x6a\x00\x52": sys.exit("unexpected game exe (patch site differs): this build is not supported")
    else: data[0x180CA:0x180CD] = b"\x52\x6a\x01"
    if os.path.islink(dst): os.remove(dst)
    open(dst, "wb").write(data); print("created", dst)
if __name__ == "__main__":
    if len(sys.argv) < 2: sys.exit(__doc__)
    make(sys.argv[1], sys.argv[2] if len(sys.argv) > 2 else None)
