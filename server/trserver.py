#!/usr/bin/env python3
"""Local 'Blaze' server for NFS The Run.

usage: trserver.py [port] [advertise-host]
 port            listen port (default 42127, the Blaze redirector port)
 advertise-host  address handed to clients as "the real server" (default 127.0.0.1)
Put the address of this machine in Scripts/NFSTR_Online.ini (Host=...).
"""
import json, os, socket, struct, sys, threading, time
from dataclasses import dataclass
import sslv3, blaze
from blaze import T_INT, T_STR, T_STRUCT, T_UNION, T_LIST, T_MAP, T_BLOB, Union

HERE = os.path.dirname(os.path.abspath(__file__))
PORT = int(sys.argv[1]) if len(sys.argv) > 1 else 42127
ADV_CONFIG = sys.argv[2] if len(sys.argv) > 2 else "auto"
ADV_HOST = ADV_CONFIG                  # kept for the start-up log; use adv() everywhere else
AUTOLOG_PORT = int(os.environ.get("TR_AUTOLOG_PORT", "8081"))
AUTOLOG_NEWS_PATH = os.environ.get("TR_AUTOLOG_NEWS_PATH", os.path.join(HERE, "autolog_news.json"))
def _ensure_key():
    """First start: make the fake service's RSA key and certificate (they only exist so the game's TLS handshake completes)."""
    k, c = os.path.join(HERE, "key.pem"), os.path.join(HERE, "cert.pem")
    if os.path.isfile(k) and os.path.isfile(c): return
    import subprocess
    try:
        subprocess.check_call(["openssl", "genrsa", "-traditional", "-out", k, "1024"], stderr=subprocess.DEVNULL, stdout=subprocess.DEVNULL)
    except (OSError, subprocess.CalledProcessError):             # no openssl (Windows): generate in pure Python
        sys.path.insert(0, HERE); import keygen; keygen.generate_key(k)
    subprocess.check_call([sys.executable, os.path.join(HERE, "mkcert.py")], stdout=subprocess.DEVNULL)

_ensure_key()
KEY = sslv3.load_rsa_key(os.path.join(HERE, "key.pem"))
CERT = sslv3.load_cert_der(os.path.join(HERE, "cert.pem"))
sslv3.RAWLOG = False

def log(*a):
    print(time.strftime("[%H:%M:%S]"), *a, flush=True)

def adv():
    """The address handed to a client as "this server". 'auto' (default) = the server-side address of the connection that client used,
    so LAN, VPN (Tailscale) and loopback players each get an address they can actually reach. Or a fixed address/name from the command line."""
    if ADV_CONFIG != "auto": return ADV_CONFIG
    try: return session().local or "127.0.0.1"
    except Exception: return "127.0.0.1"

_resolved = {}                      # host name -> (ip, resolved at); re-resolved every 5 minutes so a dynamic-DNS name follows IP changes
RESOLVE_TTL = 300

def ip_int(h):
    """IPv4 address of a dotted quad or a host name (a domain as advertise-host is resolved, cached for RESOLVE_TTL seconds)."""
    try: return struct.unpack(">I", socket.inet_aton(h))[0]
    except OSError:
        hit = _resolved.get(h)
        if not hit or time.time() - hit[1] > RESOLVE_TTL:
            try: ip = struct.unpack(">I", socket.inet_aton(socket.gethostbyname(h)))[0]
            except OSError: ip = hit[0] if hit else 0x7F000001
            _resolved[h] = hit = (ip, time.time())
        return hit[0]

# ---------------------------------------------------------------- handlers
# each handler(req: Struct) -> list of (tag, type, value) or None for "empty reply"
def redirector_get_server_instance(req):
    addr = [("HOST", T_STR, adv()), ("IP", T_INT, ip_int(adv())), ("PORT", T_INT, PORT)]
    return [("ADDR", T_UNION, Union(0, "VALU", (T_STRUCT, addr))),
            ("SECU", T_INT, 1),
            ("XDNS", T_INT, 0)]

# ---- Util
SERVER_VER = "Blaze 3.15.2.0"
PING_SITE_ALIAS = "sjc"  # Dedicated-server default; validated before createGame.
# advertise a wide set of component ids: the client only creates components it is told about
ALL_COMPONENTS = sorted(set(list(range(1, 0x50)) + list(range(0x800, 0x820)) + list(range(0x7800, 0x7810)) + [0x801, 0x7802]))
PERSONA_ID = 1000000001
USER_ID = 1000000001

@dataclass
class Session:
    name: str = "TRPlayer"
    address: tuple | None = None
    created_game: dict | None = None
    joined_gid: int | None = None
    push: object = None
    peer: str = "127.0.0.1"
    local: str = "127.0.0.1"      # server-side address of this connection

    @property
    def persona_id(self):
        return _pid(self.name)

# Finalized dedicated game shared between connections (set by the server's thread, read by players' threads)
DEDICATED = None
def push_mode():
    # experiment switch, read each time: tools/round.sh push off|a|b|full
    # off = nothing; a = UserAdded + PlayerJoining; b = same with PNET unset; full = a + PlayerJoinCompleted (full crashed the server at 0x0103DF53)
    try:
        return open("/tmp/nfstr_round/push_mode").read().strip() or "off"
    except OSError:
        return "off"
_local = threading.local()

def session():
    if not hasattr(_local, "session"):
        _local.session = Session()
    return _local.session

def util_pre_auth(req):
    qos = [("PSA", T_STR, adv()), ("PSP", T_INT, 17502), ("SNA", T_STR, "trqos")]
    conf = {"pingPeriod": "15s", "voipHeadsetUpdateRate": "1000", "xlspConnectionIdleTimeout": "300"}
    return [("ANON", T_INT, 0),
            ("ASRC", T_STR, "303107"),
            ("CIDS", T_LIST, (T_INT, ALL_COMPONENTS)),
            ("CNGN", T_STR, ""),
            ("CONF", T_STRUCT, [("CONF", T_MAP, (T_STR, T_STR, conf))]),
            ("INST", T_STR, "nfs-therun-pc"),
            ("MINR", T_INT, 0),
            ("NASP", T_STR, "cem_ea_id"),
            ("PILD", T_STR, ""),
            ("PLAT", T_STR, "pc"),
            ("PTAG", T_STR, ""),
            ("QOSS", T_STRUCT, [("BWPS", T_STRUCT, qos), ("LNP", T_INT, 10),
                                # Dedicated-server startup validates its default alias "sjc" against
                                # this map (013CB080); "ea-sjc" makes it request shutdown.
                                ("LTPS", T_MAP, (T_STR, T_STRUCT, {PING_SITE_ALIAS: qos})),
                                ("SVID", T_INT, 1161889797)]),
            ("RSRC", T_STR, "303107"),
            ("SVER", T_STR, SERVER_VER)]

def util_ping(req):
    return [("STIM", T_INT, int(time.time()))]

def util_post_auth(req):
    return [("PSS", T_STRUCT, [("ADRS", T_STR, ""), ("AMAX", T_INT, 0), ("OIDS", T_LIST, (T_INT, [])),
                               ("OMAX", T_INT, 0), ("PORT", T_INT, 0), ("RPRT", T_INT, 0), ("TIID", T_INT, 0)]),
            ("TELE", T_STRUCT, [("ADRS", T_STR, ""), ("ANON", T_INT, 0), ("DISA", T_STR, "*"),
                                ("FILT", T_STR, ""), ("LOC", T_INT, 1701726018), ("NOOK", T_STR, ""),
                                ("PORT", T_INT, 0), ("SDLY", T_INT, 15000), ("SESS", T_STR, "trsession"),
                                ("SKEY", T_STR, ""), ("SPCT", T_INT, 0), ("STIM", T_STR, "")]),
            ("TICK", T_STRUCT, [("ADRS", T_STR, ""), ("PORT", T_INT, 0), ("SKEY", T_STR, "")]),
            ("UROP", T_STRUCT, [("TMOP", T_INT, 0), ("UID", T_INT, session().persona_id)])]

def util_fetch_config(req):
    conf = {"V8ServerName": adv(), "V8XlspName": adv(), "V8XlspPort": str(AUTOLOG_PORT),
            "OnlinePassKillSwitch": "false"}
    log("  fetchClientConfig ->", conf)
    return [("CONF", T_MAP, (T_STR, T_STR, conf))]

# ---- Authentication
def _pid(name):
    return PERSONA_ID if name == "TRPlayer" else 2000000001 + (sum(name.encode()) % 1000)

def _persona(name):
    return [("DSNM", T_STR, name), ("LAST", T_INT, int(time.time())), ("PID", T_INT, _pid(name)),
            ("STAS", T_INT, 0), ("XREF", T_INT, 0), ("XTYP", T_INT, 0)]

