#!/usr/bin/env bash
set -euo pipefail
root=$(cd "$(dirname "$0")/../.." && pwd)
plan=false
if [[ ${1:-} == --plan ]]; then plan=true; shift; fi
version=${1:-0.2.3-dev}
[[ $version =~ ^[a-zA-Z0-9][a-zA-Z0-9._-]{0,63}$ ]] || exit 2
target="/opt/netgo/releases/$version"
if $plan; then
 printf 'Plan only: target %s; Debian 13 amd64/root/nft/conntrack/systemd/Python required.\n' "$target"
 printf '%s\n' 'Create owned units and CLI links. No services/forwarding/firewall enabled. Failed install removes newly created files and retains data/accounts.'
 exit 0
fi
[[ $EUID -eq 0 ]] || { echo 'Run as root on target Linux'; exit 1; }
python3 -B "$root/scripts/linux_preflight.py" --mode install
for command in nft conntrack systemctl python3 sha256sum; do command -v "$command" >/dev/null; done
[[ $(uname -s) == Linux && $(uname -m) == x86_64 ]] || { echo 'This package targets native Linux amd64'; exit 1; }
components=${NETGO_UPSTREAM_DIR:-"$root/dist/upstream-linux-amd64"}
for binary in netbird-server netbird netbird-relay caddy frpc frps; do
 [[ -x "$components/$binary" ]] || { echo "Build patched upstream natively on Linux first"; exit 1; }
