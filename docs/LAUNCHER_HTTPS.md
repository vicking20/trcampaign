# Launcher HTTPS

The launcher talks to the server over HTTPS (`LAUNCHER_HTTPS_LISTEN`, default
TCP 8443): login, session renewal, friends, presence, invites and the live
channel. The plain HTTP port (18080) keeps serving only what the game itself
uses (web Autolog backend, photos) and answers `403 https_required` to the
launcher API.

Two setups are supported.

## Direct (default)

Players connect straight to the server. No domain certificate or CA is needed.

1. On first start the server creates a self-signed certificate,
   `certs\launcher.crt` + `certs\launcher.key` (ECDSA P-256, 10 years).
2. The launcher in the player package has the server address and the
   certificate's public-key fingerprint built in (see
   [Launcher preset](#launcher-preset)), for example:

   ```ini
   SERVER_HTTP=https://redirector.example.org:8443
   SERVER_CERT_PIN=<64 hex characters>
   ```

3. The launcher accepts only a certificate with that key, so a machine in the
   middle cannot read or replace the traffic.

`server.env`:

```ini
LAUNCHER_HTTPS_LISTEN=:8443
LAUNCHER_TLS_CERT=certs/launcher.crt
LAUNCHER_TLS_KEY=certs/launcher.key
LAUNCHER_URL=
TRUSTED_PROXIES=
```

Open TCP 8443. `nfsrun-server.exe --stamp-launcher <out.exe>` prints the
values it builds in.

Keep `certs\launcher.key`. A new key changes the pin, and every player needs
a new package (`package` in the server window) or `SERVER_CERT_PIN=<new pin>`
in their `nfsrun-launcher.ini`. `reset_setup.bat` keeps it.

A certificate from a public CA (for example Let's Encrypt) works too: put it
in `LAUNCHER_TLS_CERT` / `LAUNCHER_TLS_KEY`. Player packages then get no pin
and the launcher checks the certificate normally.

## Behind Cloudflare

Only the launcher host goes through Cloudflare. The game's own connections
(Redirector, Blaze, web Autolog, race traffic) are not HTTP and must reach
the server directly.

DNS:

| Name | Proxy status |
|---|---|
| launcher host, e.g. `play.example.org` | Proxied (orange cloud) |
| `nfs.`, `scert-nfs.`, `stest-nfs.`, `redirector.<domain>` | DNS only (grey cloud) |

Cloudflare SSL/TLS mode: **Full**. With **Full (strict)**, replace
`certs\launcher.crt` / `.key` with a Cloudflare Origin Certificate for the
launcher host.

The port must be one Cloudflare proxies for HTTPS: 443, 2053, 2083, 2087,
2096 or 8443. The default 8443 works.

`server.env`:

```ini
LAUNCHER_HTTPS_LISTEN=:8443
LAUNCHER_URL=https://play.example.org:8443
TRUSTED_PROXIES=cloudflare
```

`TRUSTED_PROXIES=cloudflare` makes the server take the player's address from
`CF-Connecting-IP` when the connection comes from a Cloudflare range;
without it every launcher would appear with a Cloudflare address (and share
the per-IP limits). Headers from other addresses are ignored.

Built into the package's launcher:

```ini
SERVER_HTTP=https://play.example.org:8443
SERVER_CERT_PIN=
```

The pin stays empty: players see Cloudflare's certificate, which the launcher
checks against the Windows certificate store.

Optionally allow TCP 8443 only from Cloudflare's ranges in the firewall.

## Launcher preset

Whoever hosts the server also hands out the launcher, so the launcher carries
the server address, pin and domain itself; players only keep their own
`nfsrun-launcher.key`.

- `scripts\make_client_package.ps1` (run by first-run setup and by `package`
  in the server window) writes `dist\NFSTR_Client.zip` with a launcher that
  has this server built in, through

  ```bat
  nfsrun-server.exe --stamp-launcher dist\nfsrun-launcher.exe
  ```

  It copies `launcher\nfsrun-launcher.exe` (`--launcher-exe` picks another)
  and rewrites a fixed-size block inside the copy: no Go toolchain needed,
  the exe size does not change. The original launcher stays generic.
- Building from source can set the same values instead:

  ```bat
  go build -ldflags "-H windowsgui -X main.bakedServerHTTP=https://play.example.org:8443 -X main.bakedServerPin=<hex> -X main.bakedDomain=example.org" .\cmd\nfsrun-launcher
  ```

- The package ships **no** `nfsrun-launcher.ini` or key file. The launcher
  writes a minimal `.ini` on first start, without server settings. Copying a
  newer package over the game folder therefore never replaces a player's key.
- Advanced lines added to `nfsrun-launcher.ini` override the built-in values:

  | `.ini` | result |
  |---|---|
  | none | built-in address, pin and domain |
  | `SERVER_CERT_PIN=<hex>` | built-in address, this pin (server got a new key) |
  | `SERVER_HTTP=<other>` only | other server, no pin, no domain (CA certificate, original exe) |
  | `SERVER_HTTP=<other>` + `SERVER_CERT_PIN=<hex>` + `BASE_DOMAIN=<d>` | other server, this pin and domain |
  | `SERVER_CERT_PIN=off` | built-in address, no pin |
  | `SERVER_HTTP=off` | offline |

The launcher log says which one it used:
`[ID] server https://... from built into this launcher`.

## What HTTPS covers

| Traffic | Protection |
|---|---|
| launcher <-> server (login, tickets, friends, invites) | HTTPS |
| player key (`nfsrun-launcher.key`) | never sent; only signatures |
| game <-> Blaze (10041) | none: the 2011 client speaks plain Blaze |
| game <-> Redirector, web Autolog | SSLv3 (obsolete, breakable) |
| photos (18080) | none |
| race traffic (UDP) | none |

The game logs in to Blaze with a one-time code (`nfsrun-g1.`, valid 5 minutes,
single use) that the launcher fetches over HTTPS for each request; the
launcher's session ticket never goes over the game's connections. Blaze then
gets its own session key. Someone who can watch the game's traffic can still
follow or take over that one game session; they cannot get the launcher
ticket or the player's key.

## Plain HTTP (tests only)

```ini
LAUNCHER_HTTPS_LISTEN=off
```

The launcher API then moves to `HTTP_LISTEN`.

Launcher (`.ini`, or built in by `--stamp-launcher`, which then prints the
`http://` address):

```ini
SERVER_HTTP=http://127.0.0.1:18080
```

Server and launcher log a warning: the session ticket travels unencrypted.
