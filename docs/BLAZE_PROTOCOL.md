# Blaze protocol notes

## Service split

NFS The Run PC 1.1.0.0 uses several related services:

1. the EA Blaze core protocol (Redirector, Authentication, Util, UserSessions, GameManager, Stats, Association, Messaging, Locker and related components);
2. the game-specific `Blaze::NFS::AutologComponent` (`0x0801`);
3. a separate JSON/HTTP(S) Autolog web service;
4. local Origin/LSX communication on the player's own PC.

The emulator intentionally keeps these layers separate.

## Transport

Current public paths:

```text
Redirector:
client TCP 42127
  -> Go TCP front
  -> local legacy SSLv3 gateway 127.0.0.1:42129
  -> Go Redirector/TDF backend 127.0.0.1:42128

Blaze:
client TCP 10041
  -> Go TCP front
  -> Go Blaze/TDF backend 127.0.0.1:10042
```

The observed Blaze connection is plaintext on this PC client path. Only the Redirector and the web-Autolog compatibility front require the historical SSLv3 shim.

## Heat1 framing

The implementation uses the 12-byte big-endian Heat1 base header:

```text
u16 payload length
u16 component
u16 command
u16 error
u16 message type
u16 message id
```

Extended payload length is recognized when the Heat1 extended-length flag is set.

## TDF support

Implemented types include:

- VarInt
- String
- Blob
- Group / Struct
- List
- Map
- Union
- VarIntList
- Pair
- Triple
- Float

Tag compression and the legacy first-6-bit VarInt encoding are implemented in `source/internal/blaze` and covered by unit tests.

The preferred authority for exact class fields is the game's own PC executable. It contains `typeinfo` and `fieldinf` sections; `tools/blaze_schema` extracts them into `source/internal/blaze/schema.json` (see `BLAZE_API.md`).

## Authentication

Observed Blaze 3 command mapping:

| Component | Command | Name | Main response |
|---|---:|---|---|
| `0x0001` | `0x0028` | login | `LoginResponse` (persona list in `PLST`) |
| `0x0001` | `0x0098` | originLogin | `FullLoginResponse` |
| `0x0001` | `0x0050` | createPersona | `PersonaDetails` |
| `0x0001` | `0x005A` | getPersona | `GetPersonaResponse` |
| `0x0001` | `0x006E` | loginPersona | `SessionInfo` |
| `0x0001` | `0x0046` | logout | empty success + state clear |

Important identity domains are kept distinct:

```text
SessionInfo.BUID      -> BlazeUserId
SessionInfo.UID       -> UserId
PersonaDetails.PID    -> PersonaId
```

The local MariaDB identity layer persists those values and the session key.

### Recovered authentication field layouts

`FullLoginResponse` top-level field order recovered from PC metadata:

```text
AGUP
LDHT
NTOS
PCTK
PRIV
SESS
SPAM
THST
TSUI
TURI
```

`SessionInfo`:

```text
BUID
FRST
KEY
LLOG
MAIL
PDTL
UID
```

`PersonaDetails`:

```text
DSNM
LAST
PID
STAS
```

`CreatePersonaRequest` and `LoginPersonaRequest` use `PNAM`.

`GetPersonaResponse` contains `PINF` and `UID`; the recovered PersonaInfo block uses:

```text
DSNM
DTCR
LADT
NSNM
PID
STAS
```

Do not blindly copy fields from newer Blaze generations: familiar class names can have different layouts across titles/SDK generations.

## UserSessions (`0x7802`)

After `loginPersona`, the local player is established by server notifications rather than by another client RPC.

Observed/implemented notifications:

| Notification | TDF | Purpose |
|---:|---|---|
| `0x0001` | UserSessionExtendedDataUpdate | extended-session changes |
| `0x0002` | NotifyUserAdded | creates/adds the local user |
| `0x0003` | NotifyUserRemoved | user removal |
| `0x0004` | UserSessionDisconnectReason | disconnect information |
| `0x0005` | UserStatus | user/presence update |

The login path queues `NotifyUserAdded` followed by `UserStatus`.

Current `NotifyUserAdded` shape:

```text
DATA {                        UserSessionExtendedData
  ADDR  union unset
  BPS   string ""
  CTY   string ""
  DMAP  map<int,int> {}
  HWFG  int 0
  PSLM  list<int> []
  QDAT  { DBPS 0, NATT 0, UBPS 0 }
  UATT  int 0
}
USER {                        UserIdentification
  AID   account id
  ALOC  0x656E5553            # enUS
  EXBB  empty blob
  EXID  0
  ID    BlazeId
  NAME  persona
}
```

`UserStatus` currently uses:

```text
FLGS = 3
ID   = BlazeId
```

## Util (`0x0009`)

Important calls handled by the server:

| Command | Handling |
|---|---|
| `0x0001` fetchClientConfig | config map sourced from `client_config/` plus dynamic keys |
| `0x0002` ping | server time (`STIM`) |
| `0x0007` preAuth | Blaze 3-style component/config/server information |
| `0x0008` postAuth | PSS, telemetry, ticker and user-options groups |

`fetchClientConfig` dynamically supplies the configured web-Autolog address when needed.

## GameReporting (`0x001C`)

Implemented report commands include:

- `0x0001` `submitGameReport`
- `0x0002` `submitOfflineGameReport`
- `0x0065` `submitTrustedEndGameReport`

Reports are deduplicated/stored and useful race results are ranked into the local leaderboards database. See `AUTOLOG.md`.

## Component behavior

The implementation includes handlers for the subsystems needed by the current client path, including Authentication, Util, UserSessions, GameManager, Playgroups, Association/friends, Stats/leaderboards, GameReporting, Locker and NFS Autolog.

Unknown calls are named/logged when possible. Some non-critical calls intentionally receive a minimal/empty success reply so research can continue without disconnecting the client.

## Research method

Useful authorities, in order of preference:

1. the supported PC executable's own type/field metadata and code;
2. the symbol-rich PS3 build for class/function names and semantic context;
3. live request/response/notification traces;
4. other Blaze implementations as secondary semantic references only.

See `TOOLS.md` (`blaze_schema`) for the extraction pipeline.
