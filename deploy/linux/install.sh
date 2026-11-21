#!/usr/bin/env bash
set -euo pipefail
[[ $EUID -eq 0 ]] || { echo 'Run as root on target Linux'; exit 1; }
root=$(cd "$(dirname "$0")/../.." && pwd)
version=${1:-0.2.0-dev}
[[ $version =~ ^[a-zA-Z0-9][a-zA-Z0-9._-]*$ ]] || exit 2
target="/opt/netgo/releases/$version"
[[ ! -e "$target" && ! -e /opt/netgo/current ]] || { echo 'Existing release; use upgrade'; exit 1; }
command -v nft >/dev/null
command -v systemctl >/dev/null
[[ $(uname -s) == Linux && $(uname -m) == x86_64 ]] || { echo 'This package targets native Linux amd64'; exit 1; }
components=${NETGO_UPSTREAM_DIR:-"$root/dist/upstream-linux-amd64"}
for binary in netbird-server netbird netbird-relay; do
 [[ -x "$components/$binary" ]] || { echo "Build patched upstream natively on Linux first"; exit 1; }
done
[[ -f "$components/SHA256SUMS" ]] || { echo "Upstream checksum manifest missing"; exit 1; }
(cd "$components" && sha256sum --check SHA256SUMS)
[[ ! -e /opt/netgo/upstream ]] || { echo 'Existing upstream installation; stage a reviewed upgrade instead'; exit 1; }
(cd "$root/dist" && sha256sum --check SHA256SUMS)
for unit in "$root"/deploy/linux/netgo-*.service "$root"/deploy/linux/netbird-*.service; do
 name=$(basename "$unit")
 [[ ! -e "/etc/systemd/system/$name" ]] || { echo "Existing $name: review before replacing"; exit 1; }
done
getent group netgo >/dev/null || groupadd --system netgo
id netgo >/dev/null 2>&1 || useradd --system --gid netgo --home-dir /var/lib/netgo --shell /usr/sbin/nologin netgo
install -d -m 0755 "$target" /opt/netgo /opt/netgo/upstream
install -d -m 0750 -o netgo -g netgo /var/lib/netgo
install -d -m 0750 -o root -g netgo /etc/netgo
for name in netgo-server netgo-helper netgoctl netgo-client-service netgo-ddns; do
 install -m 0755 "$root/dist/linux-amd64/$name" "$target/$name"
done
for binary in netbird-server netbird netbird-relay; do
 install -m 0755 "$components/$binary" "/opt/netgo/upstream/$binary"
done
for unit in "$root"/deploy/linux/netgo-*.service "$root"/deploy/linux/netbird-*.service; do
 install -m 0644 "$unit" "/etc/systemd/system/$(basename "$unit")"
done
ln -s "$target" /opt/netgo/current
systemctl daemon-reload
printf '%s\n' 'Installed. No network rules or forwarding settings changed. Complete the operations guide before starting.'