def _session(mail, name):
    # SessionInfo's binary TDF schema (0x026471D8) contains PDTL, not NAME.
    # The SDK needs the nested persona ID/name to create its local user.
    return [("BUID", T_INT, _pid(name)), ("FRST", T_INT, 0), ("KEY", T_STR, "trsessionkey"),
            ("LLOG", T_INT, int(time.time())), ("MAIL", T_STR, mail), ("PDTL", T_STRUCT, _persona(name)),
            ("UID", T_INT, _pid(name))]

def _record_player(name):
    """Names that logged in since this server started; tools/online_scoring.py only scores these (memory scans see stray strings)."""
    try:
        f = os.path.join(HERE, "..", "recon", "runs", "players.json")
        os.makedirs(os.path.dirname(f), exist_ok=True)
        names = json.load(open(f)) if os.path.isfile(f) else []
        if name not in names: json.dump(names + [name], open(f, "w"))
    except Exception as e:
        log("  could not record player name:", repr(e))

def auth_express_login(req):
    name = "TRPlayer" if "serv" not in str(req.get("MAIL", "")) else "nexus001"
    try:                    # the Origin stand-in (lsx.py) puts the player's name into the auth token: hex of up to 16 bytes
        tn = bytes.fromhex(str(req.get("TOKN", ""))).rstrip(b"\0").decode("ascii")
        if name == "TRPlayer" and 3 <= len(tn) <= 16 and all(c.isalnum() or c in "_-" for c in tn): name = tn
    except ValueError:
        pass
    if name == "TRPlayer" and session().peer not in ("127.0.0.1", "::1", session().local):     # a client on another machine gets its own persona
        name = "TRPlayer" + session().peer.rsplit(".", 1)[-1]
    session().name = name
    if name in banned_names():
        log(f"  banned player {name} refused")
        raise ConnectionAbortedError("banned")
    al = allowlist()
    if al is not None and name.lower() not in al and name != "nexus001" and session().peer not in ("127.0.0.1", "::1", session().local):
        log(f"  {name} is not on the allowlist: refused")
        raise ConnectionAbortedError("not allowlisted")
    ONLINE[session().persona_id] = {"name": name, "peer": session().peer, "since": time.time(), "session": session()}
    _record_player(name)
    # ExpressLogin decodes LoginResponse (vtable 0x0264A19C), not
    # FullLoginResponse. A nested SESS is ignored, leaving the user ID zero.
    return [("LDHT", T_STR, ""), ("NTOS", T_INT, 0), ("PCTK", T_STR, "trtoken"),
            ("PLST", T_LIST, (T_STRUCT, [_persona(name)])), ("PRIV", T_STR, ""),
            ("SKEY", T_STR, "trsessionkey"), ("SPAM", T_INT, 0), ("THST", T_STR, ""),
            ("TSUI", T_STR, ""), ("TURI", T_STR, ""), ("UID", T_INT, session().persona_id)]

def auth_create_persona(req):
    return [("DSNM", T_STR, req.get("PNAM") or "TRPlayer"), ("LAST", T_INT, int(time.time())),
            ("PID", T_INT, PERSONA_ID), ("STAS", T_INT, 0), ("XREF", T_INT, 0), ("XTYP", T_INT, 0)]

def auth_login_persona(req):
    session().name = req.get("PNAM") or session().name
    # LoginPersona's callback 01A41880 consumes bare SessionInfo (verified
    # for both the menu player and dedicated server). The old SESS wrapper
    # decoded to BUID 0 and only worked with the old malformed UserAdded.
    return _session("", session().name)

# ---- UserSessions notifications (component 0x7802): pushed after login
def _user_ident(name=None, pid=None):
    name = name or session().name
    pid = pid or session().persona_id
    return [("AID", T_INT, pid), ("ALOC", T_INT, 1701726018), ("EXBB", T_BLOB, b""),
            ("EXID", T_INT, 0), ("ID", T_INT, pid), ("NAME", T_STR, name), ("ORIG", T_INT, pid)]

def _ext_data(address=None):
    addr = Union(0x7F)
    address = address or session().address
    if address:
        e = [("IP", T_INT, address[0]), ("PORT", T_INT, address[1])]
        i = [("IP", T_INT, address[2]), ("PORT", T_INT, address[3])]
        addr = Union(2, "VALU", (T_STRUCT, [("EXIP", T_STRUCT, e), ("INIP", T_STRUCT, i)]))
    # Omit unused optional fields. In particular CVAR is not a LIST in
    # this SDK; a malformed extended-data field discards the following USER.
    return [("ADDR", T_UNION, addr),
            ("HWFG", T_INT, 0),
            ("QDAT", T_STRUCT, [("DBPS", T_INT, 0), ("NATT", T_INT, 0), ("UBPS", T_INT, 0)]),
            ("UATT", T_INT, 0)]


def login_notifications():
    return [(0x7802, 0x0002, [("DATA", T_STRUCT, _ext_data()), ("USER", T_STRUCT, _user_ident())]),
            (0x7802, 0x0001, [("DATA", T_STRUCT, _ext_data()), ("USID", T_INT, session().persona_id)]),
            (0x7802, 0x0005, [("FLGS", T_INT, 3), ("ID", T_INT, session().persona_id)])]

def auth_list_entitlements(req):
    ent = [("DEVI", T_STR, ""), ("GDAY", T_STR, "2012-03-01T00:00Z"), ("GNAM", T_STR, "NFS12PC"),
           ("ID", T_INT, 1), ("ISCO", T_INT, 0), ("PID", T_INT, PERSONA_ID), ("PJID", T_STR, ""),
           ("PRCA", T_INT, 0), ("PRID", T_STR, ""), ("STAT", T_INT, 1), ("STRC", T_INT, 0),
           ("TAG", T_STR, "NFS12PC"), ("TTYP", T_INT, 1), ("UCNT", T_INT, 0), ("VER", T_INT, 0)]
    return [("NLST", T_LIST, (T_STRUCT, [ent]))]

def auth_get_auth_token(req):
    return [("AUTH", T_STR, "trauthtoken")]

def messaging_fetch(req):
    return [("MSGS", T_LIST, (T_STRUCT, []))]

# ---- GameManager (component 4). Tags per the binary / Blaze 3 docs.
MM_ID = int(time.time()) % 1000000 * 10

def gm_start_matchmaking(req):
    # fresh game / matchmaking id per search: the client never leaves the previous game on its own
    global GAME_ID, MM_ID
    MM_ID += 1
    if DEDICATED and DEDICATED["ready"]:
        session().joined_gid = DEDICATED["id"]
        log(f"  joining dedicated game {DEDICATED['id']} (host {DEDICATED['name']}), matchmaking id {MM_ID}")
    else:
        GAME_ID += 1
        session().joined_gid = None
        log(f"  no dedicated game ready: fake game id {GAME_ID}, matchmaking id {MM_ID}")
    return [("MSID", T_INT, MM_ID)]

GAME_ID = int(time.time()) % 1000000 * 10   # unique across server restarts (client keeps stale games)
LAN_PORT = 25200   # game server UDP port advertised to clients (sniffer unit no longer in the path)
# The matchmaker decides which playlist/route a matched game runs; host and clients load the level
# from the *game attributes*. The client asks for this playlist in its own search
# (GameManager.0xd ATTR.playListId), and the map paths are the exe's own level list.
PLAYLIST_ID = os.environ.get("TR_PLAYLIST_ID", "40808F8D-99F3-44D6-AE7F-0F06B2D84FD3")
MAP_PATH = os.environ.get("TR_MAP_PATH", "_c4/Levels/Level_0100_SanFrancisco/Level_0100_SanFrancisco")

PROGRESS = {}   # RouteIndex / SessionIndex written by the host; carried into the next (restarted) server's first game

def _attrs():
    # Attribute keys the level-load code looks up by name ("playListId", "MapPath"), plus the stage progress.
    return {"playListId": PLAYLIST_ID, "MapPath": MAP_PATH, **PROGRESS}

def _ordered_attrs(attrs):
    # SDK lower_bound uses the ASCII case-insensitive comparator 019B2DD0.
    return dict(sorted(attrs.items(), key=lambda item: item[0].lower()))

def _ipp(host, port):
    ip = [("IP", T_INT, ip_int(host)), ("PORT", T_INT, port)]
    return Union(2, "VALU", (T_STRUCT, [("EXIP", T_STRUCT, ip), ("INIP", T_STRUCT, ip)]))

def _hnet():
    # List of NetworkAddress. variant "A": struct-typed list, each element = member byte 2 + EXIP + INIP + end
    ip = [("IP", T_INT, ip_int(adv())), ("PORT", T_INT, LAN_PORT)]
    if os.environ.get("GM_HNET", "A") == "A":
        el = bytes([2]) + blaze._enc_struct([("EXIP", T_STRUCT, ip), ("INIP", T_STRUCT, ip)])
        return (T_STRUCT, [el])
    return (T_UNION, [_ipp(adv(), LAN_PORT)])

