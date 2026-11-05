# Client patching

The repository never contains a game executable. Every patch is applied to
a copy of the player's (or server owner's) own exe, after checking the exact
bytes. Players do not patch anything by hand: the launcher does it.

## Supported executable

NFS The Run PC **1.1.0.0**, SHA-256:

```text
61488c6ef13953b92f3b4fad6e66c3a54d7e0285eb222a0cff08bc519948a22a
```

Other builds have other offsets and are refused.

## Copy made by the launcher

The launcher runs `NFSTR_lsx<port>.exe`, a copy of the game exe next to it
(`source/cmd/nfsrun-launcher/multi.go`). It is rebuilt from the original on
every start and only written when it differs. The original stays unchanged.
The launcher asks once before it creates the copy.

| Address | Bytes | Purpose |
|---|---|---|
| `0x00838BA6` | `68 90 0C 00 00` -> `68 <port>` | LSX port given to the Origin SDK |
| `0x0041826D` | `74 1E` -> `EB 1E` | no single-instance mutex |
| `0x018AEFBC` | `E8 EE 23 95 00` -> `E8 1F 2D 00 00` | the SDK startup call reaches the real startup (retail calls an empty thunk: "sign in to Origin") |
| `0x018B207B` | `75 4B` -> `EB 4B` | Origin "installed" check always passes |
| `0x018B20D8` | `75 0B` -> `EB 0B` | never start Origin / the EA app |
| `0x0188F2E6` | `68 02 00 00 80` -> `68 01 00 00 80` | read the language (`Locale`) from HKCU, so the launcher sets it without administrator rights |
| file `0x02234EA0`.. | Redirector host names | the server's domain, see below |

Redirector hosts (`source/internal/protocert`):

| Retail host | Slot | Private host |
|---|---:|---|
| `gosredirector.ea.com` | 20 | `nfs.<domain>` |
| `gosredirector.scert.ea.com` | 26 | `scert-nfs.<domain>` |
| `gosredirector.stest.ea.com` | 26 | `stest-nfs.<domain>` |
| `gosredirector.online.ea.com` | 27 | `redirector.<domain>` |

The slots are fixed-size, so the domain can have at most 16 characters. The
domain is built into the player package's launcher (`BASE_DOMAIN`).

`NO_ORIGIN_START=0` (advanced launcher setting) runs the original exe
instead. Then the exe must be patched once with the tools in `tools\` (see
[TOOLS.md](TOOLS.md)), and the launcher writes the Origin registry values,
which needs administrator rights once.

## Copy made by the server

With `DS_ENABLED=true` the server writes `NFSTR_DedicatedServer.exe` next to
the game exe in `DS_GAME_DIR` (`source/internal/dspatch`):

| Address | Change | Purpose |
|---|---|---|
| `0x0041835D` | `serverCapable = 0` -> `1` | the race-server mode works |
| `0x0041826D` | `74 1E` -> `EB 1E` | several race servers next to each other |
| `0x00416D92` | `and ecx,3` -> `and ecx,0` | `-dedicated` loads the Win32 data |
| file `0x02234EA0`.. | Redirector host names | `BASE_DOMAIN`, when the source exe is the retail one |

## Playlist data (optional)

`EXTRA_PLAYLISTS=1` (launcher) and `DS_EXTRA_PLAYLISTS=true` (server) add the
10 unused playlists to `NFSGame.sb/.toc` (`source/internal/playlistpatch`).
Originals are kept as `.orig` and restored when the option is off.
Experimental.

## Player package

`scripts\make_client_package.ps1` creates `dist\NFSTR_Client.zip`: the
launcher with this server's address, certificate pin and domain built in,
the player documentation and `SERVER_INFO.txt`. It contains no
`nfsrun-launcher.ini` or key file, so a newer package can be copied over an
old one.
