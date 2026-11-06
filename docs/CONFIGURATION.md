# Configuration reference

Every setting the server reads, with its default
and the accepted values.

| File | Read by | Created by |
|---|---|---|
| `config/server.env` | `nfsrun-server` | you (start from `server.env.example`) |
| `config/nfsrun.db` | `nfsrun-server` | the server (SQLite database) |
| `nfsrun-launcher.ini` | `nfsrun-launcher.exe` (player PC) | launcher on first start |
| `nfsrun-launcher.key` | `nfsrun-launcher.exe` (player PC) | launcher on first start |
| `client_config\<SECTION>.cfg` | `nfsrun-server.exe` | repository |
| `data\...` | `nfsrun-server.exe` | repository / server at runtime |

Format of the `.env` and `.ini` files: one `KEY=value` per line, `#` or `;`
starts a comment line, keys are not case sensitive. A missing key keeps its
default. Restart the server (or launcher) after a change.

Boolean values: `true`/`1`/`yes`/`on` and `false`/`0`/`no`/`off`.

---

## `config\server.env`

"Default" is the built-in value used when the key is absent. Where first-run
setup writes a different value, it is listed as "setup".

### Network

| Key | Default | Values | Description |
|---|---|---|---|
| `PUBLIC_HOST` | `127.0.0.1` (setup: `redirector.<domain>`) | host name or IP | Address the server hands out to players (Blaze host, web Autolog host, launcher). |
| `PUBLIC_IP` | `127.0.0.1` (setup: asked) | IPv4 | Public IPv4 sent in the Redirector reply; also the race servers' address when `DS_PUBLIC_IP` is empty. |
| `BASE_DOMAIN` | empty (setup: your domain) | domain, at most 16 characters | Domain of the four Redirector hosts. The launcher patches it into its game copy and the server into the race-server exe (a game exe already patched with another domain keeps its own). Also the certificate's domain. |
| `BLAZE_PORT` | `10041` | 1-65535 | Blaze port sent to the game in the Redirector reply. |
| `REDIRECTOR_FRONT_LISTEN` | `:42127` | `[ip]:port` | Public Redirector port; forwarded to the built-in SSLv3 gateway. |
| `REDIRECTOR_GATEWAY_TARGET` | `127.0.0.1:42129` | `ip:port` | Where the Redirector front forwards to (the built-in gateway). |
| `REDIRECTOR_LISTEN` | `127.0.0.1:42128` | `ip:port` | Redirector backend (behind the gateway). |
| `BLAZE_FRONT_LISTEN` | `:10041` | `[ip]:port` | Public Blaze port. |
| `BLAZE_LISTEN` | `127.0.0.1:10042` | `ip:port` | Blaze backend the front forwards to. |
| `HTTP_LISTEN` | `:18080` | `[ip]:port` | Plain HTTP: web Autolog backend, Locker files, `/health`. The launcher API is refused here. |
| `CERT_FILE` | `certs/protossl_redirector.crt` | path | Redirector certificate. |
| `KEY_FILE` | `certs/protossl_redirector.key` | path | Its private key. |
| `DATABASE_ENV` | `config/nfsrun.db` | path | The SQLite database file (the key name is kept from older configs). |
| `WEB_AUTOLOG_HOST` | empty | empty, `host:port`, `off` | Web Autolog host sent to the game as `V8ServerName`. Empty = `PUBLIC_HOST:18443`; `off` = not sent, and the gateway's HTTPS front is no longer restored at start. |
| `LOCKER_BASE_URL` | empty | URL | Base URL for photo uploads/downloads. Empty = `http://<PUBLIC_HOST>:<HTTP_LISTEN port>`. |

### Launcher API (HTTPS)

See [LAUNCHER_HTTPS.md](LAUNCHER_HTTPS.md) for the direct and Cloudflare setups.

