# blaze_schema — the Blaze API as the game defines it

These scripts read the game binaries and produce
`source/internal/blaze/schema.json` (embedded in the server) and
`docs/BLAZE_API.md`. Nothing in the catalog is written by hand.

What comes from where:

| Data | Source |
|---|---|
| TDF member tables (tag, wire type, member type) | PS3 `TdfMemberInfo` tables + DWARF; PC tables after running the C++ static initializers in unicorn (`emuinit.py`) — 712 of 716 tables identical |
| NFS TDFs (Autolog, GameReporting NFS) | `visit()` code, PC first (`pcvisit.py`), PS3 as fallback |
| Command / notification names | PC `getCommandName` / `getNotificationName` switches (`pcswitch2.py`) |
| Request / response / error type of each RPC | PS3 `RpcJob<Resp,Err>` vtables at the `sendRequest` sites + full template demangling (`sites.py`, `jobsites.py`) |
| Notification payload types | PS3 `handleNotification` compare trees (`notifs.py`) |
| Error codes and names | PC `getErrorName` (`errcodes.py`, 538 codes) |

Run (Linux/WSL, ~1 minute):

```sh
pip install pyelftools capstone pefile numpy unicorn
PS3_ELF=/path/NFS.Main_Ps3_Release_unsigned.elf \
PC_EXE="/path/Need For Speed The Run.exe" sh run_all.sh
```

Neither binary is part of the repository. A from-scratch run reproduces the
committed `schema.json` byte for byte.

The server uses the catalog at runtime (`SCHEMA_CHECK=1`, default): every
reply and notification it sends is checked against the type the game will
decode it as, using the decoder's own rules (`Heat2Decoder::getHeader`):
members in ascending tag order, a known tag with the wrong wire type is a
decode error that loses the rest of the struct. Problems are logged once as
`[SCHEMA]`. RPCs the server does not implement log
`[BLAZE] UNIMPLEMENTED ... client expects T{...}`.
