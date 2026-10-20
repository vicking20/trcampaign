#!/usr/bin/env python3
"""Host-side agent: connects out to the relay and feeds each remote player to the local
NFS The Run server as an ordinary local UDP client.

  python3 agent.py --relay RELAYHOST:26000 --room my-room --secret s3cretpass --server 127.0.0.1:25200 [--lock-file PATH]

While --lock-file exists the agent asks the relay to lock the room (no new players; connected ones keep playing).
"""
import argparse, os, select, socket, struct, sys, time

MAX_PKT = 2048
LANE_IDLE = 60.0
MAX_LANES = 64


DUMP = 0

def log(*a):
    print(time.strftime("[%H:%M:%S]"), *a, flush=True)


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--relay", required=True, help="host:port of the relay control port")
    ap.add_argument("--room", required=True)
    ap.add_argument("--secret", required=True)
    ap.add_argument("--server", default="127.0.0.1:25200", help="the game server's UDP address")
    ap.add_argument("--lock-file", help="while this file exists the room is locked against new players")
    ap.add_argument("--dump", type=int, default=0, help="log the first N packets in each direction")
    a = ap.parse_args()
    global DUMP
    DUMP = a.dump
    rh, rp = a.relay.rsplit(":", 1)
    sh, sp = a.server.rsplit(":", 1)
    relay = (socket.gethostbyname(rh), int(rp))
    server = (sh, int(sp))
    ctrl = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
    hello_open = b"H|%s|%s" % (a.room.encode(), a.secret.encode())
    hello_lock = hello_open + b"|L"
    lanes = {}       # client id -> [sock, last_seen]
    by_sock = {}     # sock -> client id
    last_hello = 0.0
    registered = False
    global_n = [0, 0]

    while True:
        now = time.time()
        if now - last_hello > 5:
            ctrl.sendto(hello_lock if a.lock_file and os.path.exists(a.lock_file) else hello_open, relay); last_hello = now
        rs, _, _ = select.select([ctrl] + list(by_sock), [], [], 1.0)
        for s in rs:
            if s is ctrl:
                data, addr = ctrl.recvfrom(MAX_PKT + 16)
                if addr != relay:
                    continue
                if data[:3] == b"OK|":
                    if not registered:
                        log(f"registered; players connect to {rh}:{data[3:].decode()}")
                        registered = True
                elif data[:4] == b"ERR|":
                    log("relay says:", data[4:].decode()); registered = False
                elif data[:1] == b"D" and len(data) >= 5:
                    cid = struct.unpack(">I", data[1:5])[0]
                    lane = lanes.get(cid)
                    if lane is None:
                        if len(lanes) >= MAX_LANES:
                            continue
                        ls = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
                        # The game server accepts only ONE client per source IP, so every player
                        # lane gets its own loopback address (127.0.0.2, .3, ...).
                        used = {l[2] for l in lanes.values()}
                        octet = next(o for o in range(2, 255) if o not in used) if server[0].startswith("127.") else 1
                        ls.bind(("127.0.0.%d" % octet, 0))
                        lane = [ls, now, octet]
                        lanes[cid] = lane; by_sock[ls] = cid
                        log(f"new player lane {cid} (local port {ls.getsockname()[1]})")
                    lane[1] = now
                    global_n[0] += 1
                    if global_n[0] <= DUMP:
                        log(f"-> server  lane {cid} {len(data)-5}B {data[5:5+64].hex()}")
                    lane[0].sendto(data[5:], server)
            else:
                cid = by_sock.get(s)
                data, _ = s.recvfrom(MAX_PKT + 16)
                if cid is not None and len(data) <= MAX_PKT:
                    global_n[1] += 1
                    if global_n[1] <= DUMP:
                        log(f"<- server  lane {cid} {len(data)}B {data[:64].hex()}")
                    lanes[cid][1] = now
                    ctrl.sendto(b"D" + struct.pack(">I", cid) + data, relay)
        for cid, (ls, seen, _o) in list(lanes.items()):
            if now - seen > LANE_IDLE:
                del lanes[cid]; by_sock.pop(ls, None); ls.close()
                log(f"player lane {cid} idle, closed")


if __name__ == "__main__":
    try:
        main()
    except KeyboardInterrupt:
        pass
