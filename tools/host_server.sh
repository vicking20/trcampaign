#!/usr/bin/env bash
# Start the whole server side on this machine (Linux + Proton). Usage:
#   tools/host_server.sh [auto|address] [modes|vanilla] [races-per-mode] [hold|soft]      (auto = each player gets the address it connected to)
#   tools/host_server.sh <ip> plague,bounty,streak,cannonball 3
#   tools/host_server.sh <ip> vanilla            # no custom modes, no scoring, plain News
# <address> = the LAN IP / public IP / domain players put in their config.
# Env: NFSTR_MAX_PLAYERS=16 raises the race player cap (default 8 in the game data; experimental).
# 4th argument: what happens to a player an elimination mode knocks out - hold (default: removed, cannot match until the
# season changes) or soft (may rejoin; their results just do not score).  Stop everything: tools/host_server.sh stop
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
if [ "$1" = stop ]; then systemctl --user stop nfstr-menu-score nfstr-menu-blaze nfstr-menu-bsrv 2>&1 | grep -v "not loaded"; echo stopped; exit 0; fi
ADDR="${1:-auto}"; MODES="${2:-plague}"; EVERY="${3:-3}"; ELIM="${4:-hold}"
export NFSTR_ADV_HOST="$ADDR"
if [ ! -f "$ROOT/server/key.pem" ] || [ ! -f "$ROOT/server/cert.pem" ]; then       # fake-service key + certificate (not a secret; the game's check is patched)
  openssl genrsa -traditional -out "$ROOT/server/key.pem" 1024 2>/dev/null && python3 "$ROOT/server/mkcert.py" >/dev/null && echo "generated server/key.pem and cert.pem"
fi
"$ROOT/tools/net_setup.sh" >/dev/null && echo "443 -> 8081 redirects ok"
systemctl --user stop nfstr-menu-blaze nfstr-menu-bsrv nfstr-menu-score 2>/dev/null
if [ -n "$NFSTR_MAX_PLAYERS" ]; then        # raise the dedicated server's race player cap (plugin [Patch] MaxPlayers, see README)
  python3 - "${NFSTR_GAME_DIR:-$ROOT/online_test}/Scripts/NFSTR_Online.ini" "$NFSTR_MAX_PLAYERS" <<'PY'
import re, sys
p, n = sys.argv[1], sys.argv[2]; s = open(p).read()
s = re.sub(r"\n?\[Patch\][^\[]*", "", s).rstrip() + "\n\n[Patch]\nMaxPlayers=%s\n" % n
open(p, "w").write(s)
PY
  echo "max players patch: $NFSTR_MAX_PLAYERS"
fi
mkdir -p "$ROOT/recon/runs"; echo '[]' > "$ROOT/recon/runs/eliminated.json"
"$ROOT/tools/round.sh" restart blaze >/dev/null; sleep 2
"$ROOT/tools/round.sh" restart server >/dev/null
if [ "$MODES" = vanilla ]; then
  python3 "$ROOT/tools/gen_news.py" --vanilla >/dev/null; echo "vanilla: no custom modes, scoring off"
else
  "$ROOT/tools/round.sh" score "$MODES" --every "$EVERY" --elim "$ELIM" >/dev/null
fi
sleep 5
echo "server up: advertising $ADDR, modes $MODES (every $EVERY races, elimination: $ELIM)"; "$ROOT/tools/round.sh" status
echo "ports to open/forward: TCP 42127, TCP 443, TCP+UDP 17502, UDP 25200"
