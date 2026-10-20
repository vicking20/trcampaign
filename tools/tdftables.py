#!/usr/bin/env python3
"""Find TDF descriptor tables in nfstr.exe containing given tags. usage: tdftables.py TAG [TAG...]
Prints each matching table: address, then tag/offset/typecode per entry."""
import sys, struct
sys.path.insert(0, '.')
import tdfdump as t, blaze
def entries():
    out = []
    for a, s, r in t.secs:
        if not (0x2000000 <= a < 0x2900000): continue
        for va in range(a, a + s - 8, 4):
            if t.off(va) + 8 > len(t.d): break
            w = struct.unpack_from('<I', t.d, t.off(va))[0]
            if w & 0xff or not w: continue
            n = struct.unpack_from('<I', t.d, t.off(va) + 4)[0]
            if not (n & 0xff00) or (n & 0xe0) : continue
            try: tag = blaze.tag_decode(struct.pack('>I', w)[:3])
            except Exception: continue
            if len(tag) >= 2 and all(c.isalnum() or c == '_' for c in tag) and (n>>16) < 0x400:
                out.append((va, tag, n))
    return out
want = set(sys.argv[1:])
es = entries()
tabs, cur = [], []
for e in es:
    if cur and e[0] - cur[-1][0] > 0x60: tabs.append(cur); cur = []
    cur.append(e)
tabs.append(cur)
for tb in tabs:
    tags = {e[1] for e in tb}
    if len(want & tags) >= max(2, len(want) // 2):
        print('TABLE', hex(tb[0][0]), len(tb))
        print('  ', ' '.join('%s@%x:%02x' % (e[1], e[2] >> 16, e[2] & 0xff) for e in tb))
