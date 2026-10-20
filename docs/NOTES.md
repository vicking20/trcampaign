# NFSTR Online - technical notes

Everything that is not in the README. Addresses are for the Windows `Need For Speed The Run.exe` 1.1.0.0 (base 0x400000).

## 1. How it fits together

```
 player: game + NFSTR_Online.asi ---- TCP 42127 --->  trserver.py (fake Blaze, SSLv3): login, matchmaking, game manager
          (built-in Origin stand-in)  -- TCP 443 -->  News / XP multiplier / profile uploads (same process)
                                      - 17502 ----->  QoS stubs
                                      - UDP 25200 -->  NFSTR_Server.exe (the game's engine as dedicated server, logs in as `nexus001`)
                                                       online_scoring.py (reads results from the dedicated server's memory)
```
- **Plugin** (`plugin/nfstr_online.c`, an ASI loaded through `dinput8.dll`): redirects the EA host names to the `Host=` server, skips the certificate check,
  fakes the Origin lookup and runs a small Origin (LSX) stand-in on 127.0.0.1:3216 (`plugin/lsx_embedded.h`; `server/lsx.py` is the Python original).
  The player name travels to the server inside the Origin auth token. Options in the ini: `[Origin] Stub=memory|system|off`, `Embedded=0|1`, `[Patch] MaxPlayers=N`, `[Trace]`.
- **Server** (`server/trserver.py`): just enough Blaze for login and Quick Match. The dedicated server is an ordinary Blaze client that creates the game; each
  remote player needs its own connection slot (host = 0).
- **Game modes**: pure scoring functions in `launcher/modes.py`, applied after each race by `tools/online_scoring.py` (rotation, elimination, News).

## 2. Hosting

Scripts: `tools/prepare_host.sh <game folder>` (once), `tools/host_server.sh <address> [modes|vanilla] [races-per-mode] [hold|soft]`, `tools/host_server.sh stop`,
`python3 tools/admin.py`. `tools/host.py` is the cross-platform entry point (Windows part untested).

- **NFSTR_Server.exe** is not shipped with the game: `tools/make_server_exe.py` derives it from your exe (3 bytes at offset 0x180CA, `6a 00 52` -> `52 6a 01`).
- **Address:** the server hands each player the address that player connected to (`auto`, the default), so LAN, VPN and loopback players all work; a fixed address/name can be given instead.
- **Ports (open them only if players come from outside your network or VPN):** TCP 42127 (Blaze), TCP 443 (News; `tools/net_setup.sh <address>` redirects it to 8081), TCP+UDP 17502, UDP 25200. Admin console: 127.0.0.1:42128 only.
- **Options:** `NFSTR_MAX_PLAYERS=16` (experimental), `TR_XP_MULTIPLIER`, `TR_ROSTER=0`, `TR_MAX_CONN`, `TR_MAX_PER_IP`, `TR_RATE_PER_MIN`, `TR_IDLE_TIMEOUT`, `TR_AUTOLOG_PORT`.
  `hold` removes a knocked-out player until the season changes; `soft` lets them rejoin (their results stop scoring).
- **Game data:** the server is the game's engine, so it needs the game's data (~4 GB opened at start, plus each level used; the full game is 16 GB). The original LAN script
  guide says `AudioSuperBundle`, `Chunks0/1/2` (`.sb` + `.toc`, in `Data/Win32` and `Update/Patch/Data/Win32`) can be deleted to save space - not tried here.
  Mount the game folder read-only instead of copying it.
- **Headless / container:** `tools/host_headless.sh start <address> [modes]` runs Xvfb + Wine + the Python server (no Proton, no systemd). The scoring daemon starts the
  dedicated server as its child because `kernel.yama.ptrace_scope=1` only lets a parent read its memory. In a Debian 13 Incus container:
  `dpkg --add-architecture i386 && apt install wine wine32 xvfb python3`, mount the game folder, `tools/prepare_host.sh <mounted game folder>`, then
  `deploy/incus/nfstr.service` + `/etc/nfstr.env` (`NFSTR_ADDRESS`, `NFSTR_MODES`, `NFSTR_EVERY`, `NFSTR_ELIM`) and `systemctl enable --now nfstr`. Forward ports with
  Incus proxy devices. With `/etc/nfstr.env` present, `tools/host.py score ...` and the admin console restart the service to change the mode.
