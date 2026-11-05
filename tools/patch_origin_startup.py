#!/usr/bin/env python3
r"""
patch_origin_startup.py - let the game's Origin SDK start.

The EbisuStartup stub calls 0x022013AF, an empty thunk past the end of .text
in the PC 1.1.0.0 exe. The real startup is 0x018B1CE0 (same arguments, no
other caller), so the call is re-pointed:

    0x018AEFBC  E8 EE 23 95 00  ->  E8 1F 2D 00 00   call 0x018B1CE0

Without it the SDK never connects to the launcher and the game shows
"You must be signed in to Origin".

The original bytes are verified first and a backup is written once
(<exe>.before_origin_patch.bak).

Usage:
  python patch_origin_startup.py "<game folder>\Need For Speed The Run.exe"
  python patch_origin_startup.py --check "<exe>"   (report only)
  python patch_origin_startup.py --undo  "<exe>"
"""
import os
import shutil
import struct
import sys

CALL_VA = 0x018AEFBC
ORIG = bytes.fromhex("E8EE239500")          # call 0x022013AF
NEW_TARGET = 0x018B1CE0
NEW = b"\xE8" + struct.pack("<i", NEW_TARGET - (CALL_VA + 5))   # E8 1F 2D 00 00
# EbisuStartupImpl prologue: push ebp; mov ebp,esp; push -1; push 0x021AE8BB
TARGET_PROLOGUE = bytes.fromhex("558BEC6AFF68BBE81A02")
DEAD_LO, DEAD_HI = 0x022013A1, 0x02202000     # zero tail of .text


class PE:
    def __init__(self, data):
        if data[:2] != b"MZ":
            raise SystemExit("not a PE file")
        pe = struct.unpack_from("<I", data, 0x3C)[0]
        nsec = struct.unpack_from("<H", data, pe + 6)[0]
        optsz = struct.unpack_from("<H", data, pe + 20)[0]
        opt = pe + 24
        self.base = struct.unpack_from("<I", data, opt + 28)[0]
        self.secs = []
        for i in range(nsec):
            o = opt + optsz + i * 40
            name = data[o:o + 8].rstrip(b"\0").decode("latin-1")
            vsize, va, rsize, raw = struct.unpack_from("<IIII", data, o + 8)
            self.secs.append((name, self.base + va, vsize, raw, rsize))

    def off(self, va):
        for name, sva, vsize, raw, rsize in self.secs:
            if sva <= va < sva + rsize:
                return raw + (va - sva)
        return None

    def text(self):
        for s in self.secs:
            if s[0] == ".text":
                return s
        return None


def scan_dead_calls(data, pe):
    t = pe.text()
    if not t:
        return []
    _, sva, _, raw, rsize = t
    found = []
    blob = data[raw:raw + rsize]
    for i in range(len(blob) - 5):
        op = blob[i]
        if op not in (0xE8, 0xE9):
            continue
        rel = struct.unpack_from("<i", blob, i + 1)[0]
        tgt = (sva + i + 5 + rel) & 0xFFFFFFFF
        if DEAD_LO <= tgt < DEAD_HI:
            found.append((sva + i, "call" if op == 0xE8 else "jmp", tgt))
    return found


def main(argv):
    args = [a for a in argv[1:] if not a.startswith("--")]
    flags = {a for a in argv[1:] if a.startswith("--")}
    if not args:
        print(__doc__)
        return 2
    path = args[0]
    data = bytearray(open(path, "rb").read())
    pe = PE(data)
    off = pe.off(CALL_VA)
    toff = pe.off(NEW_TARGET)
    if off is None or toff is None:
        print("This is not the expected NFS The Run exe (addresses not mapped).")
        return 1
    cur = bytes(data[off:off + 5])
    print(f"file: {path}")
    print(f"0x{CALL_VA:08X} (file 0x{off:X}): {cur.hex(' ').upper()}")
    if bytes(data[toff:toff + len(TARGET_PROLOGUE)]) != TARGET_PROLOGUE:
        print("EbisuStartupImpl prologue not found at 0x018B1CE0 - different build, not patching.")
        return 1

    dead = scan_dead_calls(bytes(data), pe)
    others = [d for d in dead if d[0] != CALL_VA]
    print(f"calls/jumps into the zero region 0x{DEAD_LO:08X}-0x{DEAD_HI:08X}: {len(dead)}")
    for va, kind, tgt in dead[:40]:
        mark = "  <- EbisuStartup (this patch)" if va == CALL_VA else ""
        print(f"   0x{va:08X} {kind} 0x{tgt:08X}{mark}")
    if others:
        print(f"NOTE: {len(others)} other site(s) jump into the zero region; send this output.")

    if "--check" in flags:
        state = "ORIGINAL" if cur == ORIG else "PATCHED" if cur == NEW else "UNKNOWN"
        print(f"state: {state}")
        return 0

    if "--undo" in flags:
        if cur == ORIG:
            print("already original.")
            return 0
        if cur != NEW:
            print("unexpected bytes, not touching.")
            return 1
        data[off:off + 5] = ORIG
        open(path, "wb").write(data)
        print("restored original bytes.")
        return 0

    if cur == NEW:
        print("already patched.")
        return 0
    if cur != ORIG:
        print("unexpected bytes at the call site - different build or already modified. Not patching.")
        return 1
    bak = path + ".before_origin_patch.bak"
    if not os.path.exists(bak):
        shutil.copyfile(path, bak)
        print(f"backup: {bak}")
    data[off:off + 5] = NEW
    open(path, "wb").write(data)
    print(f"patched: {NEW.hex(' ').upper()}  (call 0x{NEW_TARGET:08X})")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
