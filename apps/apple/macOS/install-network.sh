#!/usr/bin/env bash
set -euo pipefail
source=${1:?App resource directory required}; owner=${2:?Owner UID required}
[[ $EUID == 0 && $owner =~ ^[0-9]+$ && $owner -gt 0 ]] || exit 1
root=/usr/local/libexec/netgo
unit=/Library/LaunchDaemons/org.netgo.network.plist
if [[ -e $root || -e $unit ]]; then
 [[ -f $root/owner && $(cat "$root/owner") == "$owner" && -f $root/SHA256SUMS ]] || { echo 'Existing unowned network installation'; exit 1; }
 (cd "$root" && shasum -a 256 -c SHA256SUMS)
 [[ -S /var/run/netgo/netbird.sock ]] && exit 0
 launchctl kickstart -k system/org.netgo.network
 exit 0
fi
[[ ! -L $source && -f $source/bin/netbird && -f $source/bin/SHA256SUMS ]] || exit 1
(cd "$source/bin" && shasum -a 256 -c SHA256SUMS)
install -d -m 0755 "$root" /var/run/netgo
install -m 0755 "$source/bin/netbird" "$root/netbird"
install -d -m 0700 /var/lib/netgo-network
printf '%s\n' "$owner" > "$root/owner"
cat > "$root/run.sh" <<'RUN'
#!/usr/bin/env bash
set -euo pipefail
root=/usr/local/libexec/netgo
owner=$(cat "$root/owner")
install -d -m 0755 /var/run/netgo
NB_STATE_DIR=/var/lib/netgo-network "$root/netbird" --daemon-addr unix:///var/run/netgo/netbird.sock --log-file console service run &
child=$!
trap 'kill -TERM "$child" 2>/dev/null || true; wait "$child" || true' EXIT TERM INT
for _ in {1..100}; do
 if [[ -S /var/run/netgo/netbird.sock ]]; then chown "$owner" /var/run/netgo/netbird.sock; chmod 0600 /var/run/netgo/netbird.sock; break; fi
 kill -0 "$child"; sleep 0.1
done
wait "$child"
RUN
chmod 0755 "$root/run.sh"
(cd "$root" && shasum -a 256 netbird run.sh owner > SHA256SUMS)
cat > "$unit" <<'PLIST'
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>Label</key><string>org.netgo.network</string>
<key>ProgramArguments</key><array><string>/usr/local/libexec/netgo/run.sh</string></array>
<key>RunAtLoad</key><true/><key>KeepAlive</key><true/>
<key>StandardErrorPath</key><string>/var/lib/netgo-network/service-error.log</string>
</dict></plist>
PLIST
chmod 0644 "$unit"
launchctl bootstrap system "$unit"
for _ in {1..100}; do [[ -S /var/run/netgo/netbird.sock ]] && exit 0; sleep 0.1; done
exit 1
