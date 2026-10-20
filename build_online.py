#!/usr/bin/env python3
"""Builds dist/NFSTR_Online-<ver>.zip - ONE zip with everything. Extract it into the game folder (next to the game exe):
  dinput8.dll, Scripts/ (plugin + ini), NFSTR_Online_Play.sh, StartServer.bat/.sh ...  = what players need
  NFSTR_Server/   = the server (Python) for hosts          NFSTR_Extras/ = LAN mod, deployment files
Usage: python build_online.py <version> [default server address for the player ini]"""
import os, subprocess, sys, zipfile
R = os.path.dirname(os.path.abspath(__file__)); ver = sys.argv[1] if len(sys.argv) > 1 else "0.1.0"
HOST = sys.argv[2] if len(sys.argv) > 2 else "127.0.0.1"
subprocess.check_call([os.path.join(R, "tools", "build_plugin.sh")], stdout=subprocess.DEVNULL)
out = os.path.join(R, "dist", "NFSTR_Online-%s.zip" % ver); os.makedirs(os.path.dirname(out), exist_ok=True)
INI = ("[Server]\n; address (IP or domain) of the server you want to play on\nHost=%s\n\n[Player]\n; your name: 3-16 letters, digits, _ or -\nName=Player\n\n"
       "[Origin]\nEnable=1\nStartDelayMs=20000\n" % HOST)
README = ("NFSTR Online (no game files included)\n\n"
          "1. Make a backup of your game folder, then extract this zip into it (next to 'Need For Speed The Run.exe').\n"
          "2. PLAY: open Scripts/NFSTR_Online.ini, set Host= to the host's address and Name= to your player name.\n"
          "   Start the game (Linux: ./NFSTR_Online_Play.sh), wait ~20 seconds at the title screen, press Enter, then Multiplayer > Quick Match.\n"
          "3. HOST: Windows: double-click StartServer.bat (needs Python 3). Linux: ./StartServer.sh   (details: NFSTR_Server/README.md).\n\n"
          "Problems? Read Scripts/NFSTR_Online.log. Delete the added files to go back to the normal game.\n")
LICENSE = "dinput8.dll is the Ultimate ASI Loader v9.7.4 by ThirteenAG (MIT License), https://github.com/ThirteenAG/Ultimate-ASI-Loader\n"

def add_tree(z, src, dst, skip=lambda f: False):
    base = os.path.join(R, src)
    for d, dirs, fs in os.walk(base):
        dirs[:] = [x for x in dirs if x != "__pycache__"]
        for f in sorted(fs):
            if f.endswith((".pyc", ".log")) or skip(f): continue
            full = os.path.join(d, f); z.write(full, os.path.join(dst, os.path.relpath(full, base)))

with zipfile.ZipFile(out, "w", zipfile.ZIP_DEFLATED) as z:
    # --- player files (zip root = game folder)
    z.write(os.path.join(R, "build", "NFSTR_Online.asi"), "Scripts/NFSTR_Online.asi")
    z.writestr("Scripts/NFSTR_Online.ini", INI)
    z.write(os.path.join(R, "client", "NFSTR_Online_Play.sh"), "NFSTR_Online_Play.sh")
    loader = os.path.join(R, "third_party", "dinput8.dll")
    if os.path.isfile(loader):
        z.write(loader, "dinput8.dll"); z.writestr("dinput8.LICENSE.txt", LICENSE)
    z.writestr("README.txt", README)
    for f in ("StartServer.bat", "StopServer.bat", "AdminConsole.bat", "StartServer.sh"): z.write(os.path.join(R, f), f)
    # --- server: NFSTR_Server/ is the root the scripts work from
    S = "NFSTR_Server/"
    add_tree(z, "server", S + "server", skip=lambda f: f.startswith("test_") or f in ("lsx.log", "key.pem", "cert.pem", "announcements.json"))
    for f in ("host_server.sh", "round.sh", "net_setup.sh", "prepare_host.sh", "host_headless.sh", "host.py", "procmem.py", "admin.py",
              "online_scoring.py", "race_results.py", "gen_news.py", "make_server_exe.py", "probe_limits.py"):
        z.write(os.path.join(R, "tools", f), S + "tools/" + f)
    add_tree(z, "tools/launch", S + "tools/launch")
    z.write(os.path.join(R, "launcher", "modes.py"), S + "launcher/modes.py")
    z.write(os.path.join(R, "README.md"), S + "README.md"); z.write(os.path.join(R, "docs", "NOTES.md"), S + "docs/NOTES.md")
    add_tree(z, "docs/img", S + "docs/img")
    z.write(os.path.join(R, "deploy", "online", "firewall.nft"), S + "deploy/firewall.nft")
    z.write(os.path.join(R, "deploy", "incus", "nfstr.service"), S + "deploy/incus/nfstr.service")
    z.write(os.path.join(R, "plugin", "nfstr_online.c"), S + "plugin/nfstr_online.c")
    z.write(os.path.join(R, "plugin", "lsx_embedded.h"), S + "plugin/lsx_embedded.h")
    # --- extras: the older LAN mod (join with a code, no server)
    E = "NFSTR_Extras/LAN_mod/"
    add_tree(z, "launcher", E + "launcher")
    add_tree(z, "relay", E + "relay", skip=lambda f: f.startswith("test_")); add_tree(z, "lobby", E + "lobby")
    z.write(os.path.join(R, "build", "NFSTR_LAN.asi"), E + "NFSTR_LAN.asi")
print("built", out, round(os.path.getsize(out) / 1e6, 1), "MB")
