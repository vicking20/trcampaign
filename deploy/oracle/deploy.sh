#!/usr/bin/env bash
# Deploys the lobby + relay to a Linux server (Ubuntu; Python 3 only, no other dependencies).
#   ./deploy.sh user@host [path/to/ssh_key]
# It copies 2 python files + 2 systemd units, creates an unprivileged user `nfstr` and starts both services.
# It does NOT touch firewalls: open them yourself (see README.md) - on Oracle Cloud also in the VCN security list.
set -euo pipefail
TARGET="${1:?usage: deploy.sh user@host [ssh_key]}"
KEY="${2:-}"
HERE="$(cd "$(dirname "$0")" && pwd)"; ROOT="$HERE/../.."
SSH=(ssh -o StrictHostKeyChecking=accept-new ${KEY:+-i "$KEY"}); SCP=(scp -q ${KEY:+-i "$KEY"})
echo "== target: $TARGET  (arch: $("${SSH[@]}" "$TARGET" uname -m))"
read -r -p "Install nfstr-relay + nfstr-lobby on $TARGET? [y/N] " yn; [ "$yn" = y ] || exit 1
"${SCP[@]}" "$ROOT/relay/relay.py" "$ROOT/lobby/lobby_server.py" "$HERE/nfstr-relay.service" "$HERE/nfstr-lobby.service" "$TARGET:/tmp/"
"${SSH[@]}" "$TARGET" 'set -e
  sudo useradd --system --no-create-home --shell /usr/sbin/nologin nfstr 2>/dev/null || true
  sudo install -d -o root -g root -m 755 /opt/nfstr
  sudo install -m 644 /tmp/relay.py /tmp/lobby_server.py /opt/nfstr/
  sudo install -m 644 /tmp/nfstr-relay.service /tmp/nfstr-lobby.service /etc/systemd/system/
  sudo systemctl daemon-reload
  sudo systemctl enable --now nfstr-relay nfstr-lobby
  sleep 1; systemctl is-active nfstr-relay nfstr-lobby'
echo "done. Lobby: http://<server-ip>:26100/api/games   Relay control: <server-ip>:26000 (UDP)"
