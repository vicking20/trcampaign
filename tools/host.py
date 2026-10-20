#!/usr/bin/env python3
"""Cross-platform server launcher.
  python tools/host.py start <address> [modes|vanilla] [races-per-mode] [hold|soft] [--game-dir DIR]
  python tools/host.py run [game-folder]            (Windows: used by StartServer.bat; the folder defaults to the one holding NFSTR_Server/)
  python tools/host.py score <modes> [--every N] [--elim hold|soft]     (change the mode)
  python tools/host.py score-stop | stop | status
Linux: delegates to tools/host_server.sh (Proton + systemd). Windows (untested): starts trserver.py, the game's own
NFSTR_Server.exe and the scoring daemon as plain processes; News is served on port 443 directly (no redirect needed).
Windows host needs: Python 3, the game folder (default ./online_test) with dinput8.dll + Scripts/NFSTR_Online.asi/.ini (Host=127.0.0.1)."""
import json, os, subprocess, sys, time
ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
RUNS = os.path.join(ROOT, "recon", "runs"); PIDS = os.path.join(RUNS, "host_pids.json")
WIN = sys.platform.startswith("win")
SERVER_ARGS = ["-Server.ServerName", "NFSTR_BlazeServer", "-GamePort", "25200",
               "-Game.DefaultLayerInclusion", "StartupMode=Game;GameMode=CareerMode",
               "-Game.DynamicSubLevels", "_c4/Levels/FE/FrontEnd/NfsGarage", "-NfsOnline.Backend", "Backend_Blaze",
               "-NfsGame.CareerStateTimeOutTimer", "86400", "-NfsGame.CarSelectTimer", "30.0", "-NfsGame.PreLoadTimer", "80.0",
               "-NfsGame.ActivityTimer", "600.0", "-NfsGame.RewardsTimer", "25.0", "-NfsGame.IntermissionTimer", "15.0",
               "-NfsGame.VotingTimer", "15.0", "-NfsGame.WaitForVotesTimer", "2.0", "-NfsGame.PostVotingTimer", "11.0"]

def pids(): 
    try: return json.load(open(PIDS))
    except (OSError, ValueError): return {}

def save(d): os.makedirs(RUNS, exist_ok=True); json.dump(d, open(PIDS, "w"))

def kill(pid):
    try:
        if WIN: subprocess.run(["taskkill", "/F", "/T", "/PID", str(pid)], capture_output=True)
        else: os.kill(pid, 15)
    except OSError: pass

def stop(which=("score", "server", "blaze")):
    d = pids()
    for k in which:
        if k in d: kill(d.pop(k))
    save(d)

def start_score(modes, every, elim):
    d = pids()
    if "score" in d: kill(d.pop("score"))
    cmd = [sys.executable, "-u", os.path.join(ROOT, "tools", "online_scoring.py"), modes, "--every", str(every), "--elim", elim]
    if WIN: p = subprocess.Popen(cmd, cwd=ROOT, creationflags=0x10)          # CREATE_NEW_CONSOLE: its own window with the standings log
    else: p = subprocess.Popen(cmd, stdout=open(os.path.join(RUNS, "scoring.log"), "a"), stderr=subprocess.STDOUT, cwd=ROOT)
    d["score"] = p.pid; save(d); print("scoring daemon:", modes, "every", every, "elimination", elim)

def default_game():
    """The game folder = the folder that holds NFSTR_Server/ (this package is extracted into it)."""
    g = os.path.dirname(ROOT)
    return g if os.path.isfile(os.path.join(g, "Need For Speed The Run.exe")) else ""

def read_server_ini():
    """Optional NFSTR_Server/server.ini:  [Server] Address=  Modes=  GameDir=  Every=  Elim=   (skips the questions)"""
    import configparser
    cp = configparser.ConfigParser(); cp.read(os.path.join(ROOT, "server.ini"))
    return dict(cp["Server"]) if cp.has_section("Server") else {}

