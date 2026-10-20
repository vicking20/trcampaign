#!/usr/bin/env bash
# Run this on the machine itself (from a terminal on its desktop), not over ssh:
#   ./join.sh NFSTR1-xxxx  [nickname]            Proton (umu)  - default
#   NFSTR_RUNNER=wine ./join.sh NFSTR1-xxxx      system Wine
cd "$(dirname "$0")"
exec python3 NFSTR_LAN/nfstr_launcher.py join "$1" --nick "${2:-GPDwin}"
