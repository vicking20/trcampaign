#!/usr/bin/env bash
# One-command test rounds for the original-menu research. Output is deliberately short.
#   tools/round.sh restart [blaze|server|game|all]   restart pieces (default all), mark the round start
#   tools/round.sh report                            what happened since the last restart (compact)
#   tools/round.sh status                            which units run
set -u
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
WORK=/tmp/nfstr_round; mkdir -p "$WORK/plog"
U_BLAZE=nfstr-menu-blaze; U_SRV=nfstr-menu-bsrv; U_GAME=nfstr-menu-game
ENVS=(-E "DISPLAY=${DISPLAY:-:0}" -E "XAUTHORITY=${XAUTHORITY:-}")

start_blaze() { systemctl --user restart $U_BLAZE 2>/dev/null || systemd-run --user --collect --property=NoNewPrivileges=yes --unit=$U_BLAZE python3 -u "$ROOT/server/trserver.py" 42127 "${NFSTR_ADV_HOST:-127.0.0.1}" >/dev/null; }
start_server() { systemctl --user stop $U_SRV 2>/dev/null; sleep 1; mkdir -p "$WORK/slog"; rm -f "$WORK"/slog/*
  systemd-run --user --collect --property=Restart=always --property=RestartSec=3 --unit=$U_SRV "${ENVS[@]}" -E PROTON_LOG=1 -E "PROTON_LOG_DIR=$WORK/slog" -E "NFSTR_SERVER_EXTRA=${NFSTR_SERVER_EXTRA:-}" "$ROOT/tools/launch/start_server_blaze.sh" >/dev/null; }
start_game() { systemctl --user stop $U_GAME 2>/dev/null; sleep 2; rm -f "$WORK"/plog/*; : > "$ROOT/online_test/Scripts/NFSTR_Online.log"
  systemd-run --user --collect --unit=$U_GAME "${ENVS[@]}" -E PROTON_LOG=1 -E "PROTON_LOG_DIR=$WORK/plog" "$ROOT/tools/launch/start.sh" >/dev/null; }

start_scoring() { systemctl --user stop nfstr-menu-score 2>/dev/null; systemd-run --user --collect --unit=nfstr-menu-score python3 -u "$ROOT/tools/online_scoring.py" "${@:-plague}" >/dev/null && echo "scoring daemon: ${*:-plague} (log: journalctl --user -u nfstr-menu-score)"; }

case "${1:-report}" in
  score) start_scoring "${@:2}" ;;
  restart)
    what="${2:-all}"
    date '+%F %T' > "$WORK/start"
    [[ $what == all || $what == blaze ]] && start_blaze && sleep 1
    [[ $what == all || $what == server ]] && start_server
    [[ $what == all || $what == game ]] && start_game
    echo "restarted ($what) at $(cat "$WORK/start"). Now: sign in, Quick Match, wait ~20 s, then run: tools/round.sh report" ;;
  push) echo "${2:-off}" > "$WORK/push_mode"; echo "push mode: $(cat "$WORK/push_mode") (off|a|b|full)" ;;
  inject) echo "${*:2}" >> "$WORK/inject.txt"; echo "queued: ${*:2}" ;;
  sstate) python3 "$ROOT/tools/server_state.py" ;;
  fast) if [ "${2:-on}" = on ]; then touch "$WORK/fast"; echo "fast mode ON: races end by timeout after 45 s (applies at the next server start)"; else rm -f "$WORK/fast"; echo "fast mode off"; fi ;;
  quiet) if [ "${2:-on}" = on ]; then touch "$WORK/quiet"; echo "quiet ON (next-stage setup not sent to players; live)"; else rm -f "$WORK/quiet"; echo "quiet off"; fi ;;
  status)
    systemctl --user list-units 'nfstr-*' --no-legend | awk '{print $1, $3}' ;;
  report)
    since="$(cat "$WORK/start" 2>/dev/null || date -d '-5 min' '+%F %T')"
    echo "== units"; systemctl --user list-units 'nfstr-*' --no-legend | awk '{printf "%s=%s ", $1, $3}'; echo
    echo "== blaze since $since (REQ/NOTIFY/errors, noise removed)"
    journalctl --user -u $U_BLAZE --since "$since" --no-pager -o cat 2>/dev/null \
      | grep -a "REQ\|NOTIFY\|error\|no handler\|new game\|closed\|connection from\|stored" \
      | grep -av "QOS\|Util.0x2 \|Util.0x1 \|Autolog\|Messaging" | sed -E 's/^\[[0-9:]*\] *//; s/ id=[0-9]+/ id=N/' | cut -c1-150 \
      | awk '{if(!($0 in c))o[++n]=$0; c[$0]++} END{for(i=1;i<=n;i++) printf "%s%s\n", o[i], (c[o[i]]>1 ? "  (x" c[o[i]] ")" : "")}' | tail -30
    echo "== server crash"
    SL="$WORK/slog/steam-0.log"
    if [[ -f $SL ]] && grep -aq "Unhandled exception" "$SL"; then
      grep -a -A8 "Unhandled exception" "$SL" | grep -a "exception\|EIP\|EAX\|ESI\|EDI" | head -4 | cut -c1-150
    else echo "none"; fi
    echo "== client crash"
    L="$WORK/plog/steam-0.log"
    if [[ -f $L ]] && grep -aq "Unhandled exception" "$L"; then
      grep -a -A8 "Unhandled exception" "$L" | grep -a "exception\|EIP\|EAX\|ESI\|EDI" | head -4 | cut -c1-150
    else echo "none"; fi
    echo "== plugin log (GAMELOG/NETPLAYER)"
    grep -a "GAMELOG\|NETPLAYER" "$ROOT/online_test/Scripts/NFSTR_Online.log" 2>/dev/null | tail -6 | cut -c1-160 ;;
  *) echo "usage: $0 score <mode> | restart [blaze|server|game|all] | report | status | push off|a|b|full | fast on|off"; exit 1 ;;
esac
