#!/usr/bin/env python3
"""Find (and optionally change) the dedicated server's player limits in live memory.
ServerRaceManager (PS3 debug build layout) keeps, as consecutive 32-bit values:
    +0 m_maxRacerCount  +4 m_racersCount  +8 m_humansCount  ...  +40 m_minPlayers  +44 m_maxPlayers  +48 m_playerGridPosition
This scans the server process for that shape. Run it while a race is loaded (state 6) so the manager exists.
Usage: tools/probe_limits.py                 list candidates
       tools/probe_limits.py --set 16        write 16 into maxRacerCount and maxPlayers of every candidate
       (--humans N : only candidates whose humans count is N)"""
import argparse, os, re, struct, sys
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import race_results as rr

ap = argparse.ArgumentParser(); ap.add_argument("--set", type=int); ap.add_argument("--loose", action="store_true"); ap.add_argument("--humans", type=int)
args = ap.parse_args()
pid = rr.server_pid()
if not pid: sys.exit("no NFSTR_Server.exe process")
print("pid", pid, "state/session/race", rr.state(pid))
m = open("/proc/%d/mem" % pid, "r+b" if args.set else "rb", 0)
hits = []
for line in open("/proc/%d/maps" % pid):
    a, b = [int(x, 16) for x in line.split()[0].split("-")]
    if a >= 0x100000000 or "rw" not in line.split()[1]: continue
    try: m.seek(a); d = m.read(b - a)
    except OSError: continue
    n = len(d) // 4
    w = memoryview(d[: n * 4]).cast("I")
    for i in range(0, n - 13):
        mx, rc, hc = w[i], w[i + 1], w[i + 2]
        if not (2 <= mx <= 64 and rc <= 64 and hc <= 8): continue
        if not (w[i + 4] > 0x1000000 and w[i + 5] > 0x1000000 and w[i + 9] & 0xfefefefe == 0): continue      # raceEntity/raceInfo pointers, 4 bool flags
        mn, mp, gp = w[i + 10], w[i + 11], w[i + 12]
        if not (0 <= mn <= 8 and 2 <= mp <= 64 and gp <= 64): continue
        if args.humans is not None and hc != args.humans: continue
        hits.append((a + i * 4, mx, rc, hc, mn, mp, gp))
print("candidates (addr maxRacer racers humans minPlayers maxPlayers grid):")
for h in hits[:40]: print("  %08x %3d %3d %3d %3d %3d %3d" % h)
print(len(hits), "candidates")
if args.set and hits:
    for h in hits:
        m.seek(h[0]); m.write(struct.pack("<I", args.set))          # maxRacerCount
        m.seek(h[0] + 44); m.write(struct.pack("<I", args.set))     # maxPlayers
    print("wrote", args.set, "into", len(hits), "candidates")