- **Public hosting:** use a DNS-only record (Cloudflare's proxy and tunnels carry web traffic only). The server re-resolves a domain advertise-host every 5 minutes.

## 3. Hardening

Exposed: the Blaze port and News (custom SSLv3 + binary parser), QoS stubs, and the game engine on UDP 25200 (the biggest risk; nothing here validates its packets).
Done: per-address and total connection limits, per-minute rate limit, 600 s idle timeout, message size caps, QoS UDP limits, name sanitising and bans (admin console). Do: run it unprivileged in a VM/container with nothing else on it; `deploy/online/firewall.nft` is a default-drop nftables example.
No real accounts: a name is whatever the plugin sends.

## 4. Blaze protocol notes (SDK 3.15)

- Frames: 12-byte header; the dedicated server uses the extended header (type flag 0x10 = 2 more length bytes). TDF fields must be sent in ascending tag order or later fields vanish.
- Check every callback's schema per code path (the dedicated server's LoginPersona wants a bare SessionInfo, the client a wrapped FullLoginResponse).
- Search flow: `GameManager.0x0D` -> `MSID`, then `NotifyGameSetup` 0x14 (GAME, PROS, QUEU, REAS) and `NotifyMatchmakingFinished` 0x0A; `updateMeshConnection` 0x1D -> `NotifyPlayerJoinCompleted` 0x1E.
  `NotifyPlayerJoining` 0x15 / `PlayerRemoved` 0x28 keep other clients' competitor lists in sync. Fresh GID/MSID per search.
- `HNET` is a struct list of `0x02 + EXIP{IP,PORT} + INIP{IP,PORT} + end`; a union list is dropped and an empty host list crashes the client.
- The dedicated server logs in as `c4win32serv01@ea.com`, persona `nexus001`, and exits ~1 s after login if its ping-site alias differs from the advertised one (`sjc`).
- Tools: `tools/tdftables.py`, `tools/tdfdump.py`.

## 5. Game internals

- The PC exe has class-name strings and typeinfo but no method names. Vtable slots: ServerRaceVehicleComponent 0x2597348, ServerVehicleEntity 0x25983c8, ServerRuleSetEntity 0x25b8e40, ServerRaceManagerEntity 0x25bdd70.
- Presence-service registry at `*(DAT_02881110+0x170)+0x138`, keyed by typeinfo: 0x2aa4800 = HTTP provider, 0x2aaa140 = Autolog service. Autolog requests go to `https://<host>:443/NFS12_A_01/Game/pc/...`.
- Race manager object at 0x27A49B0: `m_maxPlayers` at +0x5c8 (8 in the data, copied by the stores at 0x13cc0d4 / 0x13cc154; the plugin can override it). Unknown: start-grid capacity above 8.
- Dedicated-server state at 0x27A3300+0x1070: 2/3 waiting, 4 car select, 6 race, 7/8 results. Result entries in the static pool 0x02700000-0x03000000:
  `[name\0][track string][n1][n2][float seconds][hash]` (0 = DNF); `tools/race_results.py` reads them with a baseline taken at race start.
- In-race objective text ("Pass 5 other racers") is game data; it cannot be changed from the server.

## 6. News, XP, names

- **News** (`getNews`, JSON array): `NewsId`, `Subject` (title), `ShortMessage` (box text), `Message` (long text), `Author`, `PublishedDate` (`MM/DD/YYYY H:MM:SS AM|PM`), `MetaContent`,
  `NewsViewed`, and **`NewsIcon` = the banner image URL path** (empty leaves the spinner running). Banners are served from `server/news/`; admin announcements are merged in.
- **XP:** the game multiplies award XP by `getPlaylistMultiplier`, which must be a bare JSON number (`1`); `getPlaylistIndex` is also bare, `getSkillLevelRule` is `{"SkillLevel": n}`.
  Player XP lives in the client's save and is uploaded with `POST Persona/updatePersona`.
- **Names:** the plugin puts the name into the auth token; the server decodes it at login and derives the persona id from it.

## 7. Debugging

Find the real caller with a debugger instead of guessing; check that a plugin `[Trace]` hook installed (look for "bad target" in the log); read live memory through
`/proc/<pid>/mem` (`tools/race_results.py`, `tools/probe_limits.py`, `tools/server_poke.py`); Ghidra scripts in `tools/ghidra_scripts` (`DumpFuncs`, `RefsTo`, `FindDisp`, ...).
Tests: `python3 server/test_trserver.py`, `python3 server/test_blaze.py`, `python3 launcher/modes.py`. `tools/round.sh fast on` ends races after 45 s for quick test cycles.

## 8. Open questions

More than two real players and above 8 (start grid, client lobby); internet play and NAT; live rule changes (nitro, tagging, traffic density for a "rush hour" mode) need hooks
in the server's race code (ServerRaceManager start/finish, ServerRaceVehicleComponent per-frame update, ServerVehicleEntity collisions); real accounts; Windows hosting.
