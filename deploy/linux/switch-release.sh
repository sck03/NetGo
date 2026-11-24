#!/usr/bin/env bash
set -euo pipefail
[[ $EUID -eq 0 ]] || exit 1
version=${1:?Specify a reviewed installed version}
[[ $version =~ ^[a-zA-Z0-9][a-zA-Z0-9._-]*$ ]] || exit 2
next="/opt/netgo/releases/$version"
[[ -x "$next/netgo-server" && -L /opt/netgo/current && ! -e /opt/netgo/next ]] || exit 1
old=$(readlink -f /opt/netgo/current)
[[ $old == /opt/netgo/releases/* ]] || exit 1
# A release switch requires the same schema; incompatible data must be archived.
"$next/netgoctl" config validate --config /etc/netgo/site.json
ln -s "$next" /opt/netgo/next
mv -T /opt/netgo/next /opt/netgo/current
if systemctl restart netgo-helper netgo-server && curl --fail --retry 5 --retry-delay 1 http://127.0.0.1:8081/netgo/v2/health; then
 printf '%s\n' "$old" > /var/lib/netgo/previous-release
else
 ln -s "$old" /opt/netgo/next
 mv -T /opt/netgo/next /opt/netgo/current
 systemctl restart netgo-helper netgo-server
 exit 1
fi
