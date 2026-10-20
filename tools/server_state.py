#!/usr/bin/env python3
"""Read the dedicated server's career state from process memory (no gdb): state, timer, session, race."""
import os, struct
pid = None
for p in os.listdir('/proc'):
    if p.isdigit():
        try: c = open(f'/proc/{p}/cmdline', 'rb').read().decode(errors='ignore')
        except OSError: continue
        if 'NFSTR_Server.exe' in c and not any(x in c for x in ('umu', 'bash', 'python', 'start_server')):
            pid = int(p)
if not pid:
    raise SystemExit("no NFSTR_Server.exe process")
m = open(f'/proc/{pid}/mem', 'rb', 0)
def rd(a, n=4): m.seek(a); return m.read(n)
so = 0x27A3300
print(f"pid {pid} state={struct.unpack('<I', rd(so+0x1070))[0]} timer={struct.unpack('<f', rd(so+0x1074))[0]:.1f}s "
      f"session={struct.unpack('<i', rd(so+4))[0]} race={struct.unpack('<i', rd(so+8))[0]}")
mgr = struct.unpack('<I', rd(0x28E5C90))[0]            # global POINTER to the session manager
if mgr > 0x10000:
    try:
        u = lambda a: struct.unpack('<I', rd(a))[0]
        pb, pe, rb, re = u(mgr + 0x56C), u(mgr + 0x570), u(mgr + 0x58C), u(mgr + 0x590)
        print(f"participants={(pe - pb) // 4} loading_list={(re - rb) // 4} (mgr {mgr:#x})")
    except Exception as e:
        print("participants: unreadable", e)
