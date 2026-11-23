#!/usr/bin/env bash
# Standalone auxiliary-node entry. LAN installation is install-server.sh.
set -euo pipefail
repository=https://github.com/sck03/NetGo
version=latest; ref=main; action=menu; selected=false
source_root=''; package=''; digest=''; config_dir=''
from_source=false; plan=false; yes=false; install_only=false
fail() { printf '%s\n' "$*" >&2; exit 1; }
usage() {
 cat <<'HELP'
NetGo VPS 辅助节点（Debian 12/13 amd64、systemd）
  bash install-vps.sh                       维护菜单
  bash install-vps.sh install --config-dir DIR
  bash install-vps.sh install --from-source --config-dir DIR
  bash install-vps.sh install --source DIR --config-dir DIR
  bash install-vps.sh install --package FILE --sha256 DIGEST --config-dir DIR
  bash install-vps.sh configure --config-dir DIR
  bash install-vps.sh status | doctor
  bash install-vps.sh uninstall --yes        保留配置、证书和已校验程序
  bash install-vps.sh install --plan        不修改系统
DIR 为 LAN 上 export_vps.py 导出的 VPS 专用目录。
默认下载最新正式 Release 的校验包；尚无发布包时自动构建固定 commit。
--install-only 仅安装程序。--ref 支持 main、完整 commit 或版本 tag。
不自动接管现有 80/443 服务，不改 SSH、Docker 或全局防火墙策略。
HELP
}
while (($#)); do
 case "$1" in
  menu|install|configure|status|doctor|uninstall) $selected && fail '一次只能指定一个操作'; action=$1; selected=true; shift ;;
  --source|--package|--sha256|--config-dir|--ref|--version)
   (($#>=2)) || fail "缺少 $1 的值"
   case "$1" in --source) source_root=$2;; --package) package=$2;; --sha256) digest=$2;; --config-dir) config_dir=$2;; --ref) ref=$2;; --version) version=$2;; esac
   shift 2 ;;
  --from-source) from_source=true; shift ;;
  --plan) plan=true; shift ;;
  --yes|-y) yes=true; shift ;;
  --install-only) install_only=true; shift ;;
  --help|-h) usage; exit 0 ;;
  *) fail "未知参数: $1" ;;
 esac
done
[[ $version == latest || $version =~ ^[0-9]+\.[0-9]+\.[0-9]+(-[a-zA-Z0-9.-]+)?$ ]] || fail '无效版本号'
[[ $ref == main || $ref =~ ^[0-9a-f]{40}$ || $ref =~ ^v[0-9]+\.[0-9]+\.[0-9]+(-[a-zA-Z0-9.-]+)?$ ]] || fail '无效源码 ref'
[[ -z $package || ( -z $source_root && $from_source == false ) ]] || fail '安装来源参数冲突'
[[ -z $source_root || $from_source == false ]] || fail '安装来源参数冲突'
[[ -z $digest || -n $package ]] || fail '--sha256 仅用于 --package'
if [[ $action != install && $action != menu && ( -n $source_root || -n $package || $from_source == true || $install_only == true ) ]]; then fail '安装参数仅适用于 install'; fi
if $plan; then
 printf 'Plan: VPS action=%s; version=%s; Debian 12/13 amd64.\n' "$action" "$version"
 printf '%s\n' 'Install: verify independent VPS package/configuration, preflight ports, install owned services, check each service and Relay health.' \
 'Uninstall: remove only manifest-owned units and command link; retain configuration, TLS, binaries and account.' \
 'No downloads, package installation, service or network changes in --plan.'
 exit 0
fi
if [[ $action == menu ]]; then
 printf '%s\n' 'NetGo VPS 辅助节点' '1) 安装' '2) 配置/恢复' '3) 状态' '4) 诊断' '5) 卸载（保留配置）' '0) 退出'
 read -r -p '请选择 [1]: ' choice </dev/tty || fail '请显式指定操作'
 case ${choice:-1} in 1) action=install;; 2) action=configure;; 3) action=status;; 4) action=doctor;; 5) action=uninstall;; 0) exit 0;; *) fail '无效选项';; esac
