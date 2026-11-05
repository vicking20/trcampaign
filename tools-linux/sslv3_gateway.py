#!/usr/bin/env python3
"""SSLv3 front for the Go server on Linux (replaces legacy_gateway, which needs OpenSSL 1.0.2 on Windows).
Terminates the game's SSLv3 connections with server/sslv3.py and relays plaintext to the Go server.
  42129 (Redirector, from the Go server's :42127 front) -> 127.0.0.1:42128
  18443 (web Autolog, straight from the game)          -> 127.0.0.1:18080
Usage: sslv3_gateway.py [cert.pem key.pem]   (default: certs/protossl_redirector.crt/.key)"""
import os, socket, sys, threading
HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)
import sslv3

ROOT = os.path.join(HERE, "..")
crt = sys.argv[1] if len(sys.argv) > 2 else os.path.join(ROOT, "certs", "protossl_redirector.crt")
key = sys.argv[2] if len(sys.argv) > 2 else os.path.join(ROOT, "certs", "protossl_redirector.key")
KEY, CERT = sslv3.load_rsa_key(key), sslv3.load_cert_der(crt)
LISTENERS = [("127.0.0.1", 42129, "127.0.0.1", 42128, "redirector"), ("0.0.0.0", 18443, "127.0.0.1", 18080, "web-autolog")]

def log(*a): print(*a, flush=True)

def pump_back(back, c):
    try:
        while True:
            d = back.recv(65536)
            if not d: break
            c.send(d)
    except (OSError, ValueError): pass
    try: c.sock.close() if hasattr(c, "sock") else None
    except OSError: pass

def handle(sock, addr, name, bhost, bport):
    back = None
    try:
        sock.settimeout(30)
        c = sslv3.Conn(sock, KEY, CERT, log=lambda *a: None)
        c.handshake()
        back = socket.create_connection((bhost, bport), timeout=10)
        threading.Thread(target=pump_back, args=(back, c), daemon=True).start()
        while True:
            d = c.recv()
            if not d: break
            back.sendall(d)
    except Exception as e:
        log(f"[{name}] {addr[0]}:{addr[1]} {type(e).__name__}: {e}")
    finally:
        for s in (back, sock):
            try: s and s.close()
            except OSError: pass

def serve(host, port, bhost, bport, name):
    srv = socket.socket(); srv.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
    srv.bind((host, port)); srv.listen(32); log(f"[{name}] SSLv3 {host}:{port} -> {bhost}:{bport}")
    while True:
        s, a = srv.accept()
        threading.Thread(target=handle, args=(s, a, name, bhost, bport), daemon=True).start()

for l in LISTENERS[1:]: threading.Thread(target=serve, args=l, daemon=True).start()
serve(*LISTENERS[0])
