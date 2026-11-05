# Tools

Scripts in `tools\`. The Python tools need Python 3 and only the standard
library (except `blaze_schema`).

## `patch_client.py` / `patch_client.bat` (optional)

Not needed by players: the launcher patches its own copy of the game. Use it
only to run the original exe against your server (launcher setting
`NO_ORIGIN_START=0`, or the game without the launcher).

Replaces the four Redirector host names in the PC 1.1.0.0 exe, in place
(backup `<exe>.bak`). The exe's SHA-256 is checked first, so run it before
`patch_origin_startup`.

```text
python patch_client.py [exe] [--domain D] [--output FILE] [--allow-unknown-hash]
patch_client.bat "<exe>" [domain]
```

| Argument | Default | Description |
|---|---|---|
| `exe` | `Need For Speed The Run.exe` | Game exe to patch. |
| `--domain D` | asked | Base domain; the hosts become `nfs.D`, `scert-nfs.D`, `stest-nfs.D`, `redirector.D`. At most 16 characters. |
| `--output FILE` | the input file | Write the patched exe elsewhere. |
| `--allow-unknown-hash` | off | Patch an exe whose SHA-256 is not the known 1.1.0.0 one. |

## `patch_origin_startup.py` / `patch_origin_startup.bat` (optional)

Same case as above. Lets the game's Origin SDK start, so it connects to the
launcher (5 bytes at `0x018AEFBC`). Original bytes are verified; backup
`<exe>.before_origin_patch.bak`.

```text
python patch_origin_startup.py [--check | --undo] "<exe>"
patch_origin_startup.bat "<exe>"
```

| Option | Description |
|---|---|
| (none) | Apply the patch. |
| `--check` | Report the state, change nothing. |
| `--undo` | Restore the original bytes. |

## `open_ds_firewall.bat`

Opens the Windows firewall for race traffic. Run on the server as
administrator.

```text
open_ds_firewall.bat                         relay ports 6100-6163
open_ds_firewall.bat <first> <count>         relay ports first..first+count-1
open_ds_firewall.bat direct [first] [count]  race-server ports (RELAY_ENABLED=false)
```

| Mode | Default range | Matches |
|---|---|---|
| relay | `6100` + `64` ports | `RELAY_PORT_BASE`, `RELAY_PORT_COUNT` |
| direct | `25200` + `8` ports, the same +100, `6000`, `9999` | `DS_PORT_BASE`, `DS_COUNT`, `DS_LISTEN_OFFSET` |

## `blaze_schema/`

Rebuilds `source/internal/blaze/schema.json` and `docs/BLAZE_API.md` from
the game binaries (PS3 build with symbols and the PC exe). Not needed to run
the server. See [tools/blaze_schema/README.md](../tools/blaze_schema/README.md).

```sh
pip install pyelftools capstone pefile numpy unicorn
PS3_ELF=/path/NFS.Main_Ps3_Release_unsigned.elf \
PC_EXE="/path/Need For Speed The Run.exe" sh tools/blaze_schema/run_all.sh
```

| Variable | Description |
|---|---|
| `PS3_ELF` | PS3 build with DWARF symbols. |
| `PC_EXE` | Retail PC 1.1.0.0 exe. |
| `OUT` | Work folder (default `tools/blaze_schema/out`). |
