#!/usr/bin/env bash
# Dedicated game server that logs in to our Blaze server. It must not share a Wine prefix with the host's client.
. "$(dirname "$0")/env.sh"; cd "$GAME" || exit 1
[ -f NFSTR_Server.exe ] || python3 "$ROOT/tools/make_server_exe.py" "$GAME" >/dev/null
export WINEPREFIX="${NFSTR_SERVER_PREFIX:-$ROOT/runtime/pfx_clean}"
# fast test mode: `tools/round.sh fast on` (race ends by timeout after 45 s, short rewards)
if [ -f /tmp/nfstr_round/fast ]; then ACT=45.0; REW=8.0; else ACT=600.0; REW=25.0; fi
exec umu-run ./NFSTR_Server.exe \
  -Server.ServerName NFSTR_BlazeServer -GamePort 25200 \
  -Game.DefaultLayerInclusion "StartupMode=Game;GameMode=CareerMode" \
  -Game.DynamicSubLevels _c4/Levels/FE/FrontEnd/NfsGarage \
  -NfsOnline.Backend Backend_Blaze \
  -NfsGame.CareerStateTimeOutTimer 86400 -NfsGame.CarSelectTimer 30.0 -NfsGame.PreLoadTimer 80.0 -NfsGame.ActivityTimer "$ACT" \
  -NfsGame.RewardsTimer "$REW" -NfsGame.IntermissionTimer 15.0 -NfsGame.VotingTimer 15.0 \
  -NfsGame.WaitForVotesTimer 2.0 -NfsGame.PostVotingTimer 11.0 \
  ${NFSTR_SERVER_EXTRA:-}
