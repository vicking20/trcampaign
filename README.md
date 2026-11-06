# NFSTR Online

A community server for **Need for Speed: The Run** (PC, 1.1.0.0). Login, news, playlists and multiplayer races on race servers
that start on demand. You need your own copy of the game; no game files are included. See [CREDITS.md](CREDITS.md).

## What is here

```text
source/         Go server (cmd/nfsrun-server), SQLite storage, admin web page, race modes, ban list
plugin/         game plugin (NFSTR_Online.asi): Origin stand-in, password login, traffic and forced-car patches
client/         join launchers (.sh / .bat) for players
helper/         Python Origin stand-in (lsx.py) used by the launchers
tools-linux/    race-server launcher (Proton), service installer
tools/          build_plugin.sh, exe patch helpers
docs/           protocol and configuration notes
```

## Run the server (Linux)

1. Build: `cd source && go build -o ../nfsrun-server ./cmd/nfsrun-server`, and the plugin with `tools/build_plugin.sh` (needs mingw-w64).
2. Copy `config/server.env.example` settings into `config/server.env` (addresses, `DS_GAME_DIR`, `ADMIN_WEB_*`). Keep this file private.
3. Run `tools-linux/install_services.sh` (starts the server as a user service, also after a reboot), or run `nfsrun-server --config config/server.env` by hand. The SSLv3 gateway the game needs is built into the server.
4. Admin page: `http://<server address>:8090` (login from `server.env`): players, bans, race servers, traffic, forced cars, news, in-game messages, race modes, accounts.

## Releases

Two packages are planned for GitHub Releases:
- **Server zip**: `nfsrun-server`, `tools-linux/`, `data/`, `config/server.env.example`, the plugin, and the NetFix cop guard (needs the game folder; Linux hosting with Proton).
- **Client zip**: extract into the game folder: `dinput8.dll`, `Scripts/NFSTR_Online.asi`, `Scripts/lsx.py`, `Scripts/NFSTR_Online.ini`, and the join launchers.

Neither is built yet (`tools/` will get a build script). Hosts hand each player their address, name and password.

## Play

Players put the plugin files in the game folder and run `client/NFSTR_Online_Play.sh` (or `.bat`). Their name and password go in `Scripts/NFSTR_Online.ini`; the host creates the account in the admin page.

## Status

Working: login, playlists, multi-race sessions, results and points, admin page, race modes (traffic and car sets), cannonball scoring (`score` command).
Not done: AI/cops (crash the race server), internet hosting guide, Windows hosting scripts (the old MariaDB-based ones were removed).