def _gm_qos():
    return [("DBPS", T_INT, 0), ("NATT", T_INT, 0), ("UBPS", T_INT, 0)]

def _slot_for(pid):
    # Each remote player needs its own ConnApi slot on the dedicated server (0 = host); a shared slot is rejected with -3.
    if not DEDICATED: return 1
    slots = DEDICATED.setdefault("slots", {})
    if pid not in slots:
        used = set(slots.values())
        slots[pid] = next(n for n in range(1, 64) if n not in used)
    return slots[pid]

def _gm_player(gid=None, pnet_unset=False, name=None, pid=None, addr=None, state=4, slot_id=None):
    # name/pid/addr let the inject helper describe a player that is not the current session
    # (e.g. the host when pushing a full player list to the dedicated server).
    gid = gid or GAME_ID
    a = session().address if addr is None else addr
    pnet = _ipp(adv(), 3659)
    if pnet_unset:
        pnet = Union(0x7F)
    elif a:
        e = [("IP", T_INT, a[0] or ip_int(adv())), ("PORT", T_INT, a[1] or a[3])]
        i = [("IP", T_INT, a[2]), ("PORT", T_INT, a[3])]
        pnet = Union(2, "VALU", (T_STRUCT, [("EXIP", T_STRUCT, e), ("INIP", T_STRUCT, i)]))
    pid = pid or session().persona_id
    if slot_id is None: slot_id = _slot_for(pid)
    return [("BLOB", T_BLOB, b""), ("EXID", T_INT, 0), ("GID", T_INT, gid), ("LOC", T_INT, 1701729619),
            ("NAME", T_STR, name or session().name), ("NQOS", T_STRUCT, _gm_qos()),
            ("PATT", T_MAP, (T_STR, T_STR, {})), ("PID", T_INT, pid),
            # SID is the connection slot. Slot 0 belongs to the topology host;
            # reusing it makes ConnApiAddClient return -3 (occupied slot).
            ("PNET", T_UNION, pnet), ("SID", T_INT, slot_id), ("SLOT", T_INT, 0), ("STAT", T_INT, state),
            ("TEAM", T_INT, 0xFFFF), ("TIDX", T_INT, 0xFFFF), ("TIME", T_INT, int(time.time() * 1000000)),
            ("UID", T_INT, pid)]

def _gm_game(attrs):
    # TDF fields must be in ascending tag order or the client's decoder loses later fields (GID read as 0)
    return sorted([("ADMN", T_LIST, (T_INT, [PERSONA_ID])), ("ATTR", T_MAP, (T_STR, T_STR, attrs)),
            ("CRIT", T_MAP, (T_STR, T_STR, {})), ("GTYP", T_STR, ""), ("MATR", T_MAP, (T_STR, T_STR, {})), ("NRES", T_INT, 0),
            ("CAP", T_LIST, (T_INT, [8, 0])), ("GID", T_INT, GAME_ID), ("GNAM", T_STR, "TRGame"),
            ("GPVH", T_INT, 0), ("GSET", T_INT, 1287), ("GSID", T_INT, GAME_ID), ("GSTA", T_INT, 130),
            ("GVER", T_INT, 2100), ("HNET", T_LIST, _hnet()),
            ("HSES", T_INT, USER_ID), ("IGNO", T_INT, 0), ("MCAP", T_INT, 8),
            ("NQOS", T_STRUCT, _gm_qos()), ("NTOP", T_INT, 1), ("PGID", T_STR, ""),
            ("PGSR", T_BLOB, b""), ("PHST", T_STRUCT, [("HPID", T_INT, PERSONA_ID), ("HSLT", T_INT, 0)]),
            ("PSAS", T_STR, PING_SITE_ALIAS), ("QCAP", T_INT, 0), ("SEED", T_INT, 1),
            ("THST", T_STRUCT, [("HPID", T_INT, PERSONA_ID), ("HSLT", T_INT, 0)]),
            ("UUID", T_STR, "tr-game-1"), ("VOIP", T_INT, 0), ("VSTR", T_STR, "2100"),
            ("XNNC", T_BLOB, b""), ("XSES", T_BLOB, b"")], key=lambda f: f[0])

class Guard:
    """Connection limits for the public ports: total, per client address, and new connections per minute.
    This machine's own address (the host playing / the dedicated server) is exempt."""
    def __init__(self):
        self.lock = threading.Lock(); self.cur = {}; self.recent = {}
        self.max_total = int(os.environ.get("TR_MAX_CONN", "128"))
        self.max_ip = int(os.environ.get("TR_MAX_PER_IP", "12"))
        self.rate = int(os.environ.get("TR_RATE_PER_MIN", "60"))
    def admit(self, ip, local=None):
        if ip in ("127.0.0.1", "::1") or ip == local or (ADV_CONFIG != "auto" and ip == ADV_CONFIG): return True      # this machine itself is exempt
        now = time.time()
        with self.lock:
            hist = [t for t in self.recent.get(ip, []) if now - t < 60]
            if (sum(self.cur.values()) >= self.max_total or self.cur.get(ip, 0) >= self.max_ip or len(hist) >= self.rate):
                self.recent[ip] = hist; return False
            hist.append(now); self.recent[ip] = hist; self.cur[ip] = self.cur.get(ip, 0) + 1
            if len(self.recent) > 4096: self.recent = {k: v for k, v in self.recent.items() if v and now - v[-1] < 60}
            return True
    def release(self, ip):
        with self.lock:
            if ip in self.cur:
                self.cur[ip] -= 1
                if self.cur[ip] <= 0: del self.cur[ip]

GUARD = Guard()
MAX_FRAME_BUFFER = 1 << 20                                    # a client message buffer above 1 MB is dropped
IDLE_TIMEOUT = int(os.environ.get("TR_IDLE_TIMEOUT", "600"))   # clients ping every few seconds; silence this long = dead
ALLOW_PATH = os.path.join(HERE, "..", "recon", "runs", "allowlist.json")   # file present = only these names may log in

def allowlist():
    try: return {n.lower() for n in json.load(open(ALLOW_PATH))}
    except (OSError, ValueError): return None

RUNS_DIR = os.path.join(HERE, "..", "recon", "runs")
ANN_PATH = os.path.join(HERE, "announcements.json")          # admin console announcements, merged into getNews
BAN_PATH = os.path.join(RUNS_DIR, "banned.json")
ONLINE = {}                                                   # persona id -> {"name", "peer", "since", "session"} of logged-in clients

def banned_names():
    try: return set(json.load(open(BAN_PATH)))
    except (OSError, ValueError): return set()

ELIM_PATH = os.path.join(HERE, "..", "recon", "runs", "eliminated.json")
_elim_cache = (0.0, set())

def eliminated_names():
    """Names knocked out by the scoring daemon's elimination modes (tools/online_scoring.py writes the file)."""
    global _elim_cache
    try:
        mt = os.path.getmtime(ELIM_PATH)
        if mt != _elim_cache[0]: _elim_cache = (mt, set(json.load(open(ELIM_PATH))))
    except (OSError, ValueError):
        _elim_cache = (0.0, set())
    return _elim_cache[1]

def _kick(pid, reason=6):
    """Remove one player from the dedicated game (client + dedicated server notified); returns True if they were in it."""
    d = DEDICATED
    if not d or pid not in d.get("members", {}): return False
    push = d["members"].pop(pid)
    d.get("players", {}).pop(pid, None); _announce_leave(d["id"], pid, reason)
    d.get("subscribers", {}).pop(pid, None); d.get("slots", {}).pop(pid, None)
    d.get("mesh_reports", {}).pop(pid, None); d.setdefault("completed", set()).discard(pid)
    fields = [("CNTX", T_INT, 0), ("GID", T_INT, d["id"]), ("PID", T_INT, pid), ("REAS", T_INT, reason)]
    for target in (push, d["push"]):
        try: target(4, 0x0028, fields)
        except Exception as e: log("  kick notify failed:", repr(e))
    return True

