#!/usr/bin/env bash
# Install the server as systemd USER services so it starts at boot and restarts on failure (Linux).
#   tools-linux/install_services.sh            install and start nfstr-emulator
#   tools-linux/install_services.sh --print    only show the unit files
#   tools-linux/install_services.sh --remove   stop and remove them
# Needs: a built ./nfsrun-server, config/server.env. "loginctl enable-linger $USER" lets them run without a login session.
set -e
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DIR="$HOME/.config/systemd/user"
emulator="[Unit]
Description=NFSTR server
After=network-online.target
[Service]
WorkingDirectory=$ROOT
ExecStart=$ROOT/nfsrun-server --config config/server.env
Restart=on-failure
RestartSec=3
[Install]
WantedBy=default.target"
case "$1" in
  --print) printf '%s\n' "$emulator" ;;
  --remove) systemctl --user disable --now nfstr-emulator nfstr-gateway 2>/dev/null || true
            rm -f "$DIR/nfstr-emulator.service" "$DIR/nfstr-gateway.service"; systemctl --user daemon-reload ;;
  *) [ -x "$ROOT/nfsrun-server" ] || { echo "build nfsrun-server first (see README)"; exit 1; }
     [ -f "$ROOT/config/server.env" ] || { echo "create config/server.env first (see config/server.env.example)"; exit 1; }
     mkdir -p "$DIR"; printf '%s\n' "$emulator" > "$DIR/nfstr-emulator.service"
     systemctl --user daemon-reload; systemctl --user enable --now nfstr-emulator
     echo "installed. Logs: journalctl --user -u nfstr-emulator -f   (boot without login: loginctl enable-linger $USER)" ;;
esac
