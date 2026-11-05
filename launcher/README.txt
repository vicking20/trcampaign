NFS THE RUN - PLAYER LAUNCHER
=============================

The launcher logs you into the private server and starts the game. It also
acts as the local Origin the game needs, and shows your friends from the
server. The game itself is not included.


SETUP (ONCE)
------------
1. Copy the contents of the player package into the game folder, next to
   "Need For Speed The Run.exe" (PC version 1.1.0.0).

2. Start nfsrun-launcher.exe once. It creates nfsrun-launcher.ini.
   Set your name and the invite code from the server owner there:

     PLAYER_NAME=YourName
     INVITE_CODE=XXXXX-XXXXX

3. Start the launcher again and press PLAY.

The first start lists what the launcher will do on your PC and asks once:
it creates NFSTR_lsx3216.exe, a copy of the game set up for this server,
and saves the game language in the registry (your user only). Your game
files are not changed, and no administrator rights are needed.

Your login key is in nfsrun-launcher.key, created on the first start. Keep
it (back it up) and never share it. A newer player package contains no .ini
or .key, so copying it over the game folder keeps your login.


THE WINDOW
----------
- PLAYER: your name.
- SERVER: online / offline / connecting.
- FRIENDS: players on the server and what they are doing.
- PLAY starts the game. Closing the launcher also closes the game.
- Show log: what the launcher is doing (also in nfsrun-launcher.log).
- Settings: opens nfsrun-launcher.ini. Restart the launcher after a change.


SETTINGS (nfsrun-launcher.ini)
------------------------------
PLAYER_NAME      Your name, 1-16 characters.
                 Default: empty = <COMPUTERNAME>.0

INVITE_CODE      One-time code from the server owner for your first login.
                 Cleared automatically after it is used.

LANGUAGE         Game and launcher language. Default: auto (Windows)
                 en_US  English            fr_FR  French
                 de_DE  German             es_ES  Spanish (Spain)
                 es_MX  Spanish (Mexico)   it_IT  Italian
                 ja_JP  Japanese           ru_RU  Russian
                 pl_PL  Polish             nl_NL  Dutch
                 cs_CZ  Czech              zh_TW  Chinese (Traditional)
                 pt_BR, pt_PT, ko_KR: launcher only (the game has no such
                 text and stays in English). zh_CN = zh_TW in the game.
                 The game's text must be installed for that language.

GAME_EXE         Game executable, relative to the launcher or a full path.
                 Default: Need For Speed The Run.exe

GAME_ARGS        Extra game arguments. Default: empty.

LSX_LISTEN       Local Origin endpoint. Default: 127.0.0.1:3216
                 Change it only when another program already uses it.

SOLO_ONLINE      1 = keep playing a playlist when you are alone
                 0 = leave the session when alone
                 Default: 1

INVITES          Invites from friends:
                 ask  = a Yes/No box, as Origin did (it can minimise a
                        fullscreen game; play windowed or borderless)
                 auto = accept at once: the game joins the friend's group
                        or race
                 off  = ignore invites
                 Default: ask

AUTO_START       1 = start the game as soon as the launcher is ready
                 Default: 0

CLOSE_WITH_GAME  1 = close the launcher when the game exits
                 Default: 0

Advanced (add the line yourself when needed):

SERVER_HTTP      Use another server than the one built into this launcher,
                 e.g. https://play.example.org:8443 (off = offline).
SERVER_CERT_PIN  Fingerprint of that server's HTTPS key (64 hex characters).
EXTRA_PLAYLISTS  1 = add 10 unused online playlists to the game data
                 (experimental; the server must enable them too).
NO_ORIGIN_START  0 = run the original game exe instead of the copy (it must
                 be patched with the tools of the server package, and the
                 launcher needs administrator rights once).


COMMAND LINE
------------
nfsrun-launcher.exe [-persona Name] [-invite-code CODE] [-console] [-lsx-only]

  -persona Name       use this name for this start
  -invite-code CODE   invite code for this start
  -console            text window instead of the launcher window
  -lsx-only           only provide the local Origin; start the game yourself


TWO GAMES ON ONE PC
-------------------
Starting a second launcher while one is running starts a second game copy
on the next LSX port (3217, ...) with the name <PLAYER_NAME>_2 (or -persona)
and its own log, nfsrun-launcher-2.log. For testing only.


TROUBLESHOOTING
---------------
- "Server offline": check your internet connection.
- "This name needs a one-time invite code": set INVITE_CODE from the server
  owner, or ask for a new one if it expired.
- Lost nfsrun-launcher.key (new PC, reinstall): ask the server owner for a
  new code (reset-key), put it in INVITE_CODE and start again. Your records
  are kept.
- "certificate does not match SERVER_CERT_PIN": the server has a new key;
  get the new player package from the server owner.
- "Port 3216 is already in use": close Origin / the EA app or another
  launcher.
- Anything else: send nfsrun-launcher.log to the server owner.
