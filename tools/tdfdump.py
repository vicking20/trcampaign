#!/usr/bin/env python3
"""Dump TDF field descriptor tables from nfstr.exe (no Ghidra). usage: tdfdump.py VA [count]
Entry = 3 dwords? see layout detection below; tags decoded with server/blaze.tag_decode."""
import struct, sys, os
sys.path.insert(0, os.path.join(os.path.dirname(os.path.abspath(__file__)), '..', 'server'))
import blaze
EXE = os.path.join(os.path.dirname(os.path.abspath(__file__)), 'nfstr.exe')
d = open(EXE, 'rb').read()
pe = struct.unpack_from('<I', d, 0x3c)[0]
nsec = struct.unpack_from('<H', d, pe + 6)[0]; opt = struct.unpack_from('<H', d, pe + 20)[0]
base = struct.unpack_from('<I', d, pe + 24 + 28)[0]
secs = []
for i in range(nsec):
    o = pe + 24 + opt + i * 40
    vs, va, rs, ro = struct.unpack_from('<IIII', d, o + 8)
    secs.append((base + va, max(vs, rs), ro))
def off(va):
    for a, s, r in secs:
        if a <= va < a + s: return r + va - a
def u32(va): return struct.unpack_from('<I', d, off(va))[0]
def rstr(va, n=40):
    o = off(va)
    if o is None: return None
    e = d.index(b'\0', o)
    return d[o:e][:n].decode('latin1') if e - o > 0 else ''
def isstr(va):
    if not (0x400000 <= va < 0x3000000) or off(va) is None: return False
    s = rstr(va); return bool(s) and s.isprintable()
if __name__ == '__main__':
    va = int(sys.argv[1], 0); n = int(sys.argv[2]) if len(sys.argv) > 2 else 40
    for i in range(n):
        w = [u32(va + 4 * (i * 6 + k)) for k in range(6)]
        print(hex(va + 24 * i), ' '.join('%08x' % x for x in w))
