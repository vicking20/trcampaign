#!/usr/bin/env python3
"""Loopback test: fake game server <- agent <-> relay <- two fake players."""
import socket, subprocess, sys, time, os, signal

HERE = os.path.dirname(os.path.abspath(__file__))
procs = []
def spawn(*args):
    p = subprocess.Popen([sys.executable, *args], cwd=HERE, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    procs.append(p); return p
def ok(cond, msg):
    print(("PASS " if cond else "FAIL ") + msg)
    if not cond:
        for p in procs: p.kill()
        sys.exit(1)

# fake game server: replies "echo:<src port>:" + payload so we can see who it thinks is talking
srv = socket.socket(socket.AF_INET, socket.SOCK_DGRAM); srv.bind(("127.0.0.1", 29200)); srv.settimeout(0.2)
spawn("relay.py", "--bind", "127.0.0.1", "--control-port", "29000", "--room-ports", "29001-29010")
time.sleep(0.5)
LOCK = "/tmp/claude-1000/test_relay.lock"
if os.path.exists(LOCK): os.remove(LOCK)
spawn("agent.py", "--relay", "127.0.0.1:29000", "--room", "testroom", "--secret", "hunter2x", "--server", "127.0.0.1:29200", "--lock-file", LOCK)
time.sleep(1.0)
pub = 29001

def player():
    s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM); s.settimeout(1.0); return s
def serve_once():
    try:
        d, a = srv.recvfrom(4096)
    except socket.timeout:
        return None, None
    srv.sendto(b"echo:" + d, a)
    return d, a

p1, p2 = player(), player()
p1.sendto(b"hello-from-1", ("127.0.0.1", pub))
d, a1 = serve_once()
ok(d == b"hello-from-1", "server receives player 1's packet unchanged")
ok(p1.recvfrom(4096)[0] == b"echo:hello-from-1", "player 1 receives the server's reply")

p2.sendto(b"hello-from-2", ("127.0.0.1", pub))
d, a2 = serve_once()
ok(d == b"hello-from-2", "server receives player 2's packet")
ok(a1 != a2, "each player appears to the server as a different source (%s vs %s)" % (a1[1], a2[1]))
ok(p2.recvfrom(4096)[0] == b"echo:hello-from-2", "player 2 gets only its own reply")
p1.settimeout(0.3)
try:
    p1.recvfrom(4096); leaked = True
except socket.timeout:
    leaked = False
ok(not leaked, "player 1 did not receive player 2's reply")

big = os.urandom(1400)
p1.sendto(big, ("127.0.0.1", pub))
d, _ = serve_once()
ok(d == big, "1400-byte binary payload survives intact")
ok(p1.recvfrom(4096)[0] == b"echo:" + big, "and its 1400-byte reply comes back intact")

# wrong secret must not be able to hijack the room
ev = socket.socket(socket.AF_INET, socket.SOCK_DGRAM); ev.settimeout(1.0)
ev.sendto(b"H|testroom|wrongpass", ("127.0.0.1", 29000))
ok(ev.recvfrom(4096)[0].startswith(b"ERR|"), "hello with the wrong secret is rejected")
# a stranger sending 'D' frames must be ignored
ev.sendto(b"D\x00\x00\x00\x01" + b"spoof", ("127.0.0.1", 29000))
p1.settimeout(0.4)
try:
    p1.recvfrom(4096); spoofed = True
except socket.timeout:
    spoofed = False
ok(not spoofed, "data frames from a non-host address are ignored")
# room lock: existing player keeps playing, a new one is refused, unlocking admits it again
open(LOCK, "w").close()
time.sleep(6.5)                              # agent re-sends its hello every 5 s
p1.settimeout(1.0)
p1.sendto(b"still-playing", ("127.0.0.1", pub))
d, _ = serve_once()
ok(d == b"still-playing", "locked room: a player that is already connected keeps playing")
ok(p1.recvfrom(4096)[0] == b"echo:still-playing", "locked room: and gets its replies")
p3 = player(); p3.settimeout(0.5)
p3.sendto(b"late-joiner", ("127.0.0.1", pub))
d, _ = serve_once()
ok(d is None, "locked room: a NEW player's packets never reach the server")
os.remove(LOCK)
time.sleep(6.5)
p3.sendto(b"late-joiner-2", ("127.0.0.1", pub))
d, _ = serve_once()
ok(d == b"late-joiner-2", "after unlocking the new player is admitted")
for p in procs: p.kill()
print("ALL PASSED")
