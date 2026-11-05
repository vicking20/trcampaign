# Autolog, records and web services

NFS The Run uses both a Blaze game-specific Autolog component and a separate web service. The project implements both paths plus the related GameReporting/Locker storage needed by the UI.

## Blaze Autolog component

Game-specific component:

```text
0x0801 = Blaze::NFS::AutologComponent
```

The implementation covers the request families required by the current menus/Speedwalls/Recommends path. Exact TDF field layouts were recovered from the PC executable and symbol-rich PS3 build and are encoded in `source/internal/server/autolog.go` and related handlers/tests.

The code should be treated as the final layout reference; older Blaze titles should not be assumed wire-compatible merely because class names match.

## GameReporting and records

Race results arrive through GameReporting (`0x001C`), including:

- `submitGameReport` (`0x0001`)
- `submitOfflineGameReport` (`0x0002`)
- `submitTrustedEndGameReport` (`0x0065`)

Observed result data includes game type (`run_single`, `race_single`), event hash, time in milliseconds, vehicle hash and mode flags/counters.

MariaDB storage separates:

- `game_reports`: deduplicated raw/parsed report history;
- `leaderboards`: best result per board/event/persona plus metadata.

For the time-based modes currently observed, lower time is better.

`show_records.bat` prints stored personal bests in a readable time format.

## Recommends

The Autolog Recommends UI contains client-generated categories and server-provided stories.

The server builds friend stories from leaderboard relationships, for example:

- a friend is faster on an event the player has raced;
- a friend has tried an event the player has not;
- multiple friends have played an event, producing a popularity-style story.

The returned story fields and keys follow the layouts/names recovered from the game binary, including event id, poster/friend Blaze id, time and story key.

## Web Autolog

Some profile, News, friend-badge and counter data is not requested through Blaze. The game reads a `V8ServerName` client-config value and uses a JSON web service.

Current path:

```text
game HTTPS -> TCP 18443 legacy gateway
              -> local HTTP 127.0.0.1:18080 nfsrun-server
```

`Util.fetchClientConfig` supplies the configured web address unless explicitly overridden.

The server accepts the game's varying base prefix and handles routes under `/Persona/...` and `/Autolog/...`.

Representative routes:

| Route | Response |
|---|---|
| `GET /Persona/{id}` | player profile |
| `GET /Persona/{id}/getfriends` | friend collection |
| `GET /Persona/{id}/getRecommendedFriends` | friend collection |
| `GET /Persona/{id}/{id2}/getCommonFriends` | friend collection |
| `GET /Persona/{id}/getSkillLevelRule` | skill-level rule |
| `GET .../getNews` | news array |
| `GET .../getAutologNewItems` | new-item counters |
| `GET .../getRunLeaderboard` | event standings |
| `GET .../getTopRunLeaderboard` | event standings |
| `GET .../getRaceMetaDataBlobs` | race metadata |
| profile/update POST/PUT paths | persisted profile updates |

The old client does not handle modern HTTP behavior universally, so replies are intentionally conservative (including explicit `Content-Length`).

## Profiles and friends

Profile/friend JSON structures use names recovered from the PC Frostbite reflection tables. The server fills values from the local identity, presence and leaderboard stores where data is available and preserves posted profile values for later reads.

Friend status values map to the game's enum (offline/online/playing/joinable/etc.). Presence is coordinated with the launcher's live social channel.

## News

News data is server-controlled. Runtime news metadata can be stored in `data/news.json`, and banner images are served from `data/news/`.

### News per language

The game asks `/Autolog/{id}/0/0/{lang}/getNews`, where `{lang}` is its language
number:

| N | Language | N | Language |
|---:|---|---:|---|
| 0 | English (en-US) | 7 | Polish (pl-PL) |
| 1 | French (fr-FR) | 8 | Dutch (nl-NL) |
| 2 | German (de-DE) | 9 | Portuguese (pt-PT) |
| 3 | Spanish (es-ES) | 10 | Chinese (zh-CN) |
| 4 | Italian (it-IT) | 11 | Korean |
| 5 | Japanese (jp-JP) | 12 | Czech (cs-CS) |
| 6 | Russian (ru-RU) | 13 | Spanish, Mexico (es-MX) |

`data/news.json` may hold the same `NewsId` once per language (`"Language": N`).
Each player gets every `NewsId` once: the entry in their language, else the
`Language: 0` (English) entry, else the first one. `data/news.example.json` is
the example item in all 14 languages.

The game consumes fields such as subject, short message, full message, published date, category, viewed state and banner path. Viewed state is remembered per player so new-item counters can reflect unread entries.

`data/news/default.jpg` is the default banner. The game requests `"https://" + V8ServerName + NewsIcon` with no separator (`0x007CAB99`), so the server sends every `NewsIcon` with a leading `/` (`news/a.jpg` -> `/news/a.jpg`). Banners are always re-encoded to baseline RGB JPEG: the game decodes them with libjpeg only and quits on any other format. `data/news.json` is server-specific and ignored by Git.

## Locker / photo snapshots

The Blaze Locker component (`0x0014`) handles photo-mode snapshot metadata.

Typical flow:

1. `createContent` creates a pending Locker item and returns an HTTP upload URL;
2. the client uploads the sub-content to the HTTP service;
3. `confirmUpload` activates it and records size/state;
4. `ListContent`/`getContentInfo` return metadata and download URLs.

Gallery likes and views:

| RPC | request | server |
|---|---|---|
| `updateRating` (0x0C) | `CCAT`, `CID`, `RATE` | stores the player's rating (the "like" button always sends `RATE 1`; one rating per player and photo); void reply |
| `incrementUseCount` (0x0D) | `CCAT`, `CID` | counts a view (the game sends it when a photo is opened full size); void reply |

Every `ContentInfo` then carries `RCNT` (number of ratings = likes, the
number the gallery shows), `RATE` (average rating), `UCNT` (views) and
`URAT` (the asking player's own rating, 0 = not liked yet).

If photos are created (`createContent`) but no `upload` line follows in the
server log, the client could not reach the upload URL: check that TCP 18080
is open and that the host in the URL (`LOCKER_BASE_URL`, else `PUBLIC_HOST`)
resolves to the server itself, not to a proxy such as Cloudflare.

Runtime files are stored under:

```text
data/locker/<id>/
```

They are local server data and are ignored by Git.

TCP 18080 must be reachable by clients for Locker (photo) HTTP paths, unless `LOCKER_BASE_URL` points elsewhere. The launcher API (friends, live channel) is on HTTPS 8443, see [LAUNCHER_HTTPS.md](LAUNCHER_HTTPS.md).

## Research notes

The PC executable contains Frostbite web reflection records as well as Blaze TDF metadata. `tools/blaze_schema` extracts the Blaze types; see `TOOLS.md` and `BLAZE_API.md`.
