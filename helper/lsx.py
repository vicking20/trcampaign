#!/usr/bin/env python3
"""Local stand-in for the Origin client's LSX service (127.0.0.1:3216).

NFS The Run's EbisuSDK layer talks NUL-terminated XML to it. If nothing
answers, the game says you must be signed in to Origin. This pretends you are.
Runs on each player's own machine (the game connects to localhost).
"""
import socket, sys, threading, time, re, secrets, configparser
import xml.etree.ElementTree as ET

PORT = 3216
import os, re as _re
HERE = os.path.dirname(os.path.abspath(__file__))

def setup(default_host=""):
    """First run (--setup [host]): ask for whatever Host= / Name= the ini still leaves empty. Used by the launchers."""
    ini = os.path.join(HERE, "NFSTR_Online.ini")
    if not os.path.isfile(ini): return
    text = open(ini, encoding="utf-8").read()
    def value(key):
        m = re.search(r"^%s=(.*)$" % key, text, re.M | re.I); return m.group(1).strip() if m else None
    def put(key, val):
        nonlocal text; text = re.sub(r"^(%s=).*$" % key, lambda m: m.group(1) + val, text, count=1, flags=re.M | re.I)
    if value("Host") == "":
        host = default_host
        while not host: host = input("Address of the host (ask them; for example 192.168.1.20): ").strip()
        put("Host", host)
    if value("Name") == "":
        name = ""
        while not _re.fullmatch(r"[A-Za-z0-9_-]{3,16}", name): name = input("Your player name (3-16 letters, digits, _ or -): ").strip()
        put("Name", name)
    open(ini, "w", encoding="utf-8").write(text)

if __name__ == "__main__" and "--setup" in sys.argv:
    try: setup(sys.argv[sys.argv.index("--setup") + 1] if len(sys.argv) > sys.argv.index("--setup") + 1 else "")
    except (EOFError, KeyboardInterrupt): sys.exit(1)
    sys.exit(0)

def _player_name():
    """Player name: NFSTR_NAME, player_name.txt, [Player] Name in the adjacent ini, or TRPlayer."""
    n = os.environ.get("NFSTR_NAME", "")
    f = os.path.join(os.path.dirname(os.path.abspath(__file__)), "player_name.txt")
    if not n and os.path.isfile(f):
        n = open(f, encoding="utf-8").read().strip()
    if not n:
        ini = os.path.join(os.path.dirname(os.path.abspath(__file__)), "NFSTR_Online.ini")
        cfg = configparser.ConfigParser()
        if cfg.read(ini, encoding="utf-8"):
            n = cfg.get("Player", "Name", fallback="")
    n = _re.sub(r"[^A-Za-z0-9_-]", "", n)[:16]
    return n or "TRPlayer"

def _ini(section, key, default=""):
    cfg = configparser.ConfigParser()
    cfg.read([os.path.join(HERE, "NFSTR_Online.ini"), "NFSTR_Online.ini"], encoding="utf-8")   # next to this script, then the working directory
    return cfg.get(section, key, fallback=default).strip()

# Password login (the emulator server): ini [Server] Host, optional LoginPort (8443) and Pin (sha256 of the server's
# certificate, shown by the server at start), [Player] Name and Password. Without a Password the old name token is used.
PASSWORD = os.environ.get("NFSTR_PASSWORD", "") or _ini("Player", "Password")
LOGIN_HOST = _ini("Server", "Host", "127.0.0.1")
LOGIN_PORT = int(_ini("Server", "LoginPort", "8443") or 8443)
LOGIN_PIN = _ini("Server", "Pin").lower().replace(":", "")

def fetch_game_code():
    """Name + password -> one-time game code from POST /nfsrun/auth/password (HTTPS). Returns (code, error)."""
    import ssl, json, hashlib, http.client
    ctx = ssl.create_default_context(); ctx.check_hostname = False; ctx.verify_mode = ssl.CERT_NONE   # self-signed; pinned below
    try:
        c = http.client.HTTPSConnection(LOGIN_HOST, LOGIN_PORT, timeout=10, context=ctx)
        c.connect()
        if LOGIN_PIN:
            got = hashlib.sha256(c.sock.getpeercert(binary_form=True)).hexdigest()
            if got != LOGIN_PIN: c.close(); return None, "server certificate does not match Pin (got %s)" % got
        c.request("POST", "/nfsrun/auth/password", json.dumps({"name": PERSONA, "password": PASSWORD}),
                  {"Content-Type": "application/json", "User-Agent": "nfsrun-launcher/ini-1"})
        r = c.getresponse(); body = json.loads(r.read() or b"{}"); c.close()
        if r.status == 200 and body.get("code"): return body["code"], None
        return None, "%s %s" % (r.status, body.get("message") or body.get("error") or "")
    except (OSError, ValueError) as e:
        return None, "%s: %s" % (type(e).__name__, e)

PERSONA = _player_name()
# Same formula as server/trserver.py _pid(); the name travels to the Blaze server inside the Origin auth token (hex of the name).
USER_ID = "1000000001" if PERSONA == "TRPlayer" else str(2000000001 + sum(PERSONA.encode()) % 1000)
LOG = open("lsx.log", "a", buffering=1)

