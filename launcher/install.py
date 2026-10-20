#!/usr/bin/env python3
"""Installs the NFS The Run LAN bundle into the player's own game folder.
   python3 install.py [GAME_FOLDER]      (Windows: py install.py "C:\\Games\\Need for Speed The Run")
No game files are shipped: the server exe is built from the player's own exe."""
import os, shutil, sys

HERE = os.path.dirname(os.path.abspath(__file__))
CLIENT_EXE = "Need For Speed The Run.exe"


def main():
    game = sys.argv[1] if len(sys.argv) > 1 else input("Path to your 'Need for Speed The Run' folder: ").strip().strip('"')
    if not os.path.isfile(os.path.join(game, CLIENT_EXE)):
        sys.exit("'%s' not found in %s" % (CLIENT_EXE, game))
    exe = open(os.path.join(game, CLIENT_EXE), "rb")
    exe.seek(0x180CA); probe = exe.read(3); exe.close()
    if probe != b"\x6a\x00\x52":
        sys.exit("Your game exe is a different build than the one this mod supports (probe %s). Nothing was changed." % probe.hex())

    scripts = os.path.join(game, "Scripts"); os.makedirs(scripts, exist_ok=True)
    shutil.copy2(os.path.join(HERE, "NFSTR_LAN.asi"), scripts)
    for old in ("NFSTR_LANOnlineHelper.asi", "NFSTR_Online.asi"):      # would double-patch / conflict
        p = os.path.join(scripts, old)
        if os.path.isfile(p):
            os.replace(p, p + ".disabled"); print("disabled conflicting plugin:", old)
    loader = os.path.join(game, "dinput8.dll")
    if os.path.isfile(loader):
        print("dinput8.dll already present, keeping it (it must be an ASI loader).")
    else:
        shutil.copy2(os.path.join(HERE, "dinput8.dll"), loader); print("installed ASI loader dinput8.dll")

    dst = os.path.join(game, "NFSTR_LAN"); os.makedirs(dst, exist_ok=True)
    for f in ("nfstr_launcher.py", "nfstr_gui.py", "playlists.json", "agent.py"):
        shutil.copy2(os.path.join(HERE, f), dst)
    sys.path.insert(0, dst)
    import nfstr_launcher
    nfstr_launcher.ensure_server_exe(game)

    if os.name == "nt":
        open(os.path.join(game, "NFSTR LAN Launcher.bat"), "w").write('@echo off\r\ncd /d "%~dp0"\r\npy NFSTR_LAN\\nfstr_launcher.py gui\r\n')
    else:
        p = os.path.join(game, "nfstr_lan_launcher.sh")
        open(p, "w").write('#!/usr/bin/env bash\ncd "$(dirname "$0")"\nexec python3 NFSTR_LAN/nfstr_launcher.py gui\n')
        os.chmod(p, 0o755)
    print("\nDone. Start the launcher from the game folder ('NFSTR LAN Launcher.bat' / nfstr_lan_launcher.sh).")


if __name__ == "__main__":
    main()