def admin_command(req):
    cmd = req.get("cmd")
    if cmd == "players":
        d = DEDICATED or {}
        return {"players": [{"name": v["name"], "peer": v["peer"], "online_s": int(time.time() - v["since"]),
                             "in_game": pid in d.get("members", {})} for pid, v in ONLINE.items()],
                "game": d.get("id"), "dedicated_ready": bool(d.get("ready")), "banned": sorted(banned_names())}
    if cmd in ("kick", "ban", "unban"):
        name = req.get("name", "")
        if cmd == "unban":
            b = banned_names() - {name}; json.dump(sorted(b), open(BAN_PATH, "w")); return {"ok": True}
        if cmd == "ban":
            os.makedirs(RUNS_DIR, exist_ok=True); json.dump(sorted(banned_names() | {name}), open(BAN_PATH, "w"))
        hit = False
        for pid, v in list(ONLINE.items()):
            if v["name"] == name:
                hit = _kick(pid) or hit
        return {"ok": True, "kicked_from_game": hit}
    if cmd in ("allow", "disallow", "allowlist"):
        cur = json.load(open(ALLOW_PATH)) if os.path.isfile(ALLOW_PATH) else None
        name = req.get("name", "")
        if cmd == "allow":
            os.makedirs(RUNS_DIR, exist_ok=True); json.dump(sorted(set(cur or []) | {name}), open(ALLOW_PATH, "w"))
        elif cmd == "disallow" and cur is not None:
            json.dump(sorted(set(cur) - {name}), open(ALLOW_PATH, "w"))
        elif cmd == "allowlist" and req.get("on") is False:
            try: os.remove(ALLOW_PATH)
            except OSError: pass
        elif cmd == "allowlist" and req.get("on") and cur is None:
            os.makedirs(RUNS_DIR, exist_ok=True); json.dump([], open(ALLOW_PATH, "w"))
        cur = json.load(open(ALLOW_PATH)) if os.path.isfile(ALLOW_PATH) else None
        return {"ok": True, "enabled": cur is not None, "names": cur or []}
    if cmd == "announce":
        try: ann = json.load(open(ANN_PATH))
        except (OSError, ValueError): ann = []
        n = (max([a["NewsId"] for a in ann] + [900]) + 1)
        now = time.strftime("%m/%d/%Y ") + str(int(time.strftime("%I"))) + time.strftime(":%M:%S %p")
        ann.insert(0, {"NewsId": n, "NewsIcon": "/news/banner.jpg", "NewsViewed": "NewsViewed_No", "Territory": "", "MetaContent": "",
                       "Subject": req.get("title", "Announcement")[:40], "ShortMessage": req.get("text", "")[:110],
                       "Message": req.get("text", "")[:900], "Author": "Server", "PublishedDate": now})
        json.dump(ann, open(ANN_PATH, "w"), indent=1); return {"ok": True, "id": n}
    if cmd == "announcements":
        try: return {"items": json.load(open(ANN_PATH))}
        except (OSError, ValueError): return {"items": []}
    if cmd == "clear_announcements":
        json.dump([], open(ANN_PATH, "w")); return {"ok": True}
    return {"error": "unknown command"}

def admin_server():
    """Local control port for tools/admin.py (127.0.0.1 only): one JSON request per line, one JSON reply."""
    srv = socket.socket(); srv.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
    try: srv.bind(("127.0.0.1", 42128)); srv.listen(4)
    except OSError as e:
        log("admin port unavailable:", e); return
    while True:
        c, _ = srv.accept()
        try:
            c.settimeout(5)
            data = c.makefile().readline()
            c.sendall((json.dumps(admin_command(json.loads(data))) + "\n").encode())
        except Exception as e:
            try: c.sendall((json.dumps({"error": repr(e)}) + "\n").encode())
            except Exception: pass
        finally:
            c.close()

def _elim_watcher():
    """Remove eliminated players from the dedicated game (same notification as a disconnect), checked every 2 s."""
    while True:
        time.sleep(2)
        try:
            d = DEDICATED
            if not d or not d.get("members"): continue
            out = eliminated_names()
            for pid, push in list(d["members"].items()):
                name = d.get("names", {}).get(pid)
                if name in out and pid != d["pid"]:
                    fields = [("CNTX", T_INT, 0), ("GID", T_INT, d["id"]), ("PID", T_INT, pid), ("REAS", T_INT, 6)]
                    d["members"].pop(pid, None); d.get("subscribers", {}).pop(pid, None); d.get("slots", {}).pop(pid, None)
                    d.get("mesh_reports", {}).pop(pid, None); d.setdefault("completed", set()).discard(pid)
                    for target in (push, d["push"]):
                        try: target(4, 0x0028, fields)
                        except Exception as e: log("  elimination notify failed:", repr(e))
                    log(f"  eliminated player {name} removed from game {d['id']}")
        except Exception as e:
            log("  elimination watcher error:", repr(e))

def gm_found_game():
    if session().name in eliminated_names():           # knocked out: keep the search pending until the season changes
        log(f"  {session().name} is eliminated: matchmaking held")
        return []
    if DEDICATED and DEDICATED["ready"] and session().joined_gid == DEDICATED["id"]:
        return gm_found_dedicated()
    return gm_found_fake()

ROSTER = os.environ.get("TR_ROSTER", "1") == "1"      # tell clients about the other human players (competitors list)

def _roster(gid):
    """PROS for the joining client: itself first, then the other humans already in the game."""
    out = [_gm_player(gid)]
    if ROSTER and DEDICATED:
        for pid, info in DEDICATED.get("players", {}).items():
            if pid != session().persona_id and pid in DEDICATED.get("members", {}):
                out.append(_gm_player(gid, name=info["name"], pid=pid, addr=info["addr"]))
    return out

def _announce_leave(gid, pid, reason=6):
    if not (ROSTER and DEDICATED): return
    for other, push in list(DEDICATED.get("members", {}).items()):
        if other == pid or push is None: continue
        try: push(4, 0x0028, [("CNTX", T_INT, 0), ("GID", T_INT, gid), ("PID", T_INT, pid), ("REAS", T_INT, reason)])
        except Exception as e: log("  leave announce failed:", repr(e))

def _announce_join(gid):
    """Existing members learn about the newcomer (NotifyPlayerJoining + join completed) so their competitor list is complete."""
    if not (ROSTER and DEDICATED): return
    me = session().persona_id
    for pid, push in list(DEDICATED.get("members", {}).items()):
        if pid == me or push is None: continue
        try:
            push(4, 0x0015, [("GID", T_INT, gid), ("PDAT", T_STRUCT, _gm_player(gid))])
            push(4, 0x001E, [("GID", T_INT, gid), ("PID", T_INT, me)])
        except Exception as e:
            log("  roster announce failed:", repr(e))

def gm_found_dedicated():
    # The client must know the host user (nexus001) before it sees a game hosted by it, then:
    # NotifyGameSetup (matchmaking context) -> NotifyMatchmakingFinished with the dedicated GID.
    d = DEDICATED
    gid = d["id"]
    d.setdefault("members", {})[session().persona_id] = session().push
    d.setdefault("names", {})[session().persona_id] = session().name
    d.setdefault("players", {})[session().persona_id] = {"name": session().name, "addr": session().address}
    d.setdefault("subscribers", {})[session().persona_id] = session().push
    if not d.get("assigned"):
        # NotifyGameReset (0x70) assigns the reserved host to a match. Its
        # ReplicatedGameData field is DATA (descriptor 026424F4), not GAME.
        # This calls the host's onGameReset listener and starts its session.
        d["push"](4, 0x70, [("DATA", T_STRUCT, _dedicated_fields(
            gid, d["request"], d["pid"], 130, port=LAN_PORT, attrs=_attrs()))])
        d["assigned"] = True
        log(f"  assigned dedicated game {gid}: NotifyGameReset")
    # Register the remote peer before the client begins its UDP handshake.
    # Waiting for updateMeshConnection delays this until its 10 s timeout.
    _notify_dedicated_player(gid)
    _announce_join(gid)
    srv_user = (0x7802, 0x0002, [("DATA", T_STRUCT, _ext_data(d["address"])),
                                 ("USER", T_STRUCT, _user_ident(d["name"], d["pid"]))])
    reas = Union(3, "VALU", (T_STRUCT, [("FIT", T_INT, 100), ("MAXF", T_INT, 100), ("MSID", T_INT, MM_ID),
                                        ("RSLT", T_INT, 2), ("USID", T_INT, session().persona_id)]))
    # Client attribute lookup uses lower_bound (00827FA0), so the wire map
    # must be sorted. Insertion order playListId/MapPath hides playListId.
    client_attrs = _ordered_attrs({**_attrs(), **d.get("attrs", {})})
    log(f"  NOTIFY client: dedicated game {gid} host {d['name']}")
    return [srv_user,
            (4, 0x0014, [("GAME", T_STRUCT, _dedicated_fields(gid, d["request"], d["pid"], d.get("state", 130), port=LAN_PORT, attrs=client_attrs)),
                         ("PROS", T_LIST, (T_STRUCT, _roster(gid))), ("QUEU", T_LIST, (T_STRUCT, [])),
                         ("REAS", T_UNION, reas)]),
            (4, 0x000A, [("FIT", T_INT, 100), ("GID", T_INT, gid), ("MAXF", T_INT, 100),
                         ("MSID", T_INT, MM_ID), ("RSLT", T_INT, 2), ("USID", T_INT, session().persona_id)])]