def start_windows(game, addr, modes, every, elim, foreground=False):
    exe = os.path.join(game, "NFSTR_Server.exe")
    if not os.path.isfile(exe): subprocess.call([sys.executable, os.path.join(ROOT, "tools", "make_server_exe.py"), game])
    if not os.path.isfile(exe): sys.exit("no NFSTR_Server.exe in %s (is this the game folder?)" % game)
    stop()
    env = dict(os.environ, TR_AUTOLOG_PORT="443")
    d = {}
    cmd = [sys.executable, "-u", os.path.join(ROOT, "server", "trserver.py"), "42127", addr]
    if foreground:                                         # the Blaze server runs in THIS window so the host sees what happens
        d["server"] = subprocess.Popen([exe] + SERVER_ARGS, cwd=game).pid; save(d)
        if modes == "vanilla": subprocess.call([sys.executable, os.path.join(ROOT, "tools", "gen_news.py"), "--vanilla"])
        else: start_score(modes, every, elim)
        print("server up: advertising %s, modes %s. Close this window or press Ctrl+C to stop.\n" % (addr, modes), flush=True)
        try: subprocess.call(cmd, env=env, cwd=ROOT)
        except KeyboardInterrupt: pass
        stop(); print("stopped"); return
    blaze = subprocess.Popen(cmd, env=env, stdout=open(os.path.join(RUNS, "blaze.log"), "a"), stderr=subprocess.STDOUT, cwd=ROOT)
    d["blaze"] = blaze.pid; time.sleep(2)
    d["server"] = subprocess.Popen([exe] + SERVER_ARGS, cwd=game).pid
    save(d)
    if modes == "vanilla": subprocess.call([sys.executable, os.path.join(ROOT, "tools", "gen_news.py"), "--vanilla"])
    else: start_score(modes, every, elim)
    print("server up: advertising %s, modes %s." % (addr, modes))

def local_ips():
    import socket
    ips = []
    try:
        s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM); s.connect(("10.255.255.255", 1)); ips.append(s.getsockname()[0]); s.close()
    except OSError: pass
    try: ips += [i for i in socket.gethostbyname_ex(socket.gethostname())[2] if i not in ips and not i.startswith("127.")]
    except OSError: pass
    return ips

def install_player_files(game):
    """Put the plugin and the ASI loader into the game folder (from ./payload), and make an ini that points at this machine."""
    import shutil
    pay = os.path.join(ROOT, "payload"); sc = os.path.join(game, "Scripts"); os.makedirs(sc, exist_ok=True)
    if not os.path.isdir(pay): print("  (no payload folder: extract NFSTR_Online_Client.zip into the game folder yourself)"); return
    if not os.path.isfile(os.path.join(game, "dinput8.dll")) and os.path.isfile(os.path.join(pay, "dinput8.dll")):
        shutil.copy2(os.path.join(pay, "dinput8.dll"), os.path.join(game, "dinput8.dll")); print("  installed dinput8.dll (ASI loader)")
    shutil.copy2(os.path.join(pay, "NFSTR_Online.asi"), os.path.join(sc, "NFSTR_Online.asi"))
    ini = os.path.join(sc, "NFSTR_Online.ini")
    if not os.path.isfile(ini):
        open(ini, "w").write("[Server]\nHost=127.0.0.1\n\n[Player]\nName=Host\n\n[Origin]\nEnable=1\nStartDelayMs=20000\n"); print("  wrote Scripts\\NFSTR_Online.ini (Host=127.0.0.1: you play on this PC too)")
    print("  plugin installed in", game)

