#!/usr/bin/env python3
"""NFSTR LAN lobby service (stdlib only, in-memory).

Hosts register a game, players sign up for it, the host presses Launch and everybody's launcher starts at
the same moment (the game only lets players in that are already connecting when the server starts).

  python3 lobby_server.py [--bind 0.0.0.0] [--port 26100] [--data lobby_data.json]

API (JSON):
  GET  /api/games                         list open/launching games (no secrets)
  POST /api/games         {name,code,playlist,host,max[,mode,scheme]} -> {id, token}  host registers (code = launcher join code)
  GET  /api/games/<id>                    one game: players, state, launch_at, code (code only once launching)
  POST /api/games/<id>/join   {nick}      -> {ok, pid}       player signs up
  POST /api/games/<id>/leave  {pid}
  POST /api/games/<id>/launch {token, delay}                 host: state=launching, launch_at = now+delay (s)
  POST /api/games/<id>/heartbeat {token[,races,board]}       host: keep alive; board = [[name, points], ...] (top 8) shown in the listing
  POST /api/games/<id>/close  {token}                        host: remove the game (its final scores go to the ladder)
  GET  /api/ladder[?season=YYYY-MM|all]                      season ladder (default: this month): medals + points per nickname
Games expire after TTL seconds without a host heartbeat (any host call or GET with ?token=).
Limits: body <= 4 KB, 50 games, 64 players per game, nick 1-16 chars.
"""
import argparse, json, os, secrets, threading, time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

TTL = 90.0
LOCK = threading.Lock()
GAMES = {}          # id -> dict
ARCHIVE = []        # finished games: {"id","season","mode","t","rows":[[nick, points, wins], ...]} (newest last, max 500)
DATA_FILE = None    # json file the archive is saved to (set by --data)
MAX_ARCHIVE = 500


def now():
    return time.time()


def load_archive():
    global ARCHIVE
    if DATA_FILE and os.path.isfile(DATA_FILE):
        try:
            ARCHIVE = json.load(open(DATA_FILE)).get("archive", [])[-MAX_ARCHIVE:]
        except (OSError, ValueError):
            ARCHIVE = []


def save_archive():
    if not DATA_FILE:
        return
    try:
        tmp = DATA_FILE + ".tmp"
        json.dump({"archive": ARCHIVE[-MAX_ARCHIVE:]}, open(tmp, "w"))
        os.replace(tmp, DATA_FILE)
    except OSError:
        pass


def archive_game(g):
    """Called when a game leaves the lobby. Counts only games with 2+ players that really raced; only nicknames that
    signed up for this game (or the host) can score, so a stranger cannot be put on the ladder by a rogue host."""
    rows = g.get("rows") or []
    allowed = {p["nick"].lower() for p in g["players"].values()} | {g["host"].lower()}
    rows = [r for r in rows if r[0].lower() in allowed]
    if g.get("races", 0) < 1 or len(rows) < 2 or any(a["id"] == g["id"] for a in ARCHIVE):
        return
    ARCHIVE.append({"id": g["id"], "season": time.strftime("%Y-%m"), "mode": g.get("mode", ""), "t": int(now()), "rows": rows})
    del ARCHIVE[:-MAX_ARCHIVE]
    save_archive()


def ladder(season):
    """Aggregate the archive: placing in a game = sort by points (then wins); medals for the top 3."""
    agg = {}
    for a in ARCHIVE:
        if season != "all" and a["season"] != season:
            continue
        for place, (nick, pts, wins) in enumerate(sorted(a["rows"], key=lambda r: (-r[1], -r[2]))):
            e = agg.setdefault(nick, {"nick": nick, "games": 0, "gold": 0, "silver": 0, "bronze": 0, "points": 0})
            e["games"] += 1; e["points"] += pts
            if place < 3:
                e[("gold", "silver", "bronze")[place]] += 1
    return sorted(agg.values(), key=lambda e: (-e["gold"], -e["silver"], -e["bronze"], -e["points"], e["nick"]))[:50]


def clean():
    t = now()
    for gid in [g for g, v in GAMES.items() if t - v["seen"] > TTL or (v["state"] == "launching" and t - v["launch_at"] > 900)]:
        archive_game(GAMES[gid])
        del GAMES[gid]


def public(g, with_code=False):
    out = {"id": g["id"], "name": g["name"], "playlist": g["playlist"], "host": g["host"], "max": g["max"],
           "state": g["state"], "launch_at": g["launch_at"], "players": [p["nick"] for p in g["players"].values()],
           "mode": g.get("mode", ""), "scheme": g.get("scheme", ""), "races": g.get("races", 0), "board": g.get("board", []),
           "now": now()}
    if with_code and g["state"] != "open":
        out["code"] = g["code"]
    return out


def text(v, n):
    return str(v)[:n].strip()