| Key | Default | Values | Description |
|---|---|---|---|
| `LAUNCHER_HTTPS_LISTEN` | `:8443` | `[ip]:port`, `off` | HTTPS listener for the launcher API (`/nfsrun/...`) and `/health`. `off` = no HTTPS: the launcher API moves to `HTTP_LISTEN` (tests only). |
| `LAUNCHER_TLS_CERT` | `certs/launcher.crt` | path | Certificate. Created self-signed (ECDSA P-256, 10 years) when missing; replace it with a CA or Cloudflare Origin certificate if you have one. |
| `LAUNCHER_TLS_KEY` | `certs/launcher.key` | path | Its private key. |
| `LAUNCHER_URL` | empty | `https://host[:port]` | Address players use, when it is not `https://PUBLIC_HOST:<port>` (e.g. a Cloudflare-proxied host). Set = no certificate pin in the player launcher. |
| `TRUSTED_PROXIES` | empty | `cloudflare`, IPs, CIDRs, comma separated | Reverse proxies whose `CF-Connecting-IP` / `X-Forwarded-For` give the player's real address. `cloudflare` = Cloudflare's published ranges. Empty = trust none. |

### Login and access

| Key | Default | Values | Description |
|---|---|---|---|
| `AUTH_MODE` | `invite` | `invite`, `open` | `invite`: a new player needs a one-time invite code (`invite <name>`). `open`: the first launcher key that uses a free name owns it. In both modes a name with a pending code waits for that code. |
| `AUTH_SESSION_HOURS` | `8` | 1-72 | Lifetime of a launcher session ticket; the launcher renews it while running. |
| `FRIENDS_ALL` | `true` | boolean | Every player is everyone's friend. `false`: only friends added in game. |
| `CLIENT_GUARD` | `true` | boolean | Only the game may use the public ports (protocol checks, login required, HTTP user agent check). |
| `GUARD_ALLOW_IPS` | empty | comma-separated IPs/CIDRs | Allowlist for the public ports. Empty = everyone. |
| `GUARD_MAX_CONN_PER_IP` | `8` | 0 or more | Simultaneous connections per IP; `0` = no limit. |

### Matchmaking

| Key | Default | Values | Description |
|---|---|---|---|
| `MM_MODE` | `auto` | `auto`, `dedicated`, `peer` | `auto`: race servers when available (the retail client always asks for one). `dedicated`: always wait for a race server. `peer`: player-hosted games (tests only; the retail client cannot host). |
| `MM_TIMEOUT` | `45` | 5-600 s | How long Quick Match waits for a race server before "matchmaking failed". |
| `MM_DEFAULT_PLAYLIST` | `A2BD2E19-116D-43A6-A338-D47F6D1EA729` (Muscle) | playlist GUID(s), comma separated | Always in the Quick Match pool. GUIDs: see `data\playlists.txt`. |
| `MM_QUICK_EXTRA` | `false` | boolean | Extra playlists may enter the Quick Match pool (only when every player has `EXTRA_PLAYLISTS=1`). |

### Race servers

| Key | Default | Values | Description |
|---|---|---|---|
| `DS_ENABLED` | `false` | boolean | Build `NFSTR_DedicatedServer.exe` from your game exe and run race servers. |
| `DS_GAME_DIR` | empty | folder | Game folder containing `Need For Speed The Run.exe`. |
| `DS_SOURCE_EXE` | `Need For Speed The Run.exe` | file name | Game exe the race-server exe is made from. |
| `DS_EXE` | `NFSTR_DedicatedServer.exe` | file name | Race-server exe (created/updated automatically in `DS_GAME_DIR`). |
| `DS_COUNT` | `1` | 0-64 | Most race servers running at the same time (= races at once). |
| `DS_IDLE` | `0` | 0-64 | Race servers kept running while nobody plays. `0` = started on demand by Quick Match. |
| `DS_IDLE_EXIT` | `60` | 5-86400 s | An on-demand race server without players stops after this time. |
| `DS_RESTART_DELAY` | `10` | 1-3600 s | Wait before restarting a race server that failed to start. |
| `DS_SETUP_WAIT` | `10` | 1-120 s | Players get their game once the race server reaches PRE_GAME, or after this time. |
| `DS_PORT_BASE` | `25200` | 1024-65000 | Race server N uses UDP `DS_PORT_BASE+N-1` (`-GamePort`). |
| `DS_LISTEN_OFFSET` | `100` | 0-1000 | The Frostbite `-listen` socket uses port + offset. |
| `DS_LISTEN_IP` | `0.0.0.0` | IPv4 | Interface for `-listen`. |
| `DS_PUBLIC_IP` | empty | IPv4 | Address players are told for race servers. Empty = `PUBLIC_IP` when it is a public address, else looked up. |
| `DS_MESH_PORT` | `0` | 0-65535 | Port players are told for race servers; `0` = what the race server reports. |
| `DS_INSTANCE_DIR` | `data/ds` | folder | Per-instance `-serverInstancePath` root and `console.log` files. |
| `DS_KILL_STALE` | `true` | boolean | End leftover race-server processes at start. |
| `DS_ALLOW_SOLO` | `true` | boolean | A playlist keeps running with one player. |
| `DS_EXTRA_PLAYLISTS` | `false` | boolean | Add the 10 unused playlists to the race servers' data (experimental). |
| `DS_LAUNCHER` | empty | program | Starts the race-server exe: called as `DS_LAUNCHER <exe> <arguments>`. Linux: `tools-linux/ds_launch.sh` (Proton). |
| `DS_KEEP_HOSTS` | `false` | boolean | Leave the exe's Redirector host names unpatched (a plugin such as NFSTR_Online redirects them). |
| `DS_EXTRA_ARGS` | empty | arguments | Extra race-server arguments, space separated, no spaces inside values. `-NfsOnline.EnableProtoTunnel` is removed. |

