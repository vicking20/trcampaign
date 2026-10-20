#!/usr/bin/env python3
"""Find byte/ASCII string occurrences in a PE file and print their virtual addresses.
Usage: findstr.py <exe> <needle> [needle...]
The VA is what Ghidra scripts (RefsTo.java) expect."""
import struct, sys

def pe_map(data):
    e_lfanew = struct.unpack_from('<I', data, 0x3C)[0]
    coff = e_lfanew + 4
    nsec, = struct.unpack_from('<H', data, coff + 2)
    optsz, = struct.unpack_from('<H', data, coff + 16)
    opt = coff + 20
    imagebase, = struct.unpack_from('<I', data, opt + 28)
    sect = opt + optsz
    out = []
    for i in range(nsec):
        s = sect + 40 * i
        name = data[s:s + 8].rstrip(b'\0').decode('latin1')
        vsz, va, rsz, raw = struct.unpack_from('<IIII', data, s + 8)
        out.append((name, va, raw, rsz))
    return imagebase, out

def to_va(imagebase, secs, off):
    for name, va, raw, rsz in secs:
        if raw <= off < raw + rsz:
            return imagebase + va + (off - raw)
    return None

def main():
    data = open(sys.argv[1], 'rb').read()
    imagebase, secs = pe_map(data)
    print("imagebase %08X  sections: %s" % (imagebase, ", ".join("%s va=%X raw=%X" % (n, v, r) for n, v, r, _ in secs)))
    for needle in sys.argv[2:]:
        pat = needle.encode()
        hits = 0
        start = 0
        while True:
            i = data.find(pat, start)
            if i < 0:
                break
            va = to_va(imagebase, secs, i)
            if va:
                ctx = data[i:i + len(pat) + 24].split(b'\0')[0][:60]
                print("  %-42s @ file %08X  VA %08X  ctx=%r" % (needle, i, va, ctx.decode('latin1')))
                hits += 1
            start = i + 1
        if not hits:
            print("  %-42s (not found)" % needle)

if __name__ == '__main__':
    main()