def gm_found_fake():
    # Client schema (binary 0x2745410..): NotifyGameSetup (0x14) = GAME, PROS, QUEU, REAS(union).
    # REAS member 0 = DatalessSetupContext {DCTX}. Then NotifyMatchmakingFinished (0x0A).
    return [(4, 0x0014, [("GAME", T_STRUCT, _gm_game({"playListId": "40808F8D-99F3-44D6-AE7F-0F06B2D84FD3"})),
                         ("PROS", T_LIST, (T_STRUCT, [_gm_player()])), ("QUEU", T_LIST, (T_STRUCT, [])),
                         ("REAS", T_UNION, Union(int(os.environ.get("GM_REAS", "3")), "VALU", (T_STRUCT, [("FIT", T_INT, 100), ("MAXF", T_INT, 100), ("MSID", T_INT, MM_ID), ("RSLT", T_INT, 2), ("USID", T_INT, session().persona_id)])))])] + ([] if os.environ.get("GM_ONLY_SETUP", "0") == "1" else [
            (4, 0x000A, [("FIT", T_INT, 100), ("GID", T_INT, GAME_ID), ("MAXF", T_INT, 100),
                         ("MSID", T_INT, MM_ID), ("RSLT", T_INT, 2), ("USID", T_INT, session().persona_id)])])

def gm_create_game(req):
    # CreateGameResponse: GID at +8 (metadata 02642098).
    global GAME_ID
    GAME_ID += 1
    session().created_game = {"id": GAME_ID, "request": req, "state": 1, "ready": False}
    global DEDICATED
    DEDICATED = {"id": GAME_ID, "request": req, "ready": False, "name": session().name,
                 "pid": session().persona_id, "address": session().address, "push": session().push}
    log("  dedicated game created:", GAME_ID, "owner", session().name)
    return [("GID", T_INT, GAME_ID)]

def _dedicated_fields(gid, req, host_pid, state, port=None, attrs=None):
    addresses = []
    for address in req["HNET"][1]:
        # The SDK reports an unknown external endpoint before QoS completes.
        # Advertise this host while retaining the requested internal endpoint.
        internal = address["INIP"]
        external = address["EXIP"]
        fields = [("EXIP", T_STRUCT, [
            ("IP", T_INT, external["IP"] or ip_int(adv())),
            ("PORT", T_INT, port or external["PORT"] or internal["PORT"])]),
            ("INIP", T_STRUCT, [("IP", T_INT, internal["IP"]),
                                ("PORT", T_INT, port or internal["PORT"])])]
        addresses.append(bytes([2]) + blaze._enc_struct(fields))
    fields = {tag: (tag, tp, value) for tag, tp, value in _gm_game(attrs or {})}
    updates = [
        ("ADMN", T_LIST, (T_INT, [])), ("CAP", T_LIST, req["PCAP"]),
        ("GID", T_INT, gid), ("GSID", T_INT, gid),
        ("GNAM", T_STR, req["GNAM"]), ("GSET", T_INT, req["GSET"]),
        ("GSTA", T_INT, state), ("HNET", T_LIST, (T_STRUCT, addresses)),
        ("HSES", T_INT, host_pid), ("MCAP", T_INT, req["PMAX"]),
        ("PHST", T_STRUCT, [("HPID", T_INT, host_pid), ("HSLT", T_INT, 0)]),
        ("THST", T_STRUCT, [("HPID", T_INT, host_pid), ("HSLT", T_INT, 0)]),
        ("NTOP", T_INT, req["NTOP"]), ("PSAS", T_STR, req["GCTR"]),
        ("UUID", T_STR, f"tr-dedicated-{gid}"),
        ("VOIP", T_INT, req["VOIP"]), ("VSTR", T_STR, req["VSTR"]),
    ]
    fields.update((field[0], field) for field in updates)
    return [fields[tag] for tag in sorted(fields)]

def gm_created_game():
    game = session().created_game
    # DatalessSetupContext (027459F4), DCTX=CREATE_GAME_SETUP_CONTEXT (0).
    return login_notifications() + [(4, 0x14, [
        # The playlist attributes must be in the *first* NotifyGameSetup: a later setup change makes the
        # dedicated server destroy the game and exit. TR_PLAYLIST_ID / TR_MAP_PATH select the session.
        ("GAME", T_STRUCT, _dedicated_fields(game["id"], game["request"], session().persona_id, 1, attrs=_attrs())),
        ("PROS", T_LIST, (T_STRUCT, [])), ("QUEU", T_LIST, (T_STRUCT, [])),
        ("REAS", T_UNION, Union(0, "VALU", (T_STRUCT, [("DCTX", T_INT, 0)]))),
    ])]

def gm_finalize_game(req):
    game = session().created_game
    if game is None or req["GID"] != game["id"]:
        raise ValueError("finalizeGameCreation for an unowned game")
    game["state"] = 130  # PRE_GAME; INITIALIZING is no longer needed.
    game["ready"] = True
    if DEDICATED and DEDICATED["id"] == game["id"]:
        DEDICATED["ready"] = True
        DEDICATED["state"] = game["state"]
        DEDICATED["address"] = session().address
    log("  dedicated game finalized:", game["id"])
    return []

def gm_finalized_game():
    game = session().created_game
    return [(4, 0x64, [("GID", T_INT, game["id"]), ("GSTA", T_INT, game["state"])])]

def gm_set_game_attributes(req):
    d = DEDICATED
    if not d or req["GID"] != d["id"]:
        raise ValueError("setGameAttributes for an unknown game")
    attrs = _ordered_attrs(req["ATTR"][2])
    d.setdefault("attrs", {}).update(attrs)
    for k in ("RouteIndex", "SessionIndex"):
        if k in attrs:
            PROGRESS[k] = attrs[k]
            log(f"  stage progress saved: {k}={attrs[k]}")
    session().attribute_change = [("ATTR", T_MAP, (T_STR, T_STR, attrs)),
                                  ("GID", T_INT, d["id"])]
    return []

def _quiet():
    # experiment switch: tools/round.sh quiet on  -> next-stage setup (RouteIndex/SessionIndex change, replay reset) goes to the host only
    return os.path.exists("/tmp/nfstr_round/quiet")

def gm_game_attributes_changed():
    keys = set(session().attribute_change[0][2][2].keys())
    if _quiet() and keys <= {"RouteIndex", "SessionIndex"}:
        log(f"  quiet mode: attribute change {sorted(keys)} not sent to players")
        return [(4, 0x50, session().attribute_change)]
    # SDK local game data changes on NotifyGameAttribChange, not the RPC reply.
    # Echo to the writer and broadcast to the host and matched players.
    return _broadcast_game_notification(0x50, session().attribute_change)

def _broadcast_game_notification(command, fields):
    d = DEDICATED
    recipients = [d["push"], *d.get("members", {}).values()]
    sent = {id(session().push)}
    for push in recipients:
        if id(push) in sent:
            continue
        sent.add(id(push))
        try:
            push(4, command, fields)
        except Exception as e:
            log("  could not broadcast game notification:", repr(e))
    return [(4, command, fields)]

def gm_advance_game_state(req):
    game = session().created_game
    if not game or req["GID"] != game["id"]:
        raise ValueError("advanceGameState for an unowned game")
    game["state"] = req["GSTA"]
    if DEDICATED and DEDICATED["id"] == game["id"]:
        DEDICATED["state"] = game["state"]
    session().game_state_change = [("GID", T_INT, game["id"]),
                                    ("GSTA", T_INT, game["state"])]
    return []

def gm_replay_game(req):
    # replayGame (0x13): the host sets up the next round (it just wrote RouteIndex/SessionIndex via setGameAttributes).
    # Skate3's plain state change made our client leave at once; the dedicated flow uses NotifyGameReset (0x70, field DATA)
    # with the full game data (PRE_GAME + current attributes), sent to the host and every matched player.
    game = session().created_game
    if not game or req["GID"] != game["id"]:
        raise ValueError("replayGame for an unowned game")
    game["state"] = 130
    d = DEDICATED
    if d and d["id"] == game["id"]:
        d["state"] = 130
        attrs = _ordered_attrs({**_attrs(), **d.get("attrs", {})})
        session().game_reset = [("DATA", T_STRUCT, _dedicated_fields(
            game["id"], d["request"], d["pid"], 130, port=LAN_PORT, attrs=attrs))]
        log(f"  replayGame {game['id']}: NotifyGameReset with attrs {attrs}")
    return []

def gm_game_reset_notify():
    if _quiet():
        log("  quiet mode: NotifyGameReset not sent to players")
        return [(4, 0x70, session().game_reset)]
    # replayGame is issued by the dedicated host after it has already advanced
    # RouteIndex and begun its native continuation. Re-sending the full reset
    # to that same host re-enters its MapPath teardown path; after a real race
    # the scenario wrapper may already be cleared, and the native stop path
    # dereferences it. Reset matched clients so they follow the host's route,
    # while letting the host continue from its replayGame request.
    d = DEDICATED
    sent = {id(session().push)}
    if d:
        for push in d.get("members", {}).values():
            if id(push) in sent:
                continue
            sent.add(id(push))
            try:
                push(4, 0x70, session().game_reset)
            except Exception as e:
                log("  could not send replay reset to client:", repr(e))
    log("  replay reset sent to matched clients; host continues natively")
    return []

