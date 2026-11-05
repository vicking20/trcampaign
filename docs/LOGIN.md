# Launcher login

Players are identified by a key their launcher keeps, not by their name.

## Model

- `PLAYER_NAME` is a display name, not a credential.
- The launcher creates a 32-byte Ed25519 private key on first start and keeps
  it in `nfsrun-launcher.key`, next to the launcher. It never leaves the PC.
- The server stores only the public key.
- The launcher signs a one-use challenge over HTTPS
  ([LAUNCHER_HTTPS.md](LAUNCHER_HTTPS.md)) and gets a session ticket
  (`AUTH_SESSION_HOURS`, renewed before it expires). Friends, presence,
  invites and the live channel use that ticket.
- The game never sees the ticket: for every login it gets a one-time game
  code (`nfsrun-g1.`, 5 minutes, single use), and Blaze answers with its own
  session key.
- A name alone is never accepted. EA app / Origin machine names that reach the
  server are matched to the player's launcher, never turned into new players.

## Invites

`AUTH_MODE=invite` (the default): a new player needs a one-time code. In the
server window (`C`):

```text
invite RacerOne
```

(or `nfsrun-server.exe --invite RacerOne`). The code is valid 12 hours
(`invite RacerOne 48` for 48; 1-168). The player sets `PLAYER_NAME=RacerOne`
and `INVITE_CODE=<code>` in `nfsrun-launcher.ini`; the launcher clears the
code after the first login. From then on the player's key owns the name; the
hours only limit how long an unused code stays valid.

`AUTH_MODE=open`: the first launcher that uses a free name owns it. A name
with a pending code still waits for that code.

`invites` lists pending codes (only their hashes are stored);
`invites cancel <name>` withdraws one.

## Lost key

The server has only the public key, so a lost key cannot be sent back. In the
server window:

```text
reset-key RacerOne
```

After `Y`, this disconnects the player, revokes their key, sessions and
remembered EA aliases, and prints a new code (12 hours). Records, friends,
photos and the BlazeId stay. The player deletes `nfsrun-launcher.key`, sets
`INVITE_CODE=<code>` and starts the launcher: the new key takes over the same
player. The name waits for that code; if it expires, run `reset-key` again.

## What the transport protects

The key never travels; challenges cannot be replayed; the session ticket
only travels over the launcher's HTTPS (pinned self-signed certificate, or a
CA certificate such as Cloudflare's). The game's Blaze connection is
plaintext because the 2011 client cannot do better: whoever can watch it sees
an already used game code and that connection's Blaze session key, enough to
disturb that one game session, but not the ticket or the key.

Checks:

- the launcher log shows `server certificate pinned` (direct setup);
- `curl http://<server>:18080/nfsrun/auth/challenge` answers `403 https_required`;
- the server log shows `[AUTH] game code issued` and logins via `game code`.

## Tests worth running with two players

- Copying only `PLAYER_NAME=RacerOne` to another PC does not log in as RacerOne.
- Changing `PLAYER_NAME` with the same key keeps the same player.
- A second key cannot take a name that has an owner.
- Two players behind one public IP both log in; when an EA/Origin login
  cannot be matched to exactly one active launcher on that IP, the server
  refuses instead of guessing.
