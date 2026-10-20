#!/usr/bin/env python3
"""Write the dedicated server's career-state fields directly (no gdb).

Reproduces what the LAN plugin's server role does at runtime:
    SERVER_OBJ + 0x1070  career state   (0=None, 2=PreLoad/WaitingForPlayers, 6=InRace, 8=RaceResults)
    SERVER_OBJ + 0x4     session index / id
    SERVER_OBJ + 0x8     race index (-1 = rewind so the first session start is replayed)
    SERVER_OBJ + 0x1074  career state timer (float seconds)

Usage: tools/server_poke.py [state=N] [race=N] [session=N] [timer=F]   (then prints the new state)
"""
import os, struct, sys

pid = None
for p in os.listdir('/proc'):
    if p.isdigit():
        try: c = open(f'/proc/{p}/cmdline', 'rb').read().decode(errors='ignore')
        except OSError: continue
        if 'NFSTR_Server.exe' in c and not any(x in c for x in ('umu', 'bash', 'python', 'start_server')):
            pid = int(p)
if not pid:
    raise SystemExit("no NFSTR_Server.exe process")

SO = 0x27A3300
FIELDS = {'state': (0x1070, '<I'), 'session': (0x4, '<i'), 'race': (0x8, '<i'), 'timer': (0x1074, '<f')}

m = open(f'/proc/{pid}/mem', 'r+b', 0)
for arg in sys.argv[1:]:
    k, _, v = arg.partition('=')
    if k not in FIELDS:
        raise SystemExit(f"unknown field {k!r}")
    off, fmt = FIELDS[k]
    m.seek(SO + off); m.write(struct.pack(fmt, float(v) if fmt == '<f' else int(v, 0)))
m.seek(SO + 0x1070); st = struct.unpack('<I', m.read(4))[0]
m.seek(SO + 0x1074); tm = struct.unpack('<f', m.read(4))[0]
m.seek(SO + 4); ss = struct.unpack('<i', m.read(4))[0]
m.seek(SO + 8); rc = struct.unpack('<i', m.read(4))[0]
print(f"pid {pid} state={st} timer={tm:.1f}s session={ss} race={rc}")