def gm_game_state_changed():
    return _broadcast_game_notification(0x64, session().game_state_change)

def us_update_network_info(req):
    try:
        v = req["ADDR"].value[1]
        session().address = (v["EXIP"]["IP"], v["EXIP"]["PORT"], v["INIP"]["IP"], v["INIP"]["PORT"])
        log("  client address stored:", session().address)
    except Exception as e:
        log("  could not read client ADDR:", repr(e))
    return []

def us_ext_data_update():
    return [(0x7802, 0x0001, [("DATA", T_STRUCT, _ext_data()), ("USID", T_INT, session().persona_id)])]

def _notify_dedicated_player(gid):
    mode = push_mode()
    if mode != "off" and DEDICATED and DEDICATED["id"] == session().joined_gid and DEDICATED.get("push"):
        try:
            push = DEDICATED["push"]
            push(0x7802, 0x0002, [("DATA", T_STRUCT, _ext_data()), ("USER", T_STRUCT, _user_ident())])
            # PlayerState enum 02641F48: ACTIVE_CONNECTING=2, CONNECTED=4.
            push(4, 0x0015, [("GID", T_INT, gid), ("PDAT", T_STRUCT, _gm_player(gid, pnet_unset=(mode == "b"), state=2))])
            if mode == "full":
                push(4, 0x001E, [("GID", T_INT, gid), ("PID", T_INT, session().persona_id)])
            log(f"  pushed player {session().name} joining to dedicated server (game {gid}, mode {mode})")
        except Exception as e:
            log("  could not notify dedicated server:", repr(e))

def gm_update_mesh_connection(req):
    session().mesh_update = req
    return []

def gm_mesh_update():
    req = session().mesh_update
    gid = req["GID"]
    d = DEDICATED
    if not d or d["id"] != gid:
        return []
    is_host = bool(session().created_game and session().created_game["id"] == gid
                   and session().persona_id == d["pid"])
    if not is_host and session().joined_gid != gid:
        return []
    out = []
    targets = req.get("TARG", (T_STRUCT, []))[1]
    for target in targets:
        if not is_host and target.get("PID") != d["pid"]:
            continue
        pid = target.get("PID") if is_host else session().persona_id
        player_push = d.get("members", {}).get(pid)
        if pid == d["pid"] or player_push is None:
            continue
        reports = d.setdefault("mesh_reports", {}).setdefault(pid, {})
        reports["host" if is_host else "client"] = target.get("STAT") == 2
        completed = d.setdefault("completed", set())
        if not (reports.get("host") and reports.get("client")) or pid in completed:
            continue
        completed.add(pid)
        fields = [("GID", T_INT, gid), ("PID", T_INT, pid)]
        # Complete only the real remote roster entry, never the host persona.
        # Its listener registers the player's ConnApi slot for the engine.
        d["push"](4, 0x1E, fields)
        if is_host:
            player_push(4, 0x1E, fields)
        else:
            out.append((4, 0x1E, fields))
        log(f"  completed connected player {pid} on host and client (game {gid})")
    if os.environ.get("GM_ADVANCE", "0") == "1":
        out.append((4, 0x0064, [("GID", T_INT, gid), ("GSTA", T_INT, 131)]))   # NotifyGameStateChange -> IN_GAME (experiment)
    return out

def util_telemetry(req):
    return [("ADRS", T_STR, adv()), ("ANON", T_INT, 0), ("DISA", T_STR, ""), ("FILT", T_STR, ""),
            ("LOC", T_INT, 1701729619), ("NOOK", T_STR, "US,CA,MX"), ("PORT", T_INT, 9946),
            ("SDLY", T_INT, 15000), ("SESS", T_STR, "trtele"), ("SKEY", T_STR, "trtele"),
            ("SPCT", T_INT, 75), ("STIM", T_STR, "Default")]


def gr_submit_report(req):
    # GameReporting.submitGameReport (component 0x1E, cmd 1): capture the race report for the modes layer.
    # NFS The Run builds Blaze::GameReporting::NFS::Run::RunRace reports; schema is learned from the first real capture.
    import json
    try:
        os.makedirs(os.path.join(HERE, "..", "recon", "runs", "gamereports"), exist_ok=True)
        with open(os.path.join(HERE, "..", "recon", "runs", "gamereports", "reports.jsonl"), "a") as f:
            f.write(json.dumps({"t": time.time(), "from": session().name, "req": repr(req)}) + "\n")
        log("  game report captured (recon/runs/gamereports/reports.jsonl)")
    except Exception as e:
        log("  game report capture failed:", repr(e))
    return []

def gm_remove_player(req):
    # removePlayer (0x0B): the client leaves its game (e.g. REAS 6 after failing to connect). Remember the request so
    # the matching NotifyPlayerRemoved can be sent; without it the client keeps the stale game and answers every
    # new search with "no match found".
    session().last_remove = req
    return []

def gm_player_removed():
    req = getattr(session(), "last_remove", None) or {}
    gid = req.get("GID") or session().joined_gid or GAME_ID
    session().joined_gid = None
    if DEDICATED and DEDICATED["id"] == gid:
        pid = req.get("PID") or session().persona_id
        DEDICATED.get("members", {}).pop(pid, None)
        DEDICATED.get("slots", {}).pop(pid, None)
        DEDICATED.get("players", {}).pop(pid, None)
        _announce_leave(gid, pid, req.get("REAS", 6))
        DEDICATED.get("mesh_reports", {}).pop(pid, None)
        DEDICATED.get("completed", set()).discard(pid)
    fields = [("CNTX", T_INT, 0), ("GID", T_INT, gid), ("PID", T_INT, req.get("PID") or session().persona_id), ("REAS", T_INT, req.get("REAS", 0))]
    if push_mode() != "off" and DEDICATED and DEDICATED["id"] == gid and DEDICATED.get("push"):
        try:
            DEDICATED["push"](4, 0x0028, fields)
            log(f"  pushed player removal to dedicated server (game {gid})")
        except Exception as e:
            log("  could not notify dedicated server of removal:", repr(e))
    log(f"  player removed from game {gid}")
    return [(4, 0x0028, fields)]

POST = {
    (0x0004, 0x0013): gm_game_reset_notify,
    (0x0004, 0x0003): gm_game_state_changed,
    (0x0004, 0x0007): gm_game_attributes_changed,
    (0x0004, 0x000B): gm_player_removed,
    (0x0004, 0x000F): gm_finalized_game,
    (0x0001, 0x006E): login_notifications,
    (0x0004, 0x0001): gm_created_game,
    (0x0004, 0x001D): gm_mesh_update,
    (0x7802, 0x0014): us_ext_data_update,
    (0x0004, 0x000D): gm_found_game,
}

HANDLERS = {
    (0x0004, 0x0013): gm_replay_game,
    (0x0004, 0x0003): gm_advance_game_state,
    (0x0004, 0x001D): gm_update_mesh_connection,
    (0x0004, 0x0007): gm_set_game_attributes,
    (0x0004, 0x000B): gm_remove_player,
    (0x001E, 0x0001): gr_submit_report,
    (0x0004, 0x000F): gm_finalize_game,
    (0x0004, 0x0001): gm_create_game,
    (0x0005, 0x0001): redirector_get_server_instance,
    (0x0009, 0x0001): util_fetch_config,
    (0x0009, 0x0002): util_ping,
    (0x0009, 0x0007): util_pre_auth,
    (0x0009, 0x0008): util_post_auth,
    (0x0009, 0x0005): util_telemetry,
    (0x0001, 0x0028): auth_express_login,
    (0x0001, 0x0050): auth_create_persona,
    (0x0001, 0x006E): auth_login_persona,
    (0x0001, 0x001D): auth_list_entitlements,
    (0x0001, 0x00E6): auth_get_auth_token,
    (0x000F, 0x0002): messaging_fetch,
    (0x0004, 0x000D): gm_start_matchmaking,
    (0x7802, 0x0014): us_update_network_info,
}

COMP = {0x0001: "Authentication", 0x0004: "GameManager", 0x0005: "Redirector", 0x0009: "Util",
        0x000F: "Messaging", 0x0007: "Stats", 0x7802: "UserSessions", 0x0019: "Presence", 0x0801: "Autolog",
        0x0030: "?Association", 0x001E: "GameReporting"}