fi
maintenance=/opt/netgo-vps/maintenance
ensure_haproxy() (
 [[ -n $config_dir && -f $config_dir/vps.json ]] || exit 0
 python3 - "$config_dir/vps.json" <<'PY' || exit 0
import json,sys
raise SystemExit(0 if json.load(open(sys.argv[1]))['auxiliary']['mode']=='443' else 1)
PY
 [[ ! -x /usr/sbin/haproxy ]] || exit 0
 [[ ! -e /run/systemd/system/haproxy.service && ! -L /run/systemd/system/haproxy.service ]] || fail '已有 HAProxy 运行时单元，请先检查'
 systemctl is-active --quiet haproxy.service && fail '已有 HAProxy 服务，请先检查'
 systemctl mask --runtime haproxy.service
 trap 'if [[ -L /run/systemd/system/haproxy.service && $(readlink /run/systemd/system/haproxy.service) == /dev/null ]]; then systemctl unmask --runtime haproxy.service; fi' EXIT
 apt-get update
 DEBIAN_FRONTEND=noninteractive apt-get install -y --no-upgrade --no-install-recommends haproxy
 systemctl disable haproxy.service
)
if [[ $action == status && ! -f /opt/netgo-vps/install.json ]]; then echo 'NetGo VPS 未安装'; exit 0; fi
[[ $(uname -s) == Linux && $(uname -m) == x86_64 ]] || fail '仅支持 Linux amd64'
if [[ $action != status ]]; then [[ $EUID -eq 0 ]] || fail '请以 root 执行'; fi
if [[ $action == uninstall ]] && ! $yes; then
 read -r -p '卸载 VPS 受管服务并保留配置，输入 yes 确认: ' answer </dev/tty || fail '请使用 --yes'
 [[ $answer == yes ]] || exit 0
fi
if [[ $action == install || $action == configure ]] && ! $install_only && [[ -z $config_dir ]]; then
 read -r -p 'VPS 专用配置目录: ' config_dir </dev/tty || fail '请提供 --config-dir 或 --install-only'
 [[ -n $config_dir ]] || fail '配置目录不能为空'
fi
if [[ $action != install ]]; then
 [[ -f "$maintenance/scripts/vps_setup.py" ]] || fail '本机受管维护工具缺失'
 if [[ $action == configure ]]; then ensure_haproxy; fi
 args=("$action"); [[ -z $config_dir ]] || args+=(--config-dir "$config_dir")
 exec python3 -B "$maintenance/scripts/vps_setup.py" "${args[@]}"
fi
os_id=$(sed -n 's/^ID=//p' /etc/os-release | tr -d '"')
os_version=$(sed -n 's/^VERSION_ID=//p' /etc/os-release | tr -d '"')
[[ $os_id == debian && ( $os_version == 12 || $os_version == 13 ) ]] || fail '支持 Debian 12/13 amd64'
missing=()
for item in curl ca-certificates python3 python3-cryptography unzip iproute2 nftables util-linux; do
 [[ $(dpkg-query -W -f='${Status}' "$item" 2>/dev/null || true) == 'install ok installed' ]] || missing+=("$item")
