#!/usr/bin/env bash
set -euo pipefail
if [[ ${1:-} == --plan ]]; then
 printf '%s\n' 'Plan: stop/remove only registered NetGo units; disconnect the managed router; remove owned tables and known command links. Retain configuration, identities, backups, binaries and accounts.'
 exit 0
fi
[[ $EUID -eq 0 ]] || { echo 'Run as root on Linux'; exit 1; }
manifest=/opt/netgo/install.manifest
[[ -f $manifest ]] || { echo 'Ownership manifest missing; manual review required'; exit 1; }
(cd / && sha256sum --check "$manifest")
units=()
while IFS= read -r line; do
 path=${line#*  }
 if [[ $path == /etc/systemd/system/*.service ]]; then
  grep -q '^# Managed by NetGo v1$' "$path" || { echo 'Unowned unit'; exit 1; }
  units+=("$(basename "$path")")
 fi
done < "$manifest"
systemctl stop netgo-server.service netgo-helper.service
if systemctl is-active --quiet netgo-router.service; then
 /opt/netgo/upstream/netbird --daemon-addr unix:///run/netgo-router/netbird.sock down
fi
for unit in "${units[@]}"; do systemctl disable --now "$unit"; done
for table in netgo netgo_relay; do
 if nft -j list table inet "$table" > /run/netgo-uninstall-table.json 2>/dev/null; then
  python3 - /run/netgo-uninstall-table.json "$table" <<'PY'
import json,sys
expected={"netgo":"NetGo lease gate v1","netgo_relay":"NetGo relay redirect v1"}[sys.argv[2]]
tables=[v["table"] for v in json.load(open(sys.argv[1]))["nftables"] if "table" in v]
assert len(tables)==1 and tables[0].get("comment")==expected, "Refusing to delete unowned nft table"
PY
  nft delete table inet "$table"
 fi
done
rm -f -- /run/netgo-uninstall-table.json
for unit in "${units[@]}"; do rm -- "/etc/systemd/system/$unit"; done
for name in netgo-server netgo-helper netgoctl netgo-client-service netgo-ddns netbird; do
 path="/usr/local/bin/$name"
 if [[ -L $path ]]; then
  target=$(readlink "$path")
  [[ $target == "/opt/netgo/current/$name" || $target == /opt/netgo/upstream/netbird ]] || { echo 'Unrecognized command link'; exit 1; }
  rm -- "$path"
 fi
done
[[ ! -L /opt/netgo/current ]] || rm -- /opt/netgo/current
systemctl daemon-reload
mv -- "$manifest" /opt/netgo/uninstalled.manifest
printf '%s\n' 'Owned services, command links and tables removed. Configurations, identities, releases, backups and accounts retained.'
