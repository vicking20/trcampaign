# Multiplayer and race servers

## How the game plays online

Online playlists run on **dedicated race servers**. The client asks Blaze for
a `CLIENT_SERVER_DEDICATED` game, waits in matchmaking, and connects to the
race server once it sets the game attribute `OkToConnect="true"`. The retail
client cannot host a race itself.

The server mode is still inside the retail PC exe, so the server creates the
race-server exe from the owner's own copy; nothing proprietary is
distributed.

## Enabling race servers

1. Install the game on the server machine (no patch needed).
2. In `config\server.env`:

   ```ini
   DS_ENABLED=true
   DS_GAME_DIR=C:\Games\NFS The Run
   ```

   First-run setup does this when a game folder is given.
3. Open the relay ports (`tools\open_ds_firewall.bat` plus your router or
   hosting firewall): UDP 6100-6163.

At start the server writes `NFSTR_DedicatedServer.exe` next to the game exe,
pointed at `BASE_DOMAIN` (see
[CLIENT_PATCHING.md](CLIENT_PATCHING.md#copy-made-by-the-server)); the
original exe is not changed.

## Settings

| Key | Default | Description |
|---|---|---|
| `DS_ENABLED` | `false` | Run race servers. |
| `DS_GAME_DIR` | empty | Game folder. |
| `DS_COUNT` | `1` | Races at the same time (1-64). |
| `DS_IDLE` | `0` | Race servers kept running while nobody plays. |
| `DS_IDLE_EXIT` | `60` | Seconds before an idle on-demand race server stops. |
| `MM_TIMEOUT` | `45` | Seconds Quick Match waits for a race server. |
| `DS_SETUP_WAIT` | `10` | Seconds players wait for the race server to set the game up. |
| `DS_ALLOW_SOLO` | `true` | A playlist keeps running with one player. |
| `DS_PORT_BASE` | `25200` | First race-server game port. |
| `DS_LISTEN_OFFSET` | `100` | Frostbite `-listen` port = game port + offset. |
| `RELAY_ENABLED` | `true` | Players reach race servers through the relay. |
| `RELAY_PORT_BASE` / `RELAY_PORT_COUNT` | `6100` / `64` | Relay port range. |
| `MM_DEFAULT_PLAYLIST` | Muscle | Always in the Quick Match pool. |

All keys, including the advanced ones (`DS_EXTRA_ARGS`, `DS_MESH_PORT`,
`DS_PUBLIC_IP`, `RELAY_DS_HOST`, ...):
[CONFIGURATION.md](CONFIGURATION.md#race-servers).

## Race-server command line

Instance N (port = `DS_PORT_BASE + N - 1`):

```text
NFSTR_DedicatedServer.exe
  -Server.ServerName NFSTR_DS_<N>_port<port>
  -GamePort <port>
  -listen <DS_LISTEN_IP>:<port+100>-<port+100>
  -serverInstancePath <DS_INSTANCE_DIR>\instance<N>
  -instanceName NFSTR_DS_<N>_port<port>
  -Game.DefaultLayerInclusion StartupMode=Game;GameMode=CareerMode
  -NfsOnline.Backend Backend_Blaze
  -Server.IsPresenceEnabled true
  -NfsOnline.AllowSinglePlayerOnline true      (DS_ALLOW_SOLO=true)
  [DS_EXTRA_ARGS]
```

- `-Server.IsPresenceEnabled true` is required: without it the exe always
  uses the LAN backend and never logs into Blaze.
- `-GamePort` is the Blaze mesh port the server reports to players;
  `-listen` must use a different port.
- The engine splits its command line on spaces and ignores quotes, so no
  value may contain a space.
- Engine init errors go to `<DS_INSTANCE_DIR>\instance<N>\console.log`.

## Lifecycle

1. A player starts Quick Match. With no free race server, the server starts
   one (up to `DS_COUNT`).
2. The race server logs into Blaze with the server account built into the
   exe (persona `NFS-DS-NN`), creates its game, and the game goes to the pool
   (RESETABLE).
3. Matchmaking resets the game with the player's settings. The player is held
   until the race server has set the game up (PRE_GAME, route, session), then
   gets the game.
4. Players connect; the race server runs the playlist and its vote.
   `replayGame` takes the game back to PRE_GAME for the next race.
   Later players join the running game ("joining a race in progress": they
   start at the back of the pack); they get the whole game as the race
   server set it up.
5. When the game ends the race server exits. One without players stops after
   `DS_IDLE_EXIT` seconds unless it is one of the `DS_IDLE` kept servers.

A race server that dies before its game is ready counts as a failed start
and is retried after `DS_RESTART_DELAY`. Crashes are logged with the Windows
Application Error details (module, exception code, offset).

## Joining friends and groups

| What the player does | Blaze request | Server |
|---|---|---|
| Friends list → join | `joinGame` GID 0, USER = the friend | puts the player in the friend's game |
| Accepts an invite | lookupUser(inviter) → `joinPlaygroup` / `joinGame` by player | ULST in the inviter's user data names its game (4/1/gid) and playgroup (6/1/pgid) |
| Group leader starts Quick Match / a playlist | `startMatchmaking` BTPL = playgroup (6/1/pgid) | every member goes to the same game with IndirectMatchmakingSetupContext |
| Group leader joins a friend | `joinGame` BTPL = playgroup | members follow with IndirectJoinGameSetupContext |

Group members do not start matchmaking themselves; their game checks that
the setup's GRID is its own playgroup.

Attribute maps (`ATTR`, `PATT`, playgroup attributes) are sent in Blaze's
case-insensitive key order: the client looks keys up by binary search in the
order it receives them. A case-sensitive order makes the `playListId`
lookup fail at join and crashes a player who joins a game that already has
`RouteIndex` / `SessionIndex`.

## Playlists

`data\playlists.txt` is the Quick Match pool: one GUID per line. Quick Match
picks one at random for each new game; playlists players choose in the menu
are added automatically, and `MM_DEFAULT_PLAYLIST` is always included.

| GUID | Playlist |
|---|---|
| `F3E31DF0-82C0-4F21-A56E-8C6F614D4C29` | Supercar |
| `75286283-5078-420B-8B59-796A01371E2C` | Underground |
| `40808F8D-99F3-44D6-AE7F-0F06B2D84FD3` | Mixed |
| `6530CB16-7317-4945-AA78-8B1EC2480534` | Exotic |
| `A2BD2E19-116D-43A6-A338-D47F6D1EA729` | Muscle |
| `C516C96D-3870-4D57-8B06-CACEDF60E187` | Need For Speed |

The game data has 10 more playlists that are not in its menu set (European
Elite, All American, Japan, Nissan Showdown, Porsche Unleashed, Classics,
Insomnia, AWD, Ford, Lamborghini Untamed). `DS_EXTRA_PLAYLISTS=true` and the
launcher's `EXTRA_PLAYLISTS=1` add them to the set. This is experimental and
off by default.

## UDP relay

Behind NAT a player's packets reach the race server from a translated port,
while the race server only knows the address the player reported to Blaze,
and drops them. The relay avoids that without any router change on the
player's side:

```text
player  ──UDP──▶  relay port P (nfsrun-server.exe)  ──▶ 127.0.0.1:<game port> (race server)
player  ◀──────── P  ◀── 127.0.0.1:Q  ◀──────────────── race server
```

The player is told P as the race server's address; the race server is told
127.0.0.1:Q as the player's. Replies go to wherever the player's packets
come from. One relay port per player in a race.

## Ports

| Port | Who sends | Firewall |
|---|---|---|
| UDP 6100-6163 | players -> relay | open (Windows and router/hosting) |
| UDP 25200.. (`DS_PORT_BASE`) | relay -> race server | none (127.0.0.1) |
| UDP 25300.. (`-listen`) | race server | none |
| UDP 9999, one random port | race server | none |

With `RELAY_ENABLED=false` players send to the race servers directly: open
their ports with `tools\open_ds_firewall.bat direct`. With `RELAY_DS_HOST`
(race servers on another machine) their game ports must be reachable from
this server.

At start the server checks the Windows firewall rule
`NFSTR race servers (UDP)` and warns when it does not cover the relay range.
