#!/usr/bin/env python3
"""Builds dist/NFSTR_LAN-<ver>.zip: plugin + launcher + installer. Needs i686-w64-mingw32-gcc.
dinput8.dll (Ultimate ASI Loader) must be placed in third_party/ first (see third_party/README)."""
import os, shutil, subprocess, sys, zipfile
R = os.path.dirname(os.path.abspath(__file__)); ver = sys.argv[1] if len(sys.argv) > 1 else "0.1.0"
out = os.path.join(R, "dist", "NFSTR_LAN-" + ver); shutil.rmtree(out, ignore_errors=True); os.makedirs(out)
subprocess.check_call(["i686-w64-mingw32-gcc", "-O2", "-shared", "-static-libgcc", "-o", os.path.join(out, "NFSTR_LAN.asi"),
                       os.path.join(R, "plugin", "nfstr_lan.c"), "-lws2_32"])
for f in ("nfstr_launcher.py", "nfstr_gui.py", "modes.py", "playlists.json", "install.py"):
    shutil.copy2(os.path.join(R, "launcher", f), out)
shutil.copy2(os.path.join(R, "relay", "agent.py"), out)
ld = os.path.join(R, "third_party", "dinput8.dll")
if os.path.isfile(ld): shutil.copy2(ld, out)
else: print("WARNING: third_party/dinput8.dll missing - bundle will only work on folders that already have an ASI loader")
open(os.path.join(out, "README.txt"), "w").write(
 "NFS The Run LAN\n1. Run:  python install.py \"<your game folder>\"\n2. Start 'NFSTR LAN Launcher'.\n"
 "Host: pick a playlist, Start server, send the join code. Join: paste the code.\n"
 "Game modes: Host > Game mode lists all of them with a one-line description (cannonball, team modes, bounty, streak, hare, plague, halving, ...).\n"
 "Team modes take optional teams: Red=Ana,Bo;Blue=Cy,Di. With a lobby the list shows the standings and the season ladder.\n"
 "Wine/Proton: set WINEDLLOVERRIDES=\"dinput8=n,b\" (the launcher does this itself).\nNo game files are included.\n")
z = out + ".zip"
with zipfile.ZipFile(z, "w", zipfile.ZIP_DEFLATED) as zf:
    for d, _, fs in os.walk(out):
        for f in fs: zf.write(os.path.join(d, f), os.path.join(os.path.basename(out), os.path.relpath(os.path.join(d, f), out)))
print("built", z)
