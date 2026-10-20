# Lobby + relay on a public server (e.g. the Oracle box)

What runs there (Python 3 only, any CPU architecture - the Windows game server is NOT run there, it stays on the host's PC):
- `relay.py`: UDP relay. Control port **26000/udp**, one public port per game in **26001-26100/udp**.
- `lobby_server.py`: game list and synchronized launch. **26100/tcp** (no conflict: different protocol from the UDP room port 26100).

Deploy: `./deploy.sh ubuntu@SERVER_IP ~/.ssh/KEY` (asks before installing; check `uname -m` it prints).

## Open the ports (needed twice on Oracle Cloud)
1. Oracle console -> Networking -> VCN -> Security List: ingress rules `UDP 26000-26100` and `TCP 26100` from 0.0.0.0/0.
2. On the instance (Ubuntu images ship restrictive iptables):
```bash
sudo iptables -I INPUT -p udp --dport 26000:26100 -j ACCEPT
sudo iptables -I INPUT -p tcp --dport 26100 -j ACCEPT
sudo netfilter-persistent save      # apt install iptables-persistent if missing
```

## Using it
Host:  `nfstr_launcher.py host --relay SERVER_IP:26000 --room MYROOM --secret SOMESECRET --lobby http://SERVER_IP:26100 --mode cannonball ...`
Players: GUI -> Lobby address `http://SERVER_IP:26100` -> Refresh -> Join. The relay room locks itself when the race starts (no late joiners).
Checks: `curl http://SERVER_IP:26100/api/games` ; `systemctl status nfstr-relay nfstr-lobby` ; `journalctl -u nfstr-relay -f`.

## Season ladder
The lobby keeps a monthly ladder (gold/silver/bronze per finished game, points) in `/var/lib/nfstr/lobby_data.json` (survives restarts; back this file up if you care).
Read it with `nfstr_launcher.py lobby ladder http://SERVER_IP:26100 [--season YYYY-MM|all]` or the GUI button. Only games with 2+ players that finished a race count,
and only nicknames that signed up for that game. There are no accounts, so a nickname is not protected: anyone can use anyone's name.

## Safety notes
Services run as an unprivileged user with a read-only filesystem. The relay never inspects game packets and rate-limits each player (600 pkt/s).
The lobby is open (anyone can list a game; limits: 50 games, 64 players, 4 KB bodies) - no accounts, no personal data besides nicknames.
Rooms are protected by the host's secret only; pick a long random one. Both services keep state in memory only.