class H(BaseHTTPRequestHandler):
    server_version = "nfstr-lobby"

    def log_message(self, *a):
        pass

    def _send(self, code, obj):
        body = json.dumps(obj).encode()
        self.send_response(code)
        self.send_header("Content-Type", "application/json"); self.send_header("Content-Length", str(len(body)))
        self.send_header("Access-Control-Allow-Origin", "*")
        self.end_headers(); self.wfile.write(body)

    def _body(self):
        n = int(self.headers.get("Content-Length") or 0)
        if n > 4096:
            raise ValueError("body too large")
        return json.loads(self.rfile.read(n) or b"{}")

    def do_GET(self):
        parts = self.path.split("?")[0].strip("/").split("/")
        with LOCK:
            clean()
            if parts == ["api", "games"]:
                return self._send(200, {"games": [public(g) for g in GAMES.values() if g["state"] != "started"], "now": now()})
            if parts == ["api", "ladder"]:
                q = dict(x.split("=", 1) for x in (self.path.split("?", 1) + [""])[1].split("&") if "=" in x)
                season = q.get("season") or time.strftime("%Y-%m")
                if season != "all" and not (len(season) == 7 and season[4] == "-" and season[:4].isdigit() and season[5:].isdigit()):
                    return self._send(400, {"error": "season must be YYYY-MM or all"})
                return self._send(200, {"season": season, "entries": ladder(season), "games": sum(1 for a in ARCHIVE if season == "all" or a["season"] == season)})
            if len(parts) == 3 and parts[:2] == ["api", "games"] and parts[2] in GAMES:
                g = GAMES[parts[2]]
                return self._send(200, public(g, with_code=True))
        self._send(404, {"error": "not found"})

    def do_POST(self):
        try:
            b = self._body()
        except Exception as e:
            return self._send(400, {"error": str(e)})
        parts = self.path.split("?")[0].strip("/").split("/")
        with LOCK:
            clean()
            if parts == ["api", "games"]:
                if len(GAMES) >= 50:
                    return self._send(503, {"error": "too many games"})
                code = text(b.get("code", ""), 1500)
                if not code.startswith("NFSTR1-"):
                    return self._send(400, {"error": "code must be a launcher join code"})
                gid = secrets.token_hex(3)
                g = {"id": gid, "token": secrets.token_urlsafe(16), "name": text(b.get("name", "game"), 30) or "game",
                     "playlist": text(b.get("playlist", ""), 40), "host": text(b.get("host", "host"), 16),
                     "max": max(2, min(32, int(b.get("max", 8)))), "code": code, "state": "open", "launch_at": 0.0,
                     "mode": text(b.get("mode", ""), 20), "scheme": text(b.get("scheme", ""), 10), "races": 0, "board": [],
                     "players": {}, "seen": now()}
                GAMES[gid] = g
                return self._send(200, {"id": gid, "token": g["token"]})
            if len(parts) == 4 and parts[:2] == ["api", "games"] and parts[2] in GAMES:
                g, act = GAMES[parts[2]], parts[3]
                if act == "join":
                    nick = text(b.get("nick", ""), 16)
                    if not nick:
                        return self._send(400, {"error": "nick required"})
                    if g["state"] != "open":
                        return self._send(409, {"error": "game already launched"})
                    if len(g["players"]) >= min(g["max"], 64):
                        return self._send(409, {"error": "game full"})
                    if any(p["nick"].lower() == nick.lower() for p in g["players"].values()):
                        return self._send(409, {"error": "nickname already taken in this game"})
                    pid = secrets.token_urlsafe(8)
                    g["players"][pid] = {"nick": nick}
                    return self._send(200, {"ok": True, "pid": pid})
                if act == "leave":
                    g["players"].pop(str(b.get("pid", "")), None)
                    return self._send(200, {"ok": True})
                if b.get("token") != g["token"]:
                    return self._send(403, {"error": "bad token"})
                g["seen"] = now()
                if act == "launch":
                    g["state"] = "launching"
                    g["launch_at"] = now() + max(3.0, min(60.0, float(b.get("delay", 8))))
                    return self._send(200, {"ok": True, "launch_at": g["launch_at"]})
                if act == "heartbeat":
                    board = []
                    for row in (b.get("board") or [])[:8]:
                        try:
                            board.append([text(row[0], 16), int(row[1])])
                        except (IndexError, TypeError, ValueError):
                            pass
                    if "board" in b:
                        g["board"] = board
                    rows = []
                    for row in (b.get("rows") or [])[:16]:
                        try:
                            rows.append([text(row[0], 16), int(row[1]), int(row[2])])
                        except (IndexError, TypeError, ValueError):
                            pass
                    if "rows" in b:
                        g["rows"] = rows
                    try:
                        g["races"] = max(0, min(999, int(b.get("races", g.get("races", 0)))))
                    except (TypeError, ValueError):
                        pass
                    return self._send(200, {"ok": True})
                if act == "close":
                    archive_game(g)
                    del GAMES[g["id"]]
                    return self._send(200, {"ok": True})
        self._send(404, {"error": "not found"})


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--bind", default="0.0.0.0"); ap.add_argument("--port", type=int, default=26100)
    ap.add_argument("--data", default=os.path.join(os.path.dirname(os.path.abspath(__file__)), "lobby_data.json"), help="file the season ladder archive is saved to")
    a = ap.parse_args()
    global DATA_FILE
    DATA_FILE = a.data
    load_archive()
    print("lobby on %s:%d" % (a.bind, a.port), flush=True)
    ThreadingHTTPServer((a.bind, a.port), H).serve_forever()


if __name__ == "__main__":
    main()