def log(*a):
    s = time.strftime("[%H:%M:%S] ") + " ".join(str(x) for x in a)
    print(s, flush=True); LOG.write(s + "\n")


# ---- LSX challenge: AES-128-ECB, key 00..0f, zero padded to ((len>>4)+1)*16, hex ----
def _aes_tables():
    sbox = [0] * 256
    p = q = 1
    while True:
        p = p ^ ((p << 1) & 0xFF) ^ (0x1B if p & 0x80 else 0)
        q ^= q << 1; q ^= q << 2; q ^= q << 4; q &= 0xFF
        if q & 0x80: q ^= 0x09
        x = q ^ (q << 1 | q >> 7) & 0xFF ^ (q << 2 | q >> 6) & 0xFF ^ (q << 3 | q >> 5) & 0xFF ^ (q << 4 | q >> 4) & 0xFF
        sbox[p] = (x ^ 0x63) & 0xFF
        if p == 1: break
    sbox[0] = 0x63
    return sbox
_SBOX = _aes_tables()

def _xt(a): return ((a << 1) ^ 0x1B) & 0xFF if a & 0x80 else a << 1

def _expand(key):
    w = [list(key[i:i + 4]) for i in range(0, 16, 4)]
    rcon = 1
    for i in range(4, 44):
        t = list(w[i - 1])
        if i % 4 == 0:
            t = t[1:] + t[:1]
            t = [_SBOX[b] for b in t]
            t[0] ^= rcon
            rcon = _xt(rcon)
        w.append([w[i - 4][j] ^ t[j] for j in range(4)])
    return [sum(w[r * 4:r * 4 + 4], []) for r in range(11)]

def _aes_block(rk, blk):
    s = [blk[i] ^ rk[0][i] for i in range(16)]
    for rnd in range(1, 11):
        s = [_SBOX[b] for b in s]
        s = [s[(i + 4 * (i % 4)) % 16] for i in range(16)]            # ShiftRows
        if rnd != 10:
            n = []
            for c in range(4):
                a = s[c * 4:c * 4 + 4]
                n += [_xt(a[0]) ^ (_xt(a[1]) ^ a[1]) ^ a[2] ^ a[3],
                      a[0] ^ _xt(a[1]) ^ (_xt(a[2]) ^ a[2]) ^ a[3],
                      a[0] ^ a[1] ^ _xt(a[2]) ^ (_xt(a[3]) ^ a[3]),
                      (_xt(a[0]) ^ a[0]) ^ a[1] ^ a[2] ^ _xt(a[3])]
            s = n
        s = [s[i] ^ rk[rnd][i] for i in range(16)]
    return bytes(s)

_RK = _expand(bytes(range(16)))

_INV = [0] * 256
for _i, _v in enumerate(_SBOX): _INV[_v] = _i

def _mul(a, b):
    r = 0
    while b:
        if b & 1: r ^= a
        a = _xt(a) & 0xFF; b >>= 1
    return r

def _aes_block_dec(rk, blk):
    s = [blk[i] ^ rk[10][i] for i in range(16)]
    for rnd in range(9, -1, -1):
        s = [s[(i - 4 * (i % 4)) % 16] for i in range(16)]            # InvShiftRows
        s = [_INV[b] for b in s]
        s = [s[i] ^ rk[rnd][i] for i in range(16)]
        if rnd:
            n = []
            for c in range(4):
                a = s[c * 4:c * 4 + 4]
                n += [_mul(a[0], 14) ^ _mul(a[1], 11) ^ _mul(a[2], 13) ^ _mul(a[3], 9),
                      _mul(a[0], 9) ^ _mul(a[1], 14) ^ _mul(a[2], 11) ^ _mul(a[3], 13),
                      _mul(a[0], 13) ^ _mul(a[1], 9) ^ _mul(a[2], 14) ^ _mul(a[3], 11),
                      _mul(a[0], 11) ^ _mul(a[1], 13) ^ _mul(a[2], 9) ^ _mul(a[3], 14)]
            s = n
    return bytes(s)

def encrypt_hex(text):
    data = text.encode() if isinstance(text, str) else text
    pad = 16 - len(data) % 16
    data += bytes([pad]) * pad
    return b"".join(_aes_block(_RK, data[i:i + 16]) for i in range(0, len(data), 16)).hex()

def decrypt_hex(h):
    raw = bytes.fromhex(h)
    out = b"".join(_aes_block_dec(_RK, raw[i:i + 16]) for i in range(0, len(raw), 16))
    pad = out[-1]
    return out[:-pad].decode("utf-8", "replace") if 1 <= pad <= 16 else out.decode("utf-8", "replace")

challenge_response = encrypt_hex

def wrap(kind, sender, rid, inner, enc=False):
    idattr = f' id="{rid}"' if rid is not None else ""
    xml = f'<LSX><{kind} sender="{sender}"{idattr}>{inner}</{kind}></LSX>'
    return (encrypt_hex(xml) if enc else xml).encode() + b"\x00"