### UDP relay

| Key | Default | Values | Description |
|---|---|---|---|
| `RELAY_ENABLED` | `true` | boolean | Players send race traffic to relay ports of the server, which forwards it to the race servers on 127.0.0.1. Works behind any NAT. |
| `RELAY_PORT_BASE` | `6100` | 1024-65000 | First relay port. |
| `RELAY_PORT_COUNT` | `64` | 1-1000 | Number of relay ports (one per player in a race). Open `RELAY_PORT_BASE` .. `+COUNT-1` UDP. |
| `RELAY_DS_HOST` | empty | IPv4 | Race servers on another machine: where the relay sends to. Empty = 127.0.0.1. |

### Logging, console and diagnostics

| Key | Default | Values | Description |
|---|---|---|---|
| `CONSOLE_MODE` | `dashboard` | `dashboard`, `log` | Server window: compact dashboard, or the full scrolling log. |
| `CONSOLE_REFRESH` | `15` | seconds | Full redraw interval of the dashboard. |
| `LOG_MAX_MB` | `200` | MB, `0` = never | Rotate `logs\server.log` above this size (5 old files kept). |
| `LOG_TDF` | `false` | boolean | Log every Blaze packet decoded (credentials are masked). Large logs; for debugging. |
| `SCHEMA_CHECK` | `false` | boolean | Check every packet sent against the game's TDF schema and log `[SCHEMA]` issues (development). |
| `CLIENT_CONFIG_DIR` | `client_config` | folder | Folder of the `<SECTION>.cfg` files. |
| `LOCKER_DIR` | `data/locker` | folder | Uploaded photos. |

---

## Admin web page, bans and race settings

| Key | Default | Description |
|---|---|---|
| `ADMIN_WEB_LISTEN` | off | Address and port of the admin page, e.g. `0.0.0.0:8090`. |
| `ADMIN_WEB_USER` | `admin` | Login name of the admin page. |
| `ADMIN_WEB_PASSWORD` | none | Password of the admin page. Keep `server.env` private. |
| `BANS_FILE` | `config/bans.txt` | Banned names, one per line (edited from the admin page). |
| `RACE_SETTINGS_FILE` | `config/race_settings.json` | Traffic, forced cars and mode chosen on the admin page; written into the game's `Scripts/NFSTR_Online.ini` before each race server starts. |

---

## `nfsrun-launcher.ini` (player)

Created on the first start with the settings below. The server address is
built into the launcher by the server owner (`--stamp-launcher`).

| Key | Default | Values | Description |
|---|---|---|---|
| `PLAYER_NAME` | empty (`<COMPUTERNAME>.0`) | 1-16 characters | Player name. |
| `INVITE_CODE` | empty | code from the server owner | One-time invite code for the first login (or after `reset-key`); cleared after use. |
| `LANGUAGE` | `auto` | see below | Game and launcher language. |
| `GAME_EXE` | `Need For Speed The Run.exe` | file name or full path | Game executable. |
| `GAME_ARGS` | empty | arguments | Extra game arguments. `-NfsOnline.EnableProtoTunnel` is removed. |
| `LSX_LISTEN` | `127.0.0.1:3216` | `ip:port` | Local Origin endpoint the game connects to; the game copy is patched for another port. |
| `SOLO_ONLINE` | `1` | `0`, `1` | Keep playing a playlist alone. |
| `INVITES` | `ask` | `ask`, `auto`, `off` | Friends' invites: a Yes/No box (as Origin did; it can minimise a fullscreen game), accept at once, or ignore. |
| `AUTO_START` | `0` | `0`, `1` | Start the game as soon as the launcher is ready. |
| `CLOSE_WITH_GAME` | `0` | `0`, `1` | Close the launcher when the game exits. |