done
[[ -f "$components/SHA256SUMS" ]] || { echo "Upstream checksum manifest missing"; exit 1; }
(cd "$components" && sha256sum --check SHA256SUMS)
(cd "$root/dist/linux-amd64" && sha256sum --check SHA256SUMS)
python3 - "$root/dist/linux-amd64/release.json" "$version" <<'PY'
import json,sys
m=json.load(open(sys.argv[1]))
assert m == dict(version=sys.argv[2],protocol=1,schema=1,os="linux",arch="amd64"), "release compatibility mismatch"
PY
manifest=/opt/netgo/install.manifest
reuse=false
if [[ -f /opt/netgo/uninstalled.manifest && ! -e /opt/netgo/current && -d "$target" && -d /opt/netgo/upstream ]]; then
 cmp -s "$root/dist/linux-amd64/SHA256SUMS" "$target/SHA256SUMS" || { echo 'Retained release differs; use a new reviewed version'; exit 1; }
 (cd "$target" && sha256sum --check SHA256SUMS)
 while IFS= read -r line; do
  path=${line#*  }
  if [[ $path == /opt/netgo/upstream/* ]]; then printf '%s\n' "$line" | sha256sum --check -; fi
 done < /opt/netgo/uninstalled.manifest
 reuse=true
fi
if [[ -f $manifest && -L /opt/netgo/current && $(readlink -f /opt/netgo/current) == "$target" ]]; then
 cmp -s "$root/dist/linux-amd64/SHA256SUMS" "$target/SHA256SUMS" || { echo 'Same version has different contents; use a new reviewed version'; exit 1; }
 (cd / && sha256sum --check "$manifest")
 (cd "$target" && sha256sum --check SHA256SUMS)
 echo 'Matching installation already exists; no changes.'; exit 0
fi
if ! $reuse; then
 [[ ! -e "$target" && ! -e /opt/netgo/current && ! -e /opt/netgo/upstream && ! -e $manifest ]] || { echo 'Existing installation; use upgrade or inspect recovery files'; exit 1; }
fi
for unit in "$root"/deploy/linux/netgo-*.service "$root"/deploy/linux/netbird-*.service; do
 name=$(basename "$unit")
 grep -q '^# Managed by NetGo v1$' "$unit"
 [[ ! -e "/etc/systemd/system/$name" ]] || { echo "Existing $name: review before replacing"; exit 1; }
done
for name in netgo-server netgo-helper netgoctl netgo-client-service netgo-ddns netbird; do
 [[ ! -e "/usr/local/bin/$name" && ! -L "/usr/local/bin/$name" ]] || { echo "Existing $name command"; exit 1; }
done
install -d -m 0755 /opt/netgo /opt/netgo/releases /usr/local/bin
journal=/opt/netgo/.install-transaction
mkdir -m 0700 "$journal"
created=(); links=(); committed=false
rollback() {
 status=$?
 if ! $committed; then
  for path in "${links[@]}"; do [[ -L "$path" ]] && rm -- "$path"; done
  for path in "${created[@]}"; do [[ -f "$path" ]] && rm -- "$path"; done
  if ! $reuse; then rmdir -- "$target" /opt/netgo/upstream 2>/dev/null || true; fi
  systemctl daemon-reload || true
  printf 'Install failed (%s). Recovery journal retained at %s.\n' "$status" "$journal"
 fi
}
trap rollback EXIT
getent group netgo >/dev/null || groupadd --system netgo
id netgo >/dev/null 2>&1 || useradd --system --gid netgo --home-dir /var/lib/netgo --shell /usr/sbin/nologin netgo
install -d -m 0755 "$target" /opt/netgo /opt/netgo/upstream
[[ -d /var/lib/netgo ]] || install -d -m 0750 -o netgo -g netgo /var/lib/netgo
[[ -d /etc/netgo ]] || install -d -m 0750 -o root -g netgo /etc/netgo
[[ -d /var/lib/netgo-router ]] || install -d -m 0700 -o root -g root /var/lib/netgo-router
if [[ ! -e /var/lib/netgo-router/config.json ]]; then
 # Safe first boot: an administrator sets the verified local ManagementURL
 # before enrolling the dedicated router. Never default to the public cloud.
 printf '%s\n' '{"DisableAutoConnect":true,"ManagementURL":"https://127.0.0.1:1"}' > /var/lib/netgo-router/config.json
 chmod 0600 /var/lib/netgo-router/config.json
fi
for name in netgo-server netgo-helper netgoctl netgo-client-service netgo-ddns; do
 if ! $reuse; then created+=("$target/$name"); install -m 0755 "$root/dist/linux-amd64/$name" "$target/$name"; fi
done
for name in release.json SHA256SUMS; do if ! $reuse; then created+=("$target/$name"); install -m 0644 "$root/dist/linux-amd64/$name" "$target/$name"; fi; done
for binary in netbird-server netbird netbird-relay caddy frpc frps; do
 if ! $reuse; then created+=("/opt/netgo/upstream/$binary"); install -m 0755 "$components/$binary" "/opt/netgo/upstream/$binary"; fi
done
for unit in "$root"/deploy/linux/netgo-*.service "$root"/deploy/linux/netbird-*.service; do
 path="/etc/systemd/system/$(basename "$unit")"; created+=("$path"); install -m 0644 "$unit" "$path"
done
ln -s "$target" /opt/netgo/current; links+=(/opt/netgo/current)
for name in netgo-server netgo-helper netgoctl netgo-client-service netgo-ddns; do
 ln -s "/opt/netgo/current/$name" "/usr/local/bin/$name"; links+=("/usr/local/bin/$name")
done
ln -s /opt/netgo/upstream/netbird /usr/local/bin/netbird; links+=(/usr/local/bin/netbird)
owned=(/opt/netgo/upstream/netbird /opt/netgo/upstream/netbird-server /opt/netgo/upstream/netbird-relay /opt/netgo/upstream/caddy /opt/netgo/upstream/frpc /opt/netgo/upstream/frps)
for path in "${created[@]}"; do [[ $path == /etc/systemd/system/* ]] && owned+=("$path"); done
sha256sum "${owned[@]}" > "$journal/manifest"
created+=("$manifest"); install -m 0600 "$journal/manifest" "$manifest"
systemctl daemon-reload
committed=true
if $reuse; then rm -- /opt/netgo/uninstalled.manifest; fi
rm -- "$journal/manifest"; rmdir -- "$journal"
printf '%s\n' 'Installed. No network rules or forwarding settings changed. Complete the operations guide before starting.'
