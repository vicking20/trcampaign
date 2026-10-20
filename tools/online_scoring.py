#!/usr/bin/env python3
"""Scoring daemon for the online (Blaze) dedicated server: baseline at race start (state 6), results at state 8,
then launcher/modes.apply(); standings go to recon/runs/standings.json and the News box (server/autolog_news.json).

Usage: tools/online_scoring.py <mode>[,<mode>...] [--every N] [--scheme field] [--teams "Red=A,B;Blue=C,D"]
  one mode      : that mode forever
  several modes : rotation - after N races (default 3) the season ends, the winner is announced in News and the
                  next mode starts with fresh standings. Team modes are skipped unless --teams is given.
Reset by deleting recon/runs/standings.json and recon/runs/seasons.json. SCORE_NOW="a=95.2;b=96.0" applies one race by hand.
Modes: see launcher/modes.py MODES."""
import argparse, datetime, json, os, re, sys, time
ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
sys.path.insert(0, os.path.join(ROOT, "launcher")); sys.path.insert(0, os.path.join(ROOT, "tools"))
import modes, race_results as rr

ap = argparse.ArgumentParser()
ap.add_argument("modes"); ap.add_argument("--every", type=int, default=3)
ap.add_argument("--elim", choices=["hold", "soft"], default="hold",
                help="hold: eliminated players are removed and cannot match until the season changes; soft: they may rejoin, their results simply do not score")
ap.add_argument("--run", nargs=argparse.REMAINDER, help="start this command (the dedicated server) as a child process; under ptrace_scope=1 only a parent may read its memory")
ap.add_argument("--on-user-close", default="", help="shell command to run when the engine is closed on purpose (exit code 0), e.g. to stop the whole server")
ap.add_argument("--scheme", default="field"); ap.add_argument("--teams", default="")
args = ap.parse_args()
ROTATION = [m for m in args.modes.split(",") if m]
for m in ROTATION:
    if m not in modes.MODES: sys.exit("unknown mode %r; choose from: %s" % (m, ", ".join(modes.MODES)))
if len(ROTATION) > 1 and not args.teams:
    ROTATION = [m for m in ROTATION if m not in modes.TEAM_MODES] or ROTATION[:1]
RUNS = os.path.join(ROOT, "recon", "runs"); os.makedirs(RUNS, exist_ok=True)
SP = os.path.join(RUNS, "standings.json"); SEASONS = os.path.join(RUNS, "seasons.json")
NP = os.path.join(ROOT, "server", "autolog_news.json")

def stamp(d): return d.strftime("%m/%d/%Y ") + str(int(d.strftime("%I"))) + d.strftime(":%M:%S %p")

def load(path, default):
    try: return json.load(open(path))
    except (OSError, ValueError): return default

def new_season(mode):
    return modes.new_standings(mode, args.scheme, args.teams)

seasons = load(SEASONS, [])                       # finished seasons, newest last: {"mode", "races", "winner", "board"}
st = load(SP, None)
if not st or st.get("mode") not in ROTATION:
    st = new_season(ROTATION[0])
mode = st["mode"]

def item(i, subject, short, long_, age):
    return {"NewsId": i, "NewsIcon": "/news/banner.jpg", "NewsViewed": "NewsViewed_No", "Territory": "", "MetaContent": "",
            "Subject": subject, "ShortMessage": short[:110], "Message": long_[:900], "Author": "TR Campaign",
            "PublishedDate": stamp(datetime.datetime.now() - datetime.timedelta(minutes=age))}

def publish(events):
    rank = modes.ranking(st)
    lines = ["%d. %s - %s pts" % (i + 1, n, p["points"]) for i, (n, p) in enumerate(rank[:10])]
    news = [item(1, "Standings after race %d (%s)" % (st["races"], mode), "; ".join(lines[:3]) or "no results yet",
                 "\n".join(lines + [""] + events), 0),
            item(2, "Mode: " + mode.capitalize(), modes.MODES[mode][2], modes.MODES[mode][2], 1)]
    if len(ROTATION) > 1:
        left = max(args.every - st["races"], 0)
        nxt = ROTATION[(ROTATION.index(mode) + 1) % len(ROTATION)]
        news.append(item(3, "Next mode: " + nxt.capitalize(), "after %d more race(s). Rotation: %s" % (left, ", ".join(ROTATION)),
                         "%s\n\nRotation: %s, a new mode every %d races." % (modes.MODES[nxt][2], " > ".join(ROTATION), args.every), 2))
    for k, s in enumerate(reversed(seasons[-3:])):
        news.append(item(10 + k, "Season result: %s" % s["mode"].capitalize(), "Winner: %s (%d races)" % (s["winner"], s["races"]),
                         "Winner: %s after %d races.\n%s" % (s["winner"], s["races"], "\n".join(s["board"])), 3 + k))
    json.dump(news, open(NP, "w"), indent=1)