Advanced keys (not in the file; add them when needed):

| Key | Default | Values | Description |
|---|---|---|---|
| `SERVER_HTTP` | built in | `https://host:8443`, `off` | Another server than the built-in one (its pin and domain are then not used); `off` = offline. |
| `SERVER_CERT_PIN` | built in | 64 hex characters, `off` | SHA-256 of the server certificate's public key (self-signed servers). `off` = the certificate must be valid for a public CA. |
| `BASE_DOMAIN` | built in | domain | Redirector domain patched into the game copy. |
| `NO_ORIGIN_START` | `1` | `0`, `1` | `0` = run the original exe (it must be patched with `tools\patch_client.bat` and `patch_origin_startup.bat`; registers the launcher as Origin in HKLM, which needs administrator rights). |
| `EXTRA_PLAYLISTS` | `0` | `0`, `1` | Add the 10 unused playlists to the game data (experimental). `0` restores the originals. |

`nfsrun-launcher.key` holds the player's private login key (`CLIENT_KEY`) and
what they allowed on the first start (`ALLOWED`). Never share it; a lost key
needs `reset-key <name>` on the server. An `nfsrun-launcher.ini` of an older
launcher (with `PERSONA`, `CLAIM_CODE`, `LOCALE`, `CLIENT_KEY`) is converted
on the next start; the old file is kept as `nfsrun-launcher.ini.old`.

