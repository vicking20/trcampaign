# Security

This is hobby software for small private servers. It emulates 2011 game
networking and contains compatibility code for obsolete protocols on
purpose. It has not been reviewed or hardened for large public services.

## What is protected

| Data | How |
|---|---|
| `nfsrun-launcher.key` (player's login key) | never leaves the player's PC; the server stores only the public key |
| launcher session ticket | sent only over HTTPS (pinned self-signed certificate, or a CA certificate) |
| game login | one-time game codes; Blaze gets its own session key |
| new players | `AUTH_MODE=invite` (default): a name needs a one-time invite code |
| passwords, e-mail | not used: there are no accounts |

## What is not

The game's own connections cannot be secured, because the client only speaks
these protocols:

- Blaze (TCP 10041) is plaintext;
- the Redirector and web Autolog use SSLv3, which is broken;
- photos (TCP 18080) and race traffic (UDP) are unencrypted.

Someone who can watch a player's network traffic can read game data and
disturb that game session. Logs and the database hold player names and IP
addresses.

## Running a server

- Expose only the ports listed in the README; keep the backends on 127.0.0.1.
- Never publish `config\`, `certs\`, `mariadb\data\`, `logs\`, patched game
  executables or a player's `nfsrun-launcher.key`. They contain passwords,
  keys, addresses or player data.
- If a secret is committed by accident, removing the file is not enough:
  replace the key or password and clean the Git history.
- Do not reuse the SSLv3 gateway for anything else.

## Reporting a problem

The repository is archived and not maintained. If private vulnerability
reporting is enabled on it, use that; do not post credentials or exploit
details in a public issue.