done
if ((${#missing[@]})); then apt-get update; DEBIAN_FRONTEND=noninteractive apt-get install -y --no-upgrade --no-install-recommends "${missing[@]}"; fi
fetch() { curl --fail --location --silent --show-error --proto '=https' --proto-redir '=https' --retry 2 --connect-timeout 20 --max-time 600 "$1" --output "$2"; }
scratch=$(mktemp -d /tmp/netgo-vps.XXXXXXXX)
cleanup() { [[ $scratch == /tmp/netgo-vps.* && -d $scratch && ! -L $scratch ]] && rm -rf -- "$scratch"; }
trap cleanup EXIT
if [[ -z $source_root && -z $package ]] && ! $from_source; then
 if [[ $version == latest ]]; then
  if fetch 'https://api.github.com/repos/sck03/NetGo/releases/latest' "$scratch/release.json"; then
   version=$(python3 - "$scratch/release.json" <<'PY'
import json,re,sys
r=json.load(open(sys.argv[1])); t=r['tag_name']
if r.get('draft') or r.get('prerelease') or not re.fullmatch(r'v[0-9]+\.[0-9]+\.[0-9]+',t): raise SystemExit('Invalid stable Release')
print(t[1:])
PY
)
  else from_source=true; fi
 fi
 if ! $from_source; then
  name="NetGo-$version-vps-linux-amd64.zip"
  if fetch "$repository/releases/download/v$version/$name" "$scratch/$name"; then
   fetch "$repository/releases/download/v$version/SHA256SUMS" "$scratch/SHA256SUMS"
   digest=$(python3 - "$scratch/SHA256SUMS" "$name" <<'PY'
import re,sys
lines=[l.split() for l in open(sys.argv[1]) if l.split() and l.split()[-1]==sys.argv[2]]
if len(lines)!=1 or len(lines[0])!=2 or not re.fullmatch('[0-9a-f]{64}',lines[0][0]):raise SystemExit('Missing package checksum')
print(lines[0][0])
PY
)
   package="$scratch/$name"
  else from_source=true; fi
 fi
fi
if $from_source; then
 echo '尚无匹配发布包，构建已解析的 GitHub 源码 commit。'
 command -v git >/dev/null || { apt-get update; apt-get install -y --no-upgrade --no-install-recommends git; }
 install -d -m 0700 /var/cache/netgo-vps
 source_root=$(mktemp -d /var/cache/netgo-vps/source.XXXXXXXX)
 git -C "$source_root" init --quiet; git -C "$source_root" remote add origin "$repository.git"
 git -C "$source_root" fetch --depth=1 origin "$ref"
 commit=$(git -C "$source_root" rev-parse FETCH_HEAD); [[ $commit =~ ^[0-9a-f]{40}$ ]] || fail '无法确认 commit'
 git -C "$source_root" checkout --detach --quiet "$commit"
 printf 'Source commit: %s; cache: %s\n' "$commit" "$source_root"
 bash "$source_root/scripts/bootstrap-vps.sh"
 source "$source_root/.tools/vps.env"
 bash "$source_root/scripts/build-vps.sh"
elif [[ -n $package ]]; then
 [[ $digest =~ ^[0-9a-fA-F]{64}$ ]] || fail '--package 需要可信 SHA256'
 printf '%s  %s\n' "$digest" "$package" | sha256sum --check -
 source_root="$scratch/package"
 python3 - "$package" "$source_root" <<'PY'
from pathlib import Path,PurePosixPath
import shutil,sys,zipfile
root=Path(sys.argv[2]);root.mkdir(mode=0o700)
with zipfile.ZipFile(sys.argv[1]) as z:
 seen=set();entries=z.infolist()
 if len(entries)>10000 or sum(e.file_size for e in entries)>512*1024**2:raise SystemExit('Package too large')
 for e in entries:
  name=e.filename.rstrip('/');p=PurePosixPath(name);mode=e.external_attr>>16
  if not name or p.is_absolute() or '..' in p.parts or '\\' in name or ':' in name or str(p)!=name or name in seen or (mode & 0o170000) not in (0,0o100000,0o040000):raise SystemExit('Unsafe package path')
  seen.add(name)
 for e in entries:
  path=root/e.filename
  if e.is_dir():path.mkdir(parents=True,exist_ok=True);continue
  path.parent.mkdir(parents=True,exist_ok=True)
  with z.open(e) as src,path.open('xb') as dst:shutil.copyfileobj(src,dst)
  path.chmod(0o755 if (e.external_attr>>16)&0o111 else 0o644)
PY
 (cd "$source_root" && sha256sum --check --quiet SHA256SUMS)
fi
source_root=$(cd -- "$source_root" && pwd)
[[ -f "$source_root/scripts/vps_setup.py" && -f "$source_root/dist/vps/bin/frps" && -f "$source_root/dist/vps/bin/netbird-relay" ]] || fail '缺少完整 VPS 包，请运行 scripts/build-vps.sh'
(cd "$source_root/dist/vps" && sha256sum --check --quiet SHA256SUMS)
if ! $install_only; then ensure_haproxy; fi
args=(install --source "$source_root"); if ! $install_only && [[ -n $config_dir ]]; then args+=(--config-dir "$config_dir"); fi
python3 -B "$source_root/scripts/vps_setup.py" "${args[@]}"
echo 'VPS 程序安装完成。使用 netgo-vpsctl 打开独立维护菜单。'