`LANGUAGE` values: `en_US`, `fr_FR`, `de_DE`, `es_ES`, `es_MX` (also other
American Spanish), `it_IT`, `ja_JP`, `ru_RU`, `pl_PL`, `nl_NL`, `cs_CZ`,
`zh_TW` (`zh_CN` gives the game's Traditional Chinese). `pt_BR`, `pt_PT` and
`ko_KR` change only the launcher window: the PC game has no Portuguese or
Korean text and stays in English. `auto` = the Windows language.

The game takes its language only from the registry value `Locale` of
`SOFTWARE\EA Games\Need for Speed(TM) The Run` (no value = English; the
language must be installed). The launcher writes it on every start, in HKCU:
its game copy is patched to read HKCU, so no administrator is needed.

On the first start the launcher lists what it will do on the PC (create the
game copy `NFSTR_lsx<port>.exe`, set the game language, and with
`EXTRA_PLAYLISTS=1` change the playlist data) and asks once. It starts no
other program than the game.

Command line: `nfsrun-launcher.exe [-persona Name] [-invite-code CODE] [-console] [-lsx-only]`

| Option | Description |
|---|---|
| `-persona Name` | Use this name instead of `PLAYER_NAME` (second game copy on one PC). |
| `-invite-code CODE` | One-time invite code for this start. |
| `-console` | Text window instead of the launcher window; starts the game at once. |
| `-lsx-only` | Only provide the local Origin endpoint; start the game yourself. |

---

## `client_config\<SECTION>.cfg`

Sections returned by `Util.fetchClientConfig`. `KEY = value` per line, `#`
comments. The game asks for `NFS12_THERUN`:

| Key | Value | Description |
|---|---|---|
| `OnlinePassKillSwitch` | `true` | Turns the Online Pass check off. |
| `V8ServerName` | `host:port` | Web Autolog host. Leave unset: the server sends `WEB_AUTOLOG_HOST` / `PUBLIC_HOST:18443`. |

Other keys the game reads in this section: `V8XlspName`, `V8XlspPort`,
`instantConnectTo`, `PcContentMapLength`.

---

## `data\`

| Path | Description |
|---|---|
| `data\news.json` | News items of the Autolog news screen. Copy `news.example.json` to start. Fields: `NewsId`, `Language` (0 English, 1 French, 2 German, 3 Spanish, 4 Italian, 5 Japanese, 6 Russian, 7 Polish, 8 Dutch, 9 Portuguese, 10 Chinese, 11 Korean, 12 Czech, 13 Spanish (Mexico)), `Subject`, `ShortMessage`, `Message`, `NewsIcon`, `Category` (0 shift2DLC, 1 The Run, 2 New DLC, 3 Autolog, 4 EA, 5 The Run exclusive), `PublishedDate`, `Author`, `Viewed`. Each `NewsId` is shown once per player: their language, else English. |
| `data\news\` | Banner images (`.jpg .jpeg .png .gif`), referenced as `"NewsIcon": "/news/<file>"` (the leading `/` is added when missing; the name is not case-sensitive); `default.jpg` for items without one. Sent as JPEG. |
| `data\legal\` | `terms_of_service.txt`, `privacy_policy.txt`, and per-language `terms_of_service.<LANG>.txt` (e.g. `.ptBR`). Read each time the screen opens. |
| `data\playlists.txt` | Quick Match playlist pool: one GUID per line, `#` comments. The server adds playlists players pick. |
| `data\locker\` | Uploaded photos (runtime). |
| `data\web\` | Per-player web Autolog data (runtime). |
| `data\ds\` | Race-server instance folders (runtime). |

---

## `nfsrun-server.exe` command line

| Option | Description |
|---|---|
| `--config <file>` | Settings file (default `config/server.env`). |
| `--invite <Name>` | Make a one-time invite code for a new player (same as the `invite` command). |
| `--command "<command>" [--yes]` | Run one server-window command (below) and exit, e.g. `--command "players all"`. `--yes` answers its question. Players online in a running server are not disconnected from here: use the server window for `kick`, `reset-key` and `delete-player` while it runs. |
| `--exe-domain <game exe>` | Print the domain a patched game exe connects to. |
| `--make-cert [--domain D] [--game-exe F]` | Write the Redirector certificate (`CERT_FILE`/`KEY_FILE`). Domain from the game exe, else `BASE_DOMAIN`. |
| `--check-cert [--fix]` | Check that the game would accept the certificate; `--fix` makes a new one if not. Exit code 1 = refused. |
| `--stamp-launcher <out.exe> [--launcher-exe <in.exe>]` | Write a copy of the launcher (default `launcher\nfsrun-launcher.exe`) with this server built in (launcher address, certificate pin, Redirector domain). `scripts\make_client_package.ps1` uses it. See [LAUNCHER_HTTPS.md](LAUNCHER_HTTPS.md#launcher-preset). |

Server window keys: `C` (or `/`) opens the command line, `Q` then `Y` stops
everything, `L` toggles the log view, `H` shows help and the commands. Outside
the command line nothing typed is echoed, and clicking the window does not
pause the server. `stop_server.bat` does the same as `Q`.

## Server window commands

`C`, type the command, Enter (Esc cancels). Commands that change or delete
something ask first (`Y`). Every command and its result is written to
`logs\server.log` as `[ADMIN]`. With `CONSOLE_MODE=log` type the commands as
lines in the window.

| Command | Description |
|---|---|
| `players` | Who is online: game or matchmaking, group, address. |
| `players all` | Every player in the database: BlazeId, last login, photos, whether a launcher key owns it. |
| `games` | Games with state, host, playlist, players and the address advertised to players; race-server processes. |
| `kick <name>` | Disconnect a player (game connection and launcher live channel). They can log in again. |
| `invite <name> [hours]` | One-time invite code for a new player (default 12 h, 1-168). The name is reserved until it is used. |
| `reset-key <name> [hours]` | Lost launcher key: disconnect, revoke the key, print a new invite code. Records, photos and friends stay. |
| `invites` / `invites cancel <name>` | Pending invite codes (names and expiry; the codes themselves are not stored) / withdraw one. |
| `rename <old> <new>` | New name; records, photos, friends and the launcher key stay (disconnects the player if online). |
| `delete-player <name>` | Delete a player and all their data: database rows (records, friends, stats, photos, likes, launcher key), photo files in `data\locker\` and web Autolog files in `data\web\`. Asks first; cannot be undone. |
| `package` | Build the player package (`make_client_package`). |
| `help` | The list. |

Names with spaces go in quotes: `kick "Ze Silva"`. Names are not case-sensitive.

---

## Environment variables for tests

| Variable | Description |
|---|---|
| `NFSTR_EXE` | Path of a game exe for the exe patch tests. |
| `NFSTR_GAME_DIR` | Game folder for the playlist patch tests. |
| `SCHEMA_REPORT=1` | Print the schema coverage report. |
