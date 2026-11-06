# NFSTR Online

A community server for **Need for Speed: The Run** (PC, version 1.1.0.0): login, news, playlists and multiplayer races on race servers that start on demand. You need your own copy of the game; no game files are included. See [CREDITS.md](CREDITS.md).

## Play

1. Extract `NFSTR_Client.zip` into the game folder (next to `Need For Speed The Run.exe`).
2. Fill in `Scripts/NFSTR_Online.ini` (host address, your name, your password; the host gives you these), or leave it empty and the launcher asks on first run.
3. Start `NFSTR_Online_Play.bat` (Windows) or `./NFSTR_Online_Play.sh` (Linux: needs `umu-run` or `wine`). Close the EA app / Origin first.
4. In the game: Multiplayer > Quick Match.

Problems: read `Scripts/NFSTR_Online.log` and `Scripts/lsx.log`. Another `dinput8.dll` in the folder (ReShade, Special K) can clash.

## Host

1. Install the game on the server machine and extract `NFSTR_Server_Windows.zip` or `NFSTR_Server_Linux.zip` into the game folder. It becomes `<game folder>/NFSTR_Server/`.
2. Start `Start_Server.bat` (Windows) or `./start.sh` (Linux; race servers run through Proton/umu-run, see `tools-linux/ds_launch.sh`).
3. The first start creates `config/server.env` and the certificate, and prints the admin address and password once.
4. Open the admin page (`http://<server address>:8090`), create player accounts under Accounts, and give each player the address, name and password.
5. Open the ports (below) if players are not on your network.

| Ports | Use |
|---|---|
| TCP 42127, 10041, 18443, 8443 | login, game connection, profile/news, password login |
| UDP 6100 and up (`RELAY_PORT_COUNT`, default 64: one per player in a race) | race traffic |
| TCP 8090 | admin page (keep it private) |

On Windows `tools/open_ds_firewall.bat` (in the server zip) opens the UDP ports. Settings: `config/server.env`, see [docs/CONFIGURATION.md](docs/CONFIGURATION.md). Linux: `tools-linux/install_services.sh` runs the server as a user service.

## Features

- Password login, accounts and bans from the admin page; data in one SQLite file.
- Quick Match with race servers that start on demand (the game's own server mode, built from your game exe); several races at once.
- Playlists of the game (6 menu playlists; the 10 unused ones with `DS_EXTRA_PLAYLISTS`).
- Admin page: players, kick/ban, race servers, traffic density, forced cars, race modes, news, in-game messages, scoring (`score cannonball`), and options: join a running race, remember game logins, number of race servers.
- Console commands: type `help` in the server window or use the admin page's console.

## Build

```text
cd source && go build -o ../nfsrun-server ./cmd/nfsrun-server
tools/build_plugin.sh        game plugin (needs mingw-w64)
tools/build_release.sh       the three zips in dist/
```

Layout: `source/` Go server and `cmd/nfstr-origin` (the player's Origin stand-in), `plugin/` game plugin, `client/` launchers, `tools-linux/` race-server launcher, `data/` and `client_config/` served files, `docs/` reference.
