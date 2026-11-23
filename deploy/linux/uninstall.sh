#!/usr/bin/env bash
set -euo pipefail
[[ $# -eq 0 || ( $# -eq 1 && $1 == --plan ) ]] || { echo 'Usage: uninstall.sh [--plan]' >&2; exit 2; }
if [[ ${1:-} == --plan ]]; then
 printf '%s\n' 'Plan: stop/remove only registered NetGo units; disconnect the managed router; remove owned tables and known command links. Retain configuration, identities, backups, binaries and accounts.'
 exit 0
fi
[[ $EUID -eq 0 ]] || { echo 'Run as root on Linux'; exit 1; }
manifest=/opt/netgo/install.manifest
if [[ ! -f $manifest && -f /opt/netgo/uninstalled.manifest ]]; then echo 'Already uninstalled; retained data unchanged.'; exit 0; fi
[[ -f $manifest ]] || { echo 'Ownership manifest missing; manual review required'; exit 1; }
exec 9>/opt/netgo/.install.lock
flock -n 9 || { echo 'Another installation or uninstall is running'; exit 1; }
(cd / && sha256sum --check "$manifest")
units=()
while IFS= read -r line; do
 path=${line#*  }
 if [[ $path == /etc/systemd/system/*.service ]]; then
  grep -q '^# Managed by NetGo v1$' "$path" || { echo 'Unowned unit'; exit 1; }
  units+=("$(basename "$path")")
 fi
done < "$manifest"
for name in netgo-server netgo-helper netgoctl netgo-client-service netgo-ddns netbird netgo-serverctl; do
 path="/usr/local/bin/$name"
 if [[ -e $path || -L $path ]]; then
  [[ -L $path ]] || { echo 'Unrecognized command file'; exit 1; }
  target=$(readlink "$path")
  expected="/opt/netgo/current/$name"
  [[ $name != netbird ]] || expected=/opt/netgo/upstream/netbird
  [[ $name != netgo-serverctl ]] || expected=/opt/netgo/maintenance/install-server.sh
  [[ $target == "$expected" ]] || { echo 'Unrecognized command link'; exit 1; }
 fi
done
tables=()
for table in netgo netgo_relay; do
 if nft -j list table inet "$table" > /run/netgo-uninstall-table.json 2>/dev/null; then
  python3 -B /opt/netgo/maintenance/scripts/nft_owner.py "$table"
  tables+=("$table")
 fi
done
rm -f -- /run/netgo-uninstall-table.json
systemctl stop netgo-server.service
if systemctl is-active --quiet netgo-router.service; then
 NB_STATE_DIR=/var/lib/netgo-router /opt/netgo/upstream/netbird --daemon-addr unix:///run/netgo-router/netbird.sock down
fi
for unit in "${units[@]}"; do systemctl disable --now "$unit"; done
for table in "${tables[@]}"; do nft delete table inet "$table"; done
python3 -B /opt/netgo/maintenance/scripts/server_setup.py --restore-forwarding --lock-fd 9
for unit in "${units[@]}"; do rm -- "/etc/systemd/system/$unit"; done
for name in netgo-server netgo-helper netgoctl netgo-client-service netgo-ddns netbird netgo-serverctl; do
 path="/usr/local/bin/$name"
 if [[ -L $path ]]; then
  rm -- "$path"
 fi
done
[[ ! -L /opt/netgo/current ]] || rm -- /opt/netgo/current
systemctl daemon-reload
mv -- "$manifest" /opt/netgo/uninstalled.manifest
printf '%s\n' 'Owned services, command links and tables removed. Configurations, identities, releases, backups and accounts retained.'
