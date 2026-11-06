# Admin page and commands

Set `ADMIN_WEB_LISTEN`, `ADMIN_WEB_USER` and `ADMIN_WEB_PASSWORD` in `config/server.env` ([CONFIGURATION.md](CONFIGURATION.md)), then open
`http://<server address>:8090` from any device on the network (a phone works). Sign-ins last 30 days. Wrong passwords are rate limited.
Do not expose the port to the internet without a tunnel or firewall rules you trust.

| Tab | What it does |
|---|---|
| Players | who is online, kick, ban and unban |
| Race | options (join a running race, remember logins, number of race servers), traffic, forced cars, car names, race modes |
| News | news items in the game's news feed; in-game messages with repeat |
| Servers | running race servers and Stop |
| Accounts | create players with generated passwords, reset passwords |
| Console | any command below |

## Commands

The Console tab and the server console take the same commands (`help` lists them).

```text
players | players all | games
kick <name>        ban <name> | unban <name> | bans
adduser <name>     passwd <name>
stop <n>           stop race server n
traffic [off | <density 0-3> [<cars 0-50>]]
cars [force <hash,...|off> | name <hash> <label>]
announce "<title>" "<text>" | news | news delete <id>
say [-n <times>] [-every <seconds>] <text> | say stop
modes | mode <name> | mode save <name> | mode delete <name>
latejoin [on|off]  logins [on|off]  servers [n]      options (also on the Race tab)
score | score cannonball | score off | score reset
```

Traffic, forced cars and the mode are written to the game's `Scripts/NFSTR_Online.ini` (`[Patch]`) before each race server starts, so they
apply from the next race server start. A new player's account is made with `adduser`; send them the generated password privately.

## Race modes

Built in: Normal, Rush hour, Gridlock, Calm cruise, Empty roads, Stock cars only, NFS Edition cars, Classics.
A mode is a traffic setting plus an optional car set. `mode save <name>` keeps the current settings as a custom mode (`config/modes.json`).
The car table (`data/cars.json`, 322 cars, hash to name) was read from a race server's memory; `cars` lists cars players used.

## In-game messages

`say` shows a text of up to 32 characters to everyone in a game. The game words it as "<text> HAS LEFT"; that wording is fixed in the game.
