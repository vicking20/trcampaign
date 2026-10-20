#!/usr/bin/env bash
# The game fetches News over https://<host>:443; the Autolog service listens on 8081 (TR_AUTOLOG_PORT). This adds 443 -> 8081 redirects for every
# address of this machine (LAN, VPN, loopback). Needs sudo, lost on reboot. Undo: sudo nft delete table ip nfstr_nat
sudo nft delete table ip nfstr_nat 2>/dev/null; sudo nft delete table ip nfstr_lan 2>/dev/null
sudo nft -f - <<'N'
table ip nfstr_nat {
  chain out {
    type nat hook output priority -100;
    fib daddr type local tcp dport 443 redirect to :8081
  }
  chain pre {
    type nat hook prerouting priority -100;
    fib daddr type local tcp dport 443 redirect to :8081
  }
}
N
echo "443 -> 8081 redirects:"; sudo nft list ruleset | grep -E "redirect|table"