def export_eliminated():
    """The Blaze server removes these players from the game and holds their matchmaking (server/trserver.py)."""
    out = sorted(n for n, p in st["players"].items() if p.get("eliminated")) if args.elim == "hold" else []
    json.dump(out, open(os.path.join(RUNS, "eliminated.json"), "w"))
    if out: print("eliminated:", ", ".join(out), flush=True)

def after_race(events):
    """Save, publish; in a rotation end the season when it has run `every` races."""
    global st, mode
    json.dump(st, open(SP, "w"), indent=1)
    if len(ROTATION) > 1 and st["races"] >= args.every:
        rank = modes.ranking(st)
        seasons.append({"mode": mode, "races": st["races"], "winner": rank[0][0] if rank else "-",
                        "board": ["%d. %s - %s pts" % (i + 1, n, p["points"]) for i, (n, p) in enumerate(rank[:8])]})
        json.dump(seasons, open(SEASONS, "w"), indent=1)
        print("== season over: %s won %s ==" % (seasons[-1]["winner"], mode), flush=True)
        mode = ROTATION[(ROTATION.index(mode) + 1) % len(ROTATION)]
        st = new_season(mode)
        json.dump(st, open(SP, "w"), indent=1)
        events = events + ["Season over - new mode: " + mode]
    export_eliminated()
    publish(events)

def dump_raw(pid, path):
    """Debug aid when the reader finds nothing: marker hits in the whole 32-bit heap with 96 bytes of context (Linux only)."""
    if rr.procmem.IS_WINDOWS: return
    m = open("/proc/%d/mem" % pid, "rb", 0)
    with open(path, "w") as f:
        for l in open("/proc/%d/maps" % pid):
            a, b = [int(x, 16) for x in l.split()[0].split("-")]
            if a >= 0x100000000 or "rw" not in l.split()[1]: continue
            try: m.seek(a); d = m.read(b - a)
            except OSError: continue
            for h in re.finditer(re.escape(rr.SIG), d):
                i = h.start(); f.write("%08x %s\n" % (a + i, d[max(0, i - 96):i + 4].hex()))
    print("raw dump ->", path, flush=True)

import subprocess
child = None
restarts = []
def keep_server_running():
    """The engine exits with code 241 when a session ends and with other non-zero codes when it crashes: start it again (as our child, so its memory stays
    readable). Exit code 0 = closed on purpose: stop everything. Gives up after 5 restarts within a minute (crash loop). Called from the main loop."""
    global child
    if args.run and (child is None or child.poll() is not None):
        if child is not None:
            rc = child.returncode
            if rc == 0:
                print("game server closed on purpose: stopping", flush=True)
                if args.on_user_close: subprocess.call(args.on_user_close, shell=True)
                sys.exit(0)
            restarts[:] = [t for t in restarts if time.time() - t < 60] + [time.time()]
            if len(restarts) > 5: sys.exit("game server keeps crashing (exit code %s): giving up" % rc)
            print("game server exited (code %s): restarting" % rc, flush=True); time.sleep(3)
        child = subprocess.Popen(args.run); print("started dedicated server as child pid", child.pid, flush=True)
keep_server_running()
print("scoring %s (%s); rotation every %d races; races so far %d" % (mode, ", ".join(ROTATION), args.every, st["races"]), flush=True)
export_eliminated(); publish([])
if os.environ.get("SCORE_NOW"):
    res = sorted(((n, float(t)) for n, t in (x.rsplit("=", 1) for x in os.environ["SCORE_NOW"].split(";"))), key=lambda kv: (kv[1] <= 0, kv[1]))
    after_race(modes.apply(st, res)); print("manual race applied:\n" + modes.table(st), flush=True)
base, seen, pid = frozenset(), None, None
announced = False
while True:
    try:
        keep_server_running()
        if not pid or not rr.procmem.alive(pid): pid = rr.server_pid()
        if not pid: time.sleep(3); continue
        s, ss, rc = rr.state(pid)
        if not announced: announced = True; print("reading dedicated server pid %d (state %d)" % (pid, s), flush=True)
        if s == 6 and seen != ("base", ss, rc):
            base = rr.baseline(pid); seen = ("base", ss, rc); print("race start: baseline %d entries" % len(base), flush=True)
        elif s == 8 and seen != ("done", ss, rc):
            time.sleep(1.5); seen = ("done", ss, rc)
            known = set(load(os.path.join(RUNS, "players.json"), []))
            res = [(n, t) for n, t in rr.results(pid, base) if n in known]
            print("results session=%d race=%d: %s" % (ss, rc, res), flush=True)
            if not res: dump_raw(pid, os.path.join(RUNS, "rawdump_%d_%d.txt" % (ss, rc)))
            else:
                ev = modes.apply(st, res); after_race(ev)
                print("== race %d (%s) ==\n  %s\n%s" % (st["races"], mode, "\n  ".join(ev), modes.table(st)), flush=True)
    except Exception as e:
        print("error:", repr(e), flush=True); pid = None; base = frozenset()
    time.sleep(1)
