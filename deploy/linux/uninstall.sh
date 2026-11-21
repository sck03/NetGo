#!/usr/bin/env bash
set -euo pipefail
[[ $EUID -eq 0 ]] || exit 1
systemctl disable --now netgo-server netgo-helper netgo-ddns 2>/dev/null || true
for table in netgo netgo_relay; do
 if nft list table inet "$table" >/dev/null 2>&1; then nft delete table inet "$table"; fi
done
for name in netgo-server netgo-helper netgo-ddns; do
 unit="/etc/systemd/system/$name.service"
 if [[ -f "$unit" ]] && grep -q 'ExecStart=/opt/netgo/current/' "$unit"; then rm -- "$unit"; fi
done
systemctl daemon-reload
printf '%s\n' 'Owned services/tables removed. Configurations, identities, releases and backups retained for recovery.'
