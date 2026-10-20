#!/usr/bin/env python3
"""Interactive server console: players, kick/ban, announcements, standings, change game mode.
Run on the server machine after tools/host_server.sh:   python3 tools/admin.py"""
import json, os, socket, subprocess, sys
ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
sys.path.insert(0, os.path.join(ROOT, "launcher"))
RUNS = os.path.join(ROOT, "recon", "runs")
try: import modes
except ImportError: modes = None

def ask(cmd, **kw):
    try:
        s = socket.create_connection(("127.0.0.1", 42128), timeout=4)
        s.sendall((json.dumps(dict(cmd=cmd, **kw)) + "\n").encode())
        return json.loads(s.makefile().readline())
    except OSError as e:
        return {"error": "server not reachable (%s) - is tools/host_server.sh running?" % e}

def sh(*a): return subprocess.run(a, capture_output=True, text=True)

def players():
    r = ask("players")
    if "error" in r: return print(r["error"])
    print("\n  game:", r["game"], "(dedicated %s)" % ("ready" if r["dedicated_ready"] else "not ready"))
    for i, p in enumerate(r["players"], 1):
        print("  %d. %-16s %-15s online %4ds  %s" % (i, p["name"], p["peer"], p["online_s"], "IN GAME" if p["in_game"] else "menu"))
    if not r["players"]: print("  (nobody logged in)")
    if r["banned"]: print("  banned:", ", ".join(r["banned"]))
    return r

def pick_player():
    r = players()
    if not r or not r["players"]: return None
    v = input("player number or name: ").strip()
    if v.isdigit() and 1 <= int(v) <= len(r["players"]): return r["players"][int(v) - 1]["name"]
    return v or None

def standings():
    try: st = json.load(open(os.path.join(RUNS, "standings.json")))
    except (OSError, ValueError): return print("  no standings yet")
    print("\n  mode %s, races %d" % (st["mode"], st["races"]))
    if modes: print(modes.table(st))

def running_args():
    out = sh("systemctl", "--user", "show", "nfstr-menu-score", "-p", "ExecStart", "--value").stdout
    return out

def set_mode():
    if not modes: return print("  modes.py not found")
    names = list(modes.MODES)
    for i, n in enumerate(names, 1): print("  %2d. %-14s %s" % (i, n, modes.MODES[n][2][:70]))
    print("   0. vanilla (no custom modes)")
    v = input("mode number(s) or names, comma separated (rotation): ").strip()
    if not v: return
    if v == "0" or v == "vanilla":
        sh(sys.executable, os.path.join(ROOT, "tools", "host.py"), "score-stop")
        sh(sys.executable, os.path.join(ROOT, "tools", "gen_news.py"), "--vanilla")
        json.dump([], open(os.path.join(RUNS, "eliminated.json"), "w"))
        return print("  vanilla: scoring stopped, plain News")
    sel = [names[int(x) - 1] if x.strip().isdigit() else x.strip() for x in v.split(",")]
    every = input("races per mode [3]: ").strip() or "3"
    elim = input("eliminated players: hold (removed) or soft (may rejoin) [hold]: ").strip() or "hold"
    if input("reset standings? [y/N]: ").strip().lower() == "y":
        for f in ("standings.json", "seasons.json"):
            try: os.remove(os.path.join(RUNS, f))
            except OSError: pass
    r = sh(sys.executable, os.path.join(ROOT, "tools", "host.py"), "score", ",".join(sel), "--every", every, "--elim", elim)
    print(" ", r.stdout.strip() or r.stderr.strip())

MENU = """
 1 players          5 announcements (list)      9 change game mode / vanilla
 2 kick player      6 announce (post news)      0 quit
 3 ban player       7 clear announcements
 4 unban player     8 standings
"""
while True:
    print(MENU)
    c = input("> ").strip()
    if c == "1": players()
    elif c in ("2", "3"):
        n = pick_player()
        if n: print(" ", ask("kick" if c == "2" else "ban", name=n))
    elif c == "4":
        r = ask("players"); print("  banned:", ", ".join(r.get("banned", [])) or "-")
        n = input("name to unban: ").strip()
        if n: print(" ", ask("unban", name=n))
    elif c == "5":
        for a in ask("announcements").get("items", []): print("  #%s %s - %s" % (a["NewsId"], a["Subject"], a["ShortMessage"]))
    elif c == "6":
        t = input("title: ").strip() or "Announcement"; x = input("text: ").strip()
        if x: print(" ", ask("announce", title=t, text=x))
    elif c == "7": print(" ", ask("clear_announcements"))
    elif c == "8": standings()
    elif c == "9": set_mode()
    elif c in ("0", "q", ""): break
