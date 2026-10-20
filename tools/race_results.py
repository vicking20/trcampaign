#!/usr/bin/env python3
"""Read race results from the running dedicated server (NFSTR_Server.exe) by scanning its memory, same signature as
plugin/nfstr_lan.c: entries [name ...][n][n][float seconds][0x952C945B] in the static pool 0x02700000-0x03000000.
Library: state(), baseline(), results(base) ; CLI: race_results.py  -> prints state and current entries."""
import os, re, struct, sys

SO = 0x27A3300
SIG = struct.pack("<I", 0xB1F34603)
POOL = (0x02700000, 0x03000000)

import procmem

def server_pid(): return procmem.find_pid("NFSTR_Server.exe")

_mems = {}
def _rd(pid, addr, n):
    if pid not in _mems: _mems[pid] = procmem.Mem(pid)
    return _mems[pid].read(addr, n)

def state(pid):
    st, = struct.unpack("<I", _rd(pid, SO + 0x1070, 4))
    ss, = struct.unpack("<i", _rd(pid, SO + 4, 4))
    rc, = struct.unpack("<i", _rd(pid, SO + 8, 4))
    return st, ss, rc

def entries(pid):
    """Result entries in the static pool: {(addr, time): name}.
    Layout: [name\0] ... [n][n][float seconds][hash]   (n = small length word, repeated; time 0.0 = DNF). The trailing
    word changes between builds/sessions, so it is not used. The name is the last printable run (>=3 chars, not a
    path or a "0400_Track" string) within 0x40 bytes before the pair."""
    out = {}
    data = _rd(pid, POOL[0], POOL[1] - POOL[0])
    words = memoryview(data[: len(data) // 4 * 4]).cast("I")
    fl = memoryview(data[: len(data) // 4 * 4]).cast("f")
    for k in range(16, len(words) - 2):
        n = words[k]
        if not 8 <= n <= 0x80 or not 8 <= words[k + 1] <= 0x40: continue      # later races: two different length words
        t = fl[k + 2]
        if not (t == 0.0 or 1.0 < t < 36000.0): continue
        i = k * 4
        seg = data[max(0, i - 0x40): i]
        names = [x.group()[:-1].decode() for x in re.finditer(rb"[\x20-\x7e]{3,24}\x00", seg)
                 if b"/" not in x.group() and b"\\" not in x.group()]
        names = [x for x in names if not re.match(r"^\d{4}_", x)]
        if names: out[(POOL[0] + i, round(t, 3))] = names[-1]
    return out

def baseline(pid): return set(entries(pid))

def results(pid, base=frozenset()):
    """[(name, seconds)] finishers by time, DNF (0) last; only entries not in `base`."""
    best = {}
    for key, name in entries(pid).items():
        if key in base: continue
        best[name] = max(best.get(name, 0.0), key[1])
    return sorted(best.items(), key=lambda kv: (kv[1] <= 0, kv[1]))

if __name__ == "__main__":
    pid = server_pid()
    if not pid: sys.exit("no NFSTR_Server.exe process")
    print("pid", pid, "state/session/race", state(pid))
    for (a, t), n in sorted(entries(pid).items()): print(hex(a), t, n)