def gm_host_disconnected(owner):
    global DEDICATED
    d = DEDICATED
    if not d or not owner.created_game or owner.created_game["id"] != d["id"] or d.get("push") is not owner.push:
        return
    d["ready"] = False
    if DEDICATED is d:
        DEDICATED = None
    # NotifyGameRemoved: descriptor 027454C0, GID + REAS; HOST_LEAVING=2
    # in enum table 02641F90. Keep subscribers after roster removal so a
    # host-initiated kick cannot hide the subsequent game destruction.
    fields = [("GID", T_INT, d["id"]), ("REAS", T_INT, 2)]
    recipients = {**d.get("subscribers", {}), **d.get("members", {})}
    for push in recipients.values():
        try:
            push(4, 0x10, fields)
        except Exception as e:
            log("  could not notify game removal:", repr(e))
    log(f"  removed disconnected dedicated game {d['id']}")

def gm_client_disconnected(owner):
    """Drop a stale member when its Blaze connection vanishes without removePlayer."""
    d = DEDICATED
    gid = owner.joined_gid
    if not d or gid != d["id"] or owner.persona_id == d["pid"]:
        return
    pid = owner.persona_id
    member_push = d.get("members", {}).get(pid)
    # The same persona may have reconnected already; an older socket must not
    # evict the replacement connection from the active game's roster.
    if member_push is not owner.push:
        return
    d.get("members", {}).pop(pid, None)
    d.get("subscribers", {}).pop(pid, None)
    d.get("slots", {}).pop(pid, None)
    d.get("players", {}).pop(pid, None)
    _announce_leave(gid, pid)
    d.get("mesh_reports", {}).pop(pid, None)
    d.setdefault("completed", set()).discard(pid)
    fields = [("CNTX", T_INT, 0), ("GID", T_INT, gid),
              ("PID", T_INT, pid), ("REAS", T_INT, 6)]  # PLAYER_LEFT
    try:
        d["push"](4, 0x0028, fields)
        log(f"  removed disconnected player {pid} from dedicated game {gid}")
    except Exception as e:
        log("  could not notify host of disconnected player:", repr(e))

def handle(sock, addr):
    _local.session = Session()
    session().peer = addr[0]
    try: session().local = sock.getsockname()[0]
    except OSError: pass
    if not GUARD.admit(addr[0], session().local):
        log("connection refused (limit):", addr[0]); sock.close(); return
    log("connection from", addr)
    try:
        sock.settimeout(IDLE_TIMEOUT)
        c = sslv3.Conn(sock, KEY, CERT, log=lambda *a: log(" ", *a))
        c.handshake()
        send_lock = threading.Lock()
        def send_raw(b):
            with send_lock:
                c.send(b)
        # lets other connections' threads push notifications to this client/server
        session().push = lambda comp, cmd, fields: send_raw(blaze.build(comp, cmd, 0, blaze.encode(fields), ty=0x2000))
        buf = b""
        while True:
            data = c.recv()
            if not data:
                break
            buf += data
            if len(buf) > MAX_FRAME_BUFFER:
                log("  oversized message buffer: dropping", addr[0]); break
            frames, buf = blaze.parse_frames(buf)
            for hdr, payload in frames:
                name = COMP.get(hdr["component"], hex(hdr["component"]))
                try:
                    req = blaze.decode(payload)
                except Exception as e:
                    req = f"<undecodable: {e}> {payload[:64].hex()}"
                log(f"  REQ {name}.{hdr['command']:#x} id={hdr['id']} type={hdr['type']:#06x} {req}")
                h = HANDLERS.get((hdr["component"], hdr["command"]))
                body = blaze.encode(h(req)) if h else b""
                if not h:
                    log(f"  (no handler -> empty reply)")
                send_raw(blaze.build(hdr["component"], hdr["command"], hdr["id"], body))
                for ncomp, ncmd, nfields in (POST.get((hdr["component"], hdr["command"])) or (lambda: []))():
                    log(f"  NOTIFY {COMP.get(ncomp, hex(ncomp))}.{ncmd:#x}")
                    send_raw(blaze.build(ncomp, ncmd, 0, blaze.encode(nfields), ty=0x2000))
    except Exception as e:
        log("  error:", repr(e))
    finally:
        ONLINE.pop(session().persona_id, None) if ONLINE.get(session().persona_id, {}).get("session") is session() else None
        gm_client_disconnected(session())
        gm_host_disconnected(session())
        GUARD.release(addr[0])
        sock.close()
        log("  closed")

# ---------------------------------------------------------------- QoS (NAT detection) HTTP stub
QOS_PORT = 17502
_qos_req = 1

def qos_udp():
    u = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
    u.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
    u.bind(("0.0.0.0", 17502))
    log("QoS UDP echo on :17502")
    hits = {}                                            # per-source packets in the current second (reflection / flood limit)
    sec = 0
    while True:
        d, a = u.recvfrom(2048)
        now = int(time.time())
        if now != sec: sec, hits = now, {}
        hits[a[0]] = hits.get(a[0], 0) + 1
        if hits[a[0]] > 200 or len(d) > 128: continue
        if len(d) == 20:
            d = d + socket.inet_aton(a[0]) + struct.pack(">H", a[1]) + struct.pack(">I", 0)
        u.sendto(d, a)

def qos_server():
    threading.Thread(target=qos_udp, daemon=True).start()
    import http.server
    class H(http.server.BaseHTTPRequestHandler):
        timeout = 10                                   # drop stalled clients
        def log_message(self, *a): pass
        def do_GET(self):
            import urllib.parse as up
            ip = self.client_address[0]
            u = up.urlparse(self.path)
            q = {k: v[0] for k, v in up.parse_qs(u.query).items()}
            path = u.path
            global _qos_req
            if q.get("qtyp") == "2":
                _qos_req += 1
                rqid, rqsc = str(_qos_req), "4242"
            else:
                rqid, rqsc = "1", "0"
            log(f"  QOS GET {self.path}")
            if path.endswith("/firewall"):
                body = ("<firewall><ips><ip>%s</ip></ips><numinterfaces>1</numinterfaces>"
                        "<ports><port>%d</port></ports><reqsecret>%s</reqsecret><requestid>%s</requestid>"
                        "</firewall>" % (ip, QOS_PORT, rqsc, rqid))
            elif path.endswith("/firetype"):
                body = ("<firetype><firetype>1</firetype><reqsecret>%s</reqsecret><requestid>%s</requestid>"
                        "</firetype>" % (rqsc, rqid))
            elif "getPeerAddress" in path:
                body = "<peer><ip>%s</ip><port>0</port></peer>" % ip
            else:
                body = ("<qos><numprobes>3</numprobes><qosport>%d</qosport><probesize>64</probesize>"
                        "<qosip>%d</qosip><reqsecret>%s</reqsecret><requestid>%s</requestid></qos>" % (QOS_PORT, ip_int(ADV_CONFIG if ADV_CONFIG != "auto" else self.connection.getsockname()[0]), rqsc, rqid))
            data = ('<?xml version="1.0" encoding="UTF-8"?>' + body).encode()
            self.send_response(200)
            self.send_header("Content-Type", "text/xml")
            self.send_header("Content-Length", str(len(data)))
            self.end_headers()
            self.wfile.write(data)
    try:
        srv = http.server.ThreadingHTTPServer(("0.0.0.0", QOS_PORT), H)
    except OSError as e:
        log("QoS server could not start:", e); return
    log(f"QoS stub on :{QOS_PORT}")
    srv.serve_forever()


NEWS_DIR = os.path.join(HERE, "news")
XP_MULTIPLIER = int(os.environ.get("TR_XP_MULTIPLIER", "1"))
PLAYLIST_INDEX = int(os.environ.get("TR_PLAYLIST_INDEX", "0"))
SKILL_LEVEL = int(os.environ.get("TR_SKILL_LEVEL", "1"))


def autolog_route(method, path, body=b""):
    """(status, json value | bytes) for an Autolog REST call (the client sends these over HTTPS to <host>:443)."""
    if method == "GET" and path.startswith("/news/"):    # banner images: the news item's Territory field is the URL path
        try:
            with open(os.path.join(NEWS_DIR, os.path.basename(path)), "rb") as f:
                return 200, f.read()
        except OSError:
            return 404, b""
    if method == "GET" and path.endswith("/getNews"):
        try:
            with open(AUTOLOG_NEWS_PATH, encoding="utf-8") as f:
                news = json.load(f)
            if not isinstance(news, list): raise ValueError("news must be a JSON array")
            if not all(isinstance(item, dict) for item in news): raise ValueError("each news item must be an object")
            try: ann = json.load(open(ANN_PATH))
            except (OSError, ValueError): ann = []
            return 200, ann + news
        except Exception as e:
            log("  AUTOLOG news error:", repr(e))
            return 500, []
    if method == "GET" and path.endswith("/getAutologNewItems"):
        return 200, []
    # XP-related web calls (field names from the client's reflection tables: XPMultiplier, PlaylistIndex, WeeklyPlaylistIndex, SkillLevel)
    if method == "GET" and path.endswith("/getPlaylistMultiplier"):
        return 200, XP_MULTIPLIER                   # the client parses this body as one bare number (not an object)
    if method == "GET" and path.endswith("/getPlaylistIndex"):
        return 200, PLAYLIST_INDEX
    if method == "GET" and path.endswith("/getSkillLevelRule"):
        return 200, {"SkillLevel": SKILL_LEVEL}
    if method == "POST" and path.endswith("/setNewsViewed"):
        return 200, {"success": True}
    if "/Persona/" in path or "/Autolog/" in path:     # profile, skill rules, achievements, badges...: empty success
        return 200, ([] if method == "GET" and path.rsplit("/", 1)[-1] in ("getSkillLevelRule",) else {})
    return 404, []


