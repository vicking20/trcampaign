#!/usr/bin/env python3
"""UDP relay for the NFS The Run LAN server.

Players send raw game UDP to a room's public port. A host agent (agent.py) next to the
server keeps an outbound tunnel to this relay, so the host needs no port forwarding.
The relay never looks inside game packets: it only forwards bytes.

  python3 relay.py --control-port 26000 --room-ports 26001-26100 --bind 0.0.0.0

Control protocol (UDP, host agent -> relay control port):
  b"H|<room>|<secret>[|L]"   register/refresh a room; reply b"OK|<public-port>" or b"ERR|<why>"
                             the optional trailing "L" locks the room: new player addresses are ignored (players already
                             connected keep playing); the next hello without it unlocks again
  b"D" + id(4,BE) + payload  data from the server for client <id> (and the reverse direction)
"""
import argparse, re, select, socket, struct, sys, time

MAX_PKT = 2048
MAX_CLIENTS_PER_ROOM = 64
CLIENT_IDLE = 60.0
HOST_IDLE = 30.0
RATE_PPS = 600          # sustained packets/s per client
RATE_BURST = 1200
ROOM_RE = re.compile(rb"^[A-Za-z0-9_-]{3,32}$")


class Room:
    def __init__(self, code, secret, sock, port):
        self.code, self.secret, self.sock, self.port = code, secret, sock, port
        self.host_addr = None
        self.host_seen = 0.0
        self.by_addr = {}      # client addr -> [id, last_seen, tokens, token_time]
        self.by_id = {}        # id -> client addr
        self.next_id = 1
        self.locked = False
        self.refused = set()   # addresses already logged as refused (log once)


def log(*a):
    print(time.strftime("[%H:%M:%S]"), *a, flush=True)


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--bind", default="0.0.0.0")
    ap.add_argument("--control-port", type=int, default=26000)
    ap.add_argument("--room-ports", default="26001-26100")
    a = ap.parse_args()
    lo, hi = (int(x) for x in a.room_ports.split("-"))
    free_ports = list(range(lo, hi + 1))

    ctrl = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
    ctrl.bind((a.bind, a.control_port))
    log(f"relay control on {a.bind}:{a.control_port}, room ports {lo}-{hi}")

    rooms = {}          # code -> Room
    by_host = {}        # host addr -> Room
    by_sock = {}        # public socket -> Room
    last_gc = time.time()

    def close_room(room, why):
        log(f"room {room.code.decode()} closed ({why})")
        if room.host_addr in by_host:
            del by_host[room.host_addr]
        by_sock.pop(room.sock, None)
        rooms.pop(room.code, None)
        room.sock.close()
        free_ports.append(room.port)

    while True:
        rs, _, _ = select.select([ctrl] + list(by_sock), [], [], 1.0)
        now = time.time()
        for s in rs:
            if s is ctrl:
                data, addr = ctrl.recvfrom(MAX_PKT + 16)
                if not data:
                    continue
                if data[:2] == b"H|":
                    parts = data.split(b"|")
                    lock = len(parts) == 4 and parts[3] == b"L"
                    if len(parts) not in (3, 4) or (len(parts) == 4 and not lock) or not ROOM_RE.match(parts[1]) or not (4 <= len(parts[2]) <= 64):
                        ctrl.sendto(b"ERR|bad hello", addr); continue
                    code, secret = parts[1], parts[2]
                    room = rooms.get(code)
                    if room is None:
                        if not free_ports:
                            ctrl.sendto(b"ERR|no free ports", addr); continue
                        port = free_ports.pop(0)
                        sock = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
                        try:
                            sock.bind((a.bind, port))
                        except OSError as e:
                            free_ports.append(port)
                            ctrl.sendto(b"ERR|port busy", addr); continue
                        room = Room(code, secret, sock, port)
                        rooms[code] = room
                        by_sock[sock] = room
                        log(f"room {code.decode()} created on public port {port}")
                    if room.secret != secret:
                        ctrl.sendto(b"ERR|wrong secret", addr); continue
                    if room.host_addr != addr:
                        if room.host_addr in by_host:
                            del by_host[room.host_addr]
                        room.host_addr = addr
                        by_host[addr] = room
                        log(f"room {code.decode()} host is {addr[0]}:{addr[1]}")
                    room.host_seen = now
                    if room.locked != lock:
                        room.locked = lock
                        room.refused.clear()
                        log(f"room {code.decode()} {'LOCKED: new players are refused' if lock else 'unlocked'}")
                    ctrl.sendto(b"OK|%d" % room.port, addr)
                elif data[:1] == b"D" and len(data) >= 5:
                    room = by_host.get(addr)
                    if not room:
                        continue
                    room.host_seen = now
                    cid = struct.unpack(">I", data[1:5])[0]
                    caddr = room.by_id.get(cid)
                    if caddr and len(data) - 5 <= MAX_PKT:
                        room.sock.sendto(data[5:], caddr)
                # anything else is ignored
            else:
                room = by_sock.get(s)
                if room is None:
                    continue
                data, caddr = s.recvfrom(MAX_PKT + 16)
                if len(data) > MAX_PKT or room.host_addr is None:
                    continue
                c = room.by_addr.get(caddr)
                if c is None:
                    if room.locked:
                        if caddr not in room.refused and len(room.refused) < 256:
                            room.refused.add(caddr)
                            log(f"room {room.code.decode()} locked: refused new player {caddr[0]}:{caddr[1]}")
                        continue
                    if len(room.by_addr) >= MAX_CLIENTS_PER_ROOM:
                        continue
                    cid = room.next_id; room.next_id += 1
                    c = [cid, now, RATE_BURST, now]
                    room.by_addr[caddr] = c
                    room.by_id[cid] = caddr
                    log(f"room {room.code.decode()} client {cid} from {caddr[0]}:{caddr[1]}")
                # token bucket
                c[2] = min(RATE_BURST, c[2] + (now - c[3]) * RATE_PPS); c[3] = now
                if c[2] < 1:
                    continue
                c[2] -= 1
                c[1] = now
                ctrl.sendto(b"D" + struct.pack(">I", c[0]) + data, room.host_addr)

        if now - last_gc > 5:
            last_gc = now
            for room in list(rooms.values()):
                if now - room.host_seen > HOST_IDLE:
                    close_room(room, "host went silent"); continue
                for caddr, c in list(room.by_addr.items()):
                    if now - c[1] > CLIENT_IDLE:
                        del room.by_addr[caddr]; room.by_id.pop(c[0], None)
                        log(f"room {room.code.decode()} client {c[0]} idle, removed")


if __name__ == "__main__":
    try:
        main()
    except KeyboardInterrupt:
        pass
