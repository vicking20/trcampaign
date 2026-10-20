#!/usr/bin/env bash
# Stops any running relay.py / agent.py (kept in a file so the pattern never matches the caller's own command line).
for p in $(pgrep -f "python3 (relay|agent)\.py"); do
  [ "$p" = "$$" ] || kill "$p" 2>/dev/null
done
true