def answer(child, rid, recipient):
    """Build the (encrypted) response for one LSX request. Attribute names
    come from the schema table inside the game exe (all use version="2")."""
    tag = child.tag
    def resp(inner, enc=True): return wrap("Response", recipient or "EALS", rid, inner, enc)
    if tag == "ChallengeResponse":
        return resp('<ChallengeAccepted response="%s"/>' % challenge_response(child.get("key", "")), enc=False)
    if tag == "GetConfig":
        return resp('<GetConfigResponse version="2"/>')
    if tag == "GetProfile":
        return resp(f'<GetProfileResponse version="2" UserId="{USER_ID}" Persona="{PERSONA}" AvatarId="0"/>')
    if tag == "GetInternetConnectedState":
        try: mode = open("/tmp/claude-1000/lsx_mode.txt").read().strip()
        except OSError: mode = "1"
        log("   [ICS mode=%r]" % mode)
        if mode == "plain":
            return resp('<InternetConnectedState version="2" connected="1"/>')
        if mode == "resp":
            return resp('<GetInternetConnectedStateResponse version="2" connected="1"/>')
        if mode == "cap":
            return resp('<InternetConnectedState version="2" connected="True"/>')
        if mode == "up":
            return resp('<InternetConnectedState version="2" connected="TRUE"/>')
        if mode == "yes":
            return resp('<InternetConnectedState version="2" connected="yes"/>')
        if mode == "int":
            return resp('<InternetConnectedState version="2" connected="1" connection="1"/>')
        return resp('<InternetConnectedState version="2" connected="%s"/>' % mode)
    if tag in ("GetAuthToken", "GetAuthCode") and PASSWORD:
        code, err = fetch_game_code()
        if err: log("password login failed:", err)
        el = "AuthCode" if tag == "GetAuthCode" else "AuthToken"
        if code: log("%s -> one-time game code issued for %s" % (tag, PERSONA))
        return resp('<%s version="2" value="%s"/>' % (el, code or ""))
    if tag == "GetAuthToken":
        return resp('<AuthToken version="2" value="%s"/>' % PERSONA.encode().ljust(16, b"\0").hex())
    if tag == "CheckPermission":
        return resp('<CheckPermissionResponse version="2" Access="GRANTED"/>')
    if tag == "GetSetting":
        return resp('<GetSettingResponse version="2" Setting=""/>')
    if tag == "QueryFriends":
        return resp('<QueryFriendsResponse version="2"/>')
    if tag == "GetBlockList":
        return resp('<GetBlockListResponse version="2"/>')
    if tag in ("GetPresence", "QueryPresence"):
        return resp(f'<GetPresenceResponse version="2" UserId="{USER_ID}" Presence="ONLINE" Title="" '
                    f'TitleId="" RichPresence="" GamePresence="" SessionId=""/>')
    return resp('<ErrorSuccess version="2" Code="0" Description=""/>')

def handle(c, addr):
    log("LSX client", addr)
    try:
        skey = secrets.token_hex(8)
        log("challenge key", skey, "-> expected client response", challenge_response(skey))
        c.sendall(wrap("Event", "EALS", None, '<Challenge version="3" key="%s"/>' % skey))
        buf = b""
        while True:
            d = c.recv(65536)
            if not d:
                break
            buf += d
            while b"\x00" in buf:
                msg, buf = buf.split(b"\x00", 1)
                txt = msg.decode("utf-8", "replace")
                if txt and not txt.startswith("<"):
                    try: txt = decrypt_hex(txt.strip())
                    except Exception as e: log("   decrypt error", e); continue
                log("<<", txt)
                try:
                    root = ET.fromstring(txt)
                except ET.ParseError as e:
                    log("   parse error", e); continue
                for req in root:
                    rid = req.get("id"); rcp = req.get("recipient")
                    for child in req:
                        out = answer(child, rid, rcp)
                        log(">>", out[:-1].decode())
                        c.sendall(out)
                        if child.tag in ("ChallengeResponse", "GetProfile"):
                            for snd in ("EbisuSDK",):
                                ev2 = wrap("Event", snd, None, '<Login version="2" IsLoggedIn="1"/>', True)
                                log(">> (Login event as %s)" % snd); c.sendall(ev2)
                        if child.tag == "ChallengeResponse":
                            ev = wrap("Event", "EALS", None, '<Login version="2" IsLoggedIn="1"/>', True)
                            log(">>", ev[:-1].decode()); c.sendall(ev)
    except OSError as e:
        log("   error", e)
    finally:
        c.close(); log("LSX closed")

def main():
    s = socket.socket()
    if os.name == "nt": s.setsockopt(socket.SOL_SOCKET, socket.SO_EXCLUSIVEADDRUSE, 1)     # SO_REUSEADDR would let a second helper share the port and split the game's connections
    else: s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
    try: s.bind(("127.0.0.1", PORT))
    except OSError: sys.exit("another sign-in helper is already running on port %d" % PORT)
    s.listen(8)
    log("LSX stub on 127.0.0.1:%d as %s" % (PORT, PERSONA))
    while True:
        c, a = s.accept()
        threading.Thread(target=handle, args=(c, a), daemon=True).start()

if __name__ == "__main__":
    main()