def autolog_conn(sock, addr):
    """One Autolog REST request. The client speaks SSLv3 (like Blaze), so sniff: 0x16 = TLS record, else plain HTTP."""
    try:
        sock.settimeout(15)
        first = sock.recv(1, socket.MSG_PEEK)
        if first == b"\x16":
            c = sslv3.Conn(sock, KEY, CERT, log=lambda *a: None)
            c.handshake()
            rd, wr = c.recv, c.send
        else:
            rd, wr = (lambda: sock.recv(65536)), sock.sendall
        buf = b""
        while b"\r\n\r\n" not in buf:
            d = rd()
            if not d or len(buf) > 65536: return
            buf += d
        head, _, body = buf.partition(b"\r\n\r\n")
        lines = head.decode("latin-1").split("\r\n")
        method, target = lines[0].split(" ")[:2]
        n = 0
        headers = {}
        for l in lines[1:]:
            if ":" in l:
                key, value = l.split(":", 1)
                headers[key.strip().lower()] = value.strip()
                if key.lower() == "content-length": n = int(value)
        n = min(n, 1 << 20)
        while len(body) < n:
            d = rd()
            if not d: break
            body += d
        from urllib.parse import urlsplit
        path = urlsplit(target).path
        status, value = autolog_route(method, path, body)
        log(f"  AUTOLOG {method} {path} -> {status}")
        if method == "POST" and body:                                  # keep what the client uploads (XP, stats) for research
            try:
                f = os.path.join(RUNS_DIR, "autolog_posts.jsonl")
                if not os.path.exists(f) or os.path.getsize(f) < 5_000_000:
                    os.makedirs(RUNS_DIR, exist_ok=True)
                    open(f, "a").write(json.dumps({"t": time.time(), "from": addr[0], "path": path, "body": body[:4000].decode("latin-1")}) + "\n")
            except OSError:
                pass
        ctype = "application/json; charset=utf-8"
        if isinstance(value, bytes):
            data, ctype = value, "image/png" if path.endswith(".png") else "image/jpeg"
        else:
            data = json.dumps(value, ensure_ascii=False).encode("utf-8")
        wr((f"HTTP/1.1 {status} {'OK' if status == 200 else 'Error'}\r\nContent-Type: {ctype}\r\n"
            f"Content-Length: {len(data)}\r\nConnection: close\r\n\r\n").encode() + data)
    except Exception as e:
        log("  AUTOLOG connection error:", repr(e))
    finally:
        try: sock.close()
        except Exception: pass


def autolog_conn_guarded(sock, addr):
    try: autolog_conn(sock, addr)
    finally: GUARD.release(addr[0])


def autolog_server():
    try:
        srv = socket.socket(); srv.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
        srv.bind(("0.0.0.0", AUTOLOG_PORT)); srv.listen(16)
    except OSError as e:
        log("Autolog server could not start:", e); return
    log(f"Autolog news service on :{AUTOLOG_PORT} (SSLv3 or plain HTTP), file {AUTOLOG_NEWS_PATH}. The client uses https://<host>:443, so forward 443 -> {AUTOLOG_PORT} (see STATUS.md)")
    while True:
        cs, ca = srv.accept()
        if not GUARD.admit(ca[0], cs.getsockname()[0]): cs.close(); continue
        threading.Thread(target=autolog_conn_guarded, args=(cs, ca), daemon=True).start()


def _inject_field(kv):
    # Research aid: "TAG=value", where value is an int or one of the @tokens below that rebuild a
    # real payload, e.g. "4 0x14 GAME=@game PROS=@pros2 QUEU=@queu REAS=@dct".
    k, v = kv.split("=", 1)
    d = DEDICATED
    if v == "@gid":
        return (k, T_INT, d["id"])
    if v == "@pdat":
        return (k, T_STRUCT, _gm_player(d["id"]))
    if v == "@pdatC":
        return (k, T_STRUCT, _gm_player(d["id"], state=2))
    if v == "@user":
        return (k, T_STRUCT, _user_ident())
    if v == "@extdata":
        return (k, T_STRUCT, _ext_data())
    if v == "@game":
        return (k, T_STRUCT, _dedicated_fields(d["id"], d["request"], d["pid"], 130, port=LAN_PORT))
    if v == "@attr":   # matchmaker attributes (playListId/MapPath) the host loads the level from
        return (k, T_MAP, (T_STR, T_STR, _attrs()))
    if v == "@game2":  # GAME with the matchmaker attributes set
        return (k, T_STRUCT, _dedicated_fields(d["id"], d["request"], d["pid"], 130, port=LAN_PORT, attrs=_attrs()))
    if v == "@gameP":  # attributes with playListId only
        return (k, T_STRUCT, _dedicated_fields(d["id"], d["request"], d["pid"], 130, port=LAN_PORT, attrs={"playListId": PLAYLIST_ID}))
    if v == "@gameM":  # attributes with MapPath only
        return (k, T_STRUCT, _dedicated_fields(d["id"], d["request"], d["pid"], 130, port=LAN_PORT, attrs={"MapPath": MAP_PATH}))
    if v == "@game132":  # attributes + IN_GAME state
        return (k, T_STRUCT, _dedicated_fields(d["id"], d["request"], d["pid"], 131, port=LAN_PORT, attrs=_attrs()))
    if v == "@pros":
        return (k, T_LIST, (T_STRUCT, [_gm_player(d["id"])]))
    if v == "@pdatB":  # a second, made-up player (to test the "enough players" gate)
        return (k, T_STRUCT, _gm_player(d["id"], name="TRPlayer2", pid=1000000002, slot_id=2))
    if v == "@pros3":  # both players
        return (k, T_LIST, (T_STRUCT, [_gm_player(d["id"]), _gm_player(d["id"], name="TRPlayer2", pid=1000000002, slot_id=2)]))
    if v == "@pros2":  # host first, then the joining player (fresh default session)
        host = _gm_player(d["id"], name=d["name"], pid=d["pid"], addr=d["address"], slot_id=0)
        return (k, T_LIST, (T_STRUCT, [host, _gm_player(d["id"])]))
    if v == "@queu":
        return (k, T_LIST, (T_STRUCT, []))
    if v.startswith("@reas"):   # REAS union with member N (e.g. @reas4) and an empty VALU struct: probe setup contexts
        return (k, T_UNION, Union(int(v[5:]), "VALU", (T_STRUCT, [])))
    if v == "@dct":
        return (k, T_UNION, Union(0, "VALU", (T_STRUCT, [("DCTX", T_INT, 0)])))
    return (k, T_INT, int(v, 0))

def _inject_watcher():
    # Research aid: write "COMP CMD TAG=value ..." (use @gid for the dedicated game id) to
    # /tmp/nfstr_round/inject.txt to push that notification to the dedicated server's connection,
    # e.g. "4 0x64 GID=@gid GSTA=131" or "4 0x14 GAME=@game PROS=@pros2 QUEU=@queu REAS=@dct".
    path = "/tmp/nfstr_round/inject.txt"
    while True:
        time.sleep(1)
        try:
            lines = open(path).read().splitlines()
            os.remove(path)
        except OSError:
            continue
        for line in lines:
            try:
                parts = line.split()
                comp, cmd = int(parts[0], 0), int(parts[1], 0)
                fields = sorted((_inject_field(kv) for kv in parts[2:]), key=lambda f: f[0])
                DEDICATED["push"](comp, cmd, fields)
                log("  injected to dedicated server:", line)
            except Exception as e:
                log("  inject failed:", line, repr(e))

def main():
    threading.Thread(target=_inject_watcher, daemon=True).start()
    threading.Thread(target=_elim_watcher, daemon=True).start()
    threading.Thread(target=admin_server, daemon=True).start()
    threading.Thread(target=qos_server, daemon=True).start()
    threading.Thread(target=autolog_server, daemon=True).start()
    s = socket.socket()
    s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
    s.bind(("0.0.0.0", PORT))
    s.listen(16)
    log(f"trserver listening on :{PORT}, advertising " + ("each client the address it connected to" if ADV_CONFIG == "auto" else f"{ADV_CONFIG}:{PORT}"))
    while True:
        c, a = s.accept()
        threading.Thread(target=handle, args=(c, a), daemon=True).start()

if __name__ == "__main__":
    main()