def windows_wizard(rest):
    """StartServer.bat: finds the game folder itself and starts everything. No questions: players are handed the address they connected to."""
    CFG = os.path.join(RUNS, "windows.json")
    try: cfg = json.load(open(CFG))
    except (OSError, ValueError): cfg = {}
    ini = read_server_ini()
    print("NFSTR Online server (Windows, experimental)\n")
    game = (rest[0] if rest else "") or ini.get("gamedir", "") or default_game() or cfg.get("game", "")
    while not os.path.isfile(os.path.join(game, "Need For Speed The Run.exe")):
        game = input("Game folder (the one with 'Need For Speed The Run.exe'): ").strip().strip('"')
    modes = ini.get("modes") or cfg.get("modes", "plague,bounty,streak,cannonball")
    json.dump({"game": game, "modes": modes}, open(CFG, "w"))
    if not os.path.isfile(os.path.join(game, "Scripts", "NFSTR_Online.asi")): print("WARNING: Scripts\\NFSTR_Online.asi is missing - extract the whole zip into the game folder.")
    print("Friends put one of these in Scripts\\NFSTR_Online.ini as Host=:  %s\n(same network: the first one; a VPN like Tailscale: the VPN address)\n" % ", ".join(local_ips() or ["this PC's address"]))
    start_windows(game, ini.get("address", "auto"), modes, int(ini.get("every", 3)), ini.get("elim", "hold"), foreground=True)

def main():
    a = sys.argv[1:]
    if not a: sys.exit(__doc__)
    cmd, rest = a[0], a[1:]
    os.makedirs(RUNS, exist_ok=True)
    if not WIN and os.path.isfile("/etc/nfstr.env"):      # headless service (deploy/incus): the mode is part of the service settings; changing it restarts the server
        env = dict(l.rstrip("\n").split("=", 1) for l in open("/etc/nfstr.env") if "=" in l)
        def write_env(**kv):
            env.update(kv); open("/etc/nfstr.env", "w").write("".join("%s=%s\n" % (k, v) for k, v in env.items()))
        if cmd == "score" or cmd == "score-stop":
            modes = rest[0] if cmd == "score" else "vanilla"
            kv = {"NFSTR_MODES": modes}
            if "--every" in rest: kv["NFSTR_EVERY"] = rest[rest.index("--every") + 1]
            if "--elim" in rest: kv["NFSTR_ELIM"] = rest[rest.index("--elim") + 1]
            write_env(**kv); print("restarting the server with the new mode (players are disconnected for about a minute)")
            sys.exit(subprocess.call(["systemctl", "restart", "nfstr"]))
        if cmd in ("stop", "start", "status"):
            sys.exit(subprocess.call(["systemctl", cmd if cmd != "start" else "restart", "nfstr"]))
    if not WIN:                                           # Linux: the tested path
        sh = os.path.join(ROOT, "tools", "host_server.sh"); rnd = os.path.join(ROOT, "tools", "round.sh")
        if cmd == "start": sys.exit(subprocess.call([sh] + rest))
        if cmd == "stop": sys.exit(subprocess.call([sh, "stop"]))
        if cmd == "score": sys.exit(subprocess.call([rnd, "score"] + rest))
        if cmd == "score-stop": sys.exit(subprocess.call(["systemctl", "--user", "stop", "nfstr-menu-score"]))
        if cmd == "status": sys.exit(subprocess.call([rnd, "status"]))
        sys.exit(__doc__)
    # ---- Windows
    if cmd == "run": return windows_wizard(rest)
    game = os.path.join(ROOT, "online_test")
    if "--game-dir" in rest: i = rest.index("--game-dir"); game = rest[i + 1]; del rest[i:i + 2]
    if cmd == "stop": stop(); print("stopped"); return
    if cmd == "score-stop": stop(("score",)); return
    if cmd == "status": print(json.dumps(pids())); return
    if cmd == "score":
        modes = rest[0]; every = 3; elim = "hold"
        if "--every" in rest: every = int(rest[rest.index("--every") + 1])
        if "--elim" in rest: elim = rest[rest.index("--elim") + 1]
        return start_score(modes, every, elim)
    if cmd != "start" or not rest: sys.exit(__doc__)
    addr = rest[0]; modes = rest[1] if len(rest) > 1 else "plague"; every = int(rest[2]) if len(rest) > 2 else 3; elim = rest[3] if len(rest) > 3 else "hold"
    start_windows(game, addr, modes, every, elim)

main()
