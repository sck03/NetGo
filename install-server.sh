#!/usr/bin/env bash
# Independent LAN entry. All installation and menu logic runs in Go.
set -euo pipefail
set_action=false; source_set=false; package_set=false; check_plan=false
remaining=("$@")
while (("${#remaining[@]}")); do
 value=${remaining[0]}; remaining=("${remaining[@]:1}")
 case "$value" in
  menu|install|configure|status|doctor|version|uninstall|render|network-apply|network-remove|redirect-remove)
   $set_action && { echo '一次只能指定一个操作' >&2; exit 2; };set_action=true;;
  --plan) check_plan=true;;
  --yes|--install-only|--private-ca|--password-stdin) ;;
  --source|--package|--sha256|--version|--config|--bundle|--tls-dir|--admin-email|--admin-alias|--output|--secrets)
   (("${#remaining[@]}")) || { echo "缺少 $value 的值" >&2;exit 2; }
   case "$value" in
    --source) source_set=true;;
    --package) package_set=true;;
    --version) [[ ${remaining[0]} == latest || ${remaining[0]} =~ ^[0-9]+\.[0-9]+\.[0-9]+(-[A-Za-z0-9.-]+)?(\+[A-Za-z0-9.-]+)?$ ]] || { echo '无效版本' >&2;exit 2; };;
   esac
   remaining=("${remaining[@]:1}");;
  --help|-h) ;;
  *) echo "未知参数: $value" >&2;exit 2;;
 esac
done
if $source_set && $package_set;then echo '安装来源参数冲突' >&2;exit 2;fi
for arg in "$@"; do
 if [[ $arg == --plan ]]; then printf '%s\n' 'Plan: Linux server; Go installer/menu; owned files only; no Python, downloads or system changes.'; exit 0; fi
done
case ${1:-} in
 --help|-h) printf '%s\n' 'NetGo 局域网服务端：bash install-server.sh [install|configure|status|doctor|uninstall] [Go 维护参数]' '包内首次运行自动安装并打开菜单；安装后使用 ng。' '--source DIR  本地平台包；--package FILE --sha256 HASH  校验 ZIP；--install-only  仅安装程序'; exit 0;;
esac
case $(uname -m) in x86_64|amd64) arch=amd64;; aarch64|arm64) arch=arm64;; *) echo '需要 Linux amd64 或 arm64' >&2;exit 1;; esac
root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
if [[ -x /opt/netgo/current/netgoctl ]]; then exec /opt/netgo/current/netgoctl server "$@"; fi
tool="$root/dist/linux-$arch/netgoctl"
source_root="$root"
args=("$@")
for ((i=0;i<${#args[@]};i++)); do
 if [[ ${args[i]} == --source ]]; then source_root=${args[i+1]:?Missing source directory};tool="$source_root/dist/linux-$arch/netgoctl";fi
done
if [[ -x $tool ]]; then
 if (($#==0)); then exec "$tool" server install --source "$source_root"; fi
 exec "$tool" server "$@"
fi
[[ $(uname -s) == Linux ]] || { echo '需要 Linux amd64 或 arm64'; exit 1; }
[[ $EUID == 0 ]] || { echo '请使用 root 执行安装'; exit 1; }
command -v curl >/dev/null && command -v sha256sum >/dev/null || { echo '请先安装 curl 和 ca-certificates';exit 1; }
scratch=$(mktemp -d /tmp/netgo-server-bootstrap.XXXXXXXX)
trap '[[ $scratch == /tmp/netgo-server-bootstrap.* && -d $scratch && ! -L $scratch ]] && rm -rf -- "$scratch"' EXIT
base=https://github.com/sck03/NetGo/releases/latest/download
name=netgo-server-installer-linux-$arch
curl --fail --location --proto '=https' --proto-redir '=https' --connect-timeout 20 --max-time 600 "$base/$name" -o "$scratch/$name"
curl --fail --location --proto '=https' --proto-redir '=https' --connect-timeout 20 --max-time 60 "$base/$name.sha256" -o "$scratch/checksum"
(cd "$scratch" && sha256sum --check checksum)
chmod 0755 "$scratch/$name"
if (($#==0)); then "$scratch/$name" server install; else "$scratch/$name" server "$@"; fi
