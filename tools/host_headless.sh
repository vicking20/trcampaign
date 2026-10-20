#!/usr/bin/env bash
# Headless server for a container/VPS (Debian/Ubuntu/Arch, plain Wine + Xvfb, no systemd units, no Proton needed).
#   tools/host_headless.sh start <address> [modes|vanilla] [races-per-mode] [hold|soft]
#   tools/host_headless.sh stop | status
# Needs: wine (32-bit capable), xvfb, python3. Game folder: ./online_test (or NFSTR_GAME_DIR) with NFSTR_Server.exe, dinput8.dll and
# Scripts/NFSTR_Online.asi + .ini (Host=127.0.0.1). Run as root in a container (it can bind port 443), or set TR_AUTOLOG_PORT=8081 and forward 443 yourself.
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
GAME="${NFSTR_GAME_DIR:-$ROOT/online_test}"
RUN="$ROOT/recon/runs"; mkdir -p "$RUN"; PIDS="$RUN/headless.pids"
DISP="${NFSTR_DISPLAY:-:99}"
stop() {
  [ -f "$PIDS" ] && while read -r p; do kill "$p" 2>/dev/null; done < "$PIDS"
  rm -f "$PIDS"; wineserver -k 2>/dev/null; echo stopped
}
case "$1" in
  stop) WINEPREFIX="${WINEPREFIX:-$ROOT/runtime/wine_server}" stop; exit 0 ;;
  status) [ -f "$PIDS" ] && ps -o pid,etime,comm -p "$(paste -sd, "$PIDS")" || echo "not running"; exit 0 ;;
  start) shift ;;
  *) sed -n 2,8p "$0"; exit 1 ;;
esac
ADDR="${1:-auto}"; MODES="${2:-plague}"; EVERY="${3:-3}"; ELIM="${4:-hold}"
[ -f "$GAME/NFSTR_Server.exe" ] || python3 "$ROOT/tools/make_server_exe.py" "$GAME" >/dev/null 2>&1
[ -f "$GAME/NFSTR_Server.exe" ] || { echo "no NFSTR_Server.exe in $GAME (prepare it with tools/prepare_host.sh <game-folder>, or set NFSTR_GAME_DIR)"; exit 1; }
command -v wine >/dev/null && command -v Xvfb >/dev/null || { echo "install wine and xvfb first"; exit 1; }
export WINEPREFIX="${WINEPREFIX:-$ROOT/runtime/wine_server}" WINEDLLOVERRIDES="dinput8=n,b;mscoree,mshtml=" WINEDEBUG=-all
mkdir -p "$WINEPREFIX"
stop >/dev/null 2>&1; : > "$PIDS"
Xvfb "$DISP" -screen 0 800x600x24 >/dev/null 2>&1 & echo $! >> "$PIDS"; sleep 1
export TR_AUTOLOG_PORT="${TR_AUTOLOG_PORT:-443}"
python3 -u "$ROOT/server/trserver.py" 42127 "$ADDR" >> "$RUN/blaze.log" 2>&1 & echo $! >> "$PIDS"; sleep 2
SERVER_CMD=(wine NFSTR_Server.exe -Server.ServerName NFSTR_BlazeServer -GamePort 25200
  -Game.DefaultLayerInclusion "StartupMode=Game;GameMode=CareerMode" -Game.DynamicSubLevels _c4/Levels/FE/FrontEnd/NfsGarage
  -NfsOnline.Backend Backend_Blaze -NfsGame.CareerStateTimeOutTimer 86400 -NfsGame.CarSelectTimer 30.0 -NfsGame.PreLoadTimer 80.0
  -NfsGame.ActivityTimer 600.0 -NfsGame.RewardsTimer 25.0 -NfsGame.IntermissionTimer 15.0 -NfsGame.VotingTimer 15.0
  -NfsGame.WaitForVotesTimer 2.0 -NfsGame.PostVotingTimer 11.0)
export DISPLAY="$DISP"; cd "$GAME" || exit 1
if [ "$MODES" = vanilla ]; then
  python3 "$ROOT/tools/gen_news.py" --vanilla >/dev/null
  ( while true; do "${SERVER_CMD[@]}" >> "$RUN/dedicated.log" 2>&1; sleep 3; done ) & echo $! >> "$PIDS"      # the engine exits after each session: start it again
else      # the scoring daemon starts the dedicated server as its child: only a parent may read its memory when kernel.yama.ptrace_scope=1
  python3 -u "$ROOT/tools/online_scoring.py" "$MODES" --every "$EVERY" --elim "$ELIM" --run "${SERVER_CMD[@]}" >> "$RUN/scoring.log" 2>&1 & echo $! >> "$PIDS"
fi
echo "server started: advertising $ADDR, modes $MODES. Logs: $RUN/*.log. Ports: TCP 42127, TCP $TR_AUTOLOG_PORT, TCP+UDP 17502, UDP 25200"
