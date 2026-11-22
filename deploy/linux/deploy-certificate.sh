#!/usr/bin/env bash
# Certbot DNS-01 deploy hook. Does not issue certificates or open public ports.
set -euo pipefail
plan=false; check=false
if [[ ${1:-} == --plan ]]; then plan=true; shift; fi
if [[ ${1:-} == --check ]]; then check=true; shift; fi
role=${1:-}; hostname=${2:-}
case "$role" in
 control) service=netgo-caddy; action=reload ;;
 relay) service=netbird-relay; action=restart ;;
 tunnel) service=netgo-frps; action=restart ;;
 *) echo 'role must be control, relay or tunnel' >&2; exit 2 ;;
esac
[[ $hostname =~ ^[a-zA-Z0-9][a-zA-Z0-9.-]{0,252}$ ]] || { echo 'DNS hostname required' >&2; exit 2; }
destination=/etc/netgo/tls
if $plan; then
 printf 'Plan only: validate %s certificate/key for %s; replace %s/%s.{crt,key}; %s active %s; restore files on failure. No network ports opened.\n' "$role" "$hostname" "$destination" "$role" "$action" "$service"
 exit 0
fi
lineage=${RENEWED_LINEAGE:?Certbot must supply RENEWED_LINEAGE}
[[ $lineage == /* && -f "$lineage/fullchain.pem" && -f "$lineage/privkey.pem" ]] || exit 2
for tool in openssl sha256sum; do command -v "$tool" >/dev/null; done
openssl verify -partial_chain -trusted "$lineage/fullchain.pem" -purpose sslserver -verify_hostname "$hostname" "$lineage/fullchain.pem" >/dev/null
openssl x509 -in "$lineage/fullchain.pem" -checkend 1209600 -noout >/dev/null
cert_key=$(openssl x509 -in "$lineage/fullchain.pem" -pubkey -noout | openssl pkey -pubin -outform DER | sha256sum)
private_key=$(openssl pkey -in "$lineage/privkey.pem" -passin pass: -pubout -outform DER | sha256sum)
[[ $cert_key == "$private_key" ]] || { echo 'Certificate and private key do not match' >&2; exit 1; }
if $check; then printf 'PASS certificate hostname, validity and key pair for %s\n' "$hostname"; exit 0; fi
[[ $EUID -eq 0 ]] || { echo 'Run the deploy hook as root on target Linux' >&2; exit 1; }
for tool in systemctl install; do command -v "$tool" >/dev/null; done
getent group netgo >/dev/null
install -d -m 0750 -o root -g netgo "$destination"
[[ ! -L "$destination/$role.crt" && ! -L "$destination/$role.key" ]] || { echo 'Review existing certificate symlinks before using this hook' >&2; exit 1; }
had_previous=false
if [[ -e "$destination/$role.crt" || -e "$destination/$role.key" ]]; then
 [[ -f "$destination/$role.crt" && -f "$destination/$role.key" ]] || { echo 'Previous certificate pair incomplete' >&2; exit 1; }
 had_previous=true
fi
journal="$destination/.certificate-$role.transaction"
mkdir -m 0700 "$journal"
committed=false; saved=false; cert_written=false; key_written=false; active=false
systemctl is-active --quiet "$service" && active=true
recover() {
 status=$?
 if ! $committed; then
  recovered=true
  if $saved; then
   cp -p -- "$journal/previous.crt" "$destination/$role.crt" || recovered=false
   cp -p -- "$journal/previous.key" "$destination/$role.key" || recovered=false
  elif ! $had_previous; then
   if $cert_written; then rm -f -- "$destination/$role.crt" || recovered=false; fi
   if $key_written; then rm -f -- "$destination/$role.key" || recovered=false; fi
  fi
  if $active && $saved; then systemctl "$action" "$service" || recovered=false; fi
  printf 'Certificate update failed; rollback completed=%s. Protected journal retained: %s\n' "$recovered" "$journal" >&2
 fi
 exit "$status"
}
trap recover EXIT
if $had_previous; then
 cp -p -- "$destination/$role.crt" "$journal/previous.crt"
 cp -p -- "$destination/$role.key" "$journal/previous.key"
 saved=true
fi
install -m 0640 -o root -g netgo "$lineage/fullchain.pem" "$journal/next.crt"
install -m 0640 -o root -g netgo "$lineage/privkey.pem" "$journal/next.key"
mv -T -- "$journal/next.crt" "$destination/$role.crt"; cert_written=true
mv -T -- "$journal/next.key" "$destination/$role.key"; key_written=true
if $active; then
 systemctl "$action" "$service"
 systemctl is-active --quiet "$service"
fi
committed=true
rm -f -- "$journal/previous.crt" "$journal/previous.key"
rmdir -- "$journal"
printf 'Installed %s certificate for %s. Service active=%s.\n' "$role" "$hostname" "$active"
