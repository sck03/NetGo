#!/usr/bin/env bash
# NetGo Linux server only. The Windows entry is install-client.ps1.
set -euo pipefail
VERSION=0.2.4-dev
REPOSITORY=https://github.com/sck03/NetGo
action=menu
source_root=''; package=''; digest=''; ref=main
from_source=false; plan=false; yes=false; install_only=false
setup_args=()
usage() {
 cat <<'HELP'
NetGo Linux 服务端安装与维护（Debian 12/13 amd64）
  bash install-server.sh                       交互菜单
  bash install-server.sh install               下载校验开发包、安装并初始化
  bash install-server.sh install --source DIR  使用本地完整包/已构建源码
  bash install-server.sh install --from-source [--ref COMMIT]
  bash install-server.sh install --package FILE --sha256 DIGEST
  bash install-server.sh configure [初始化参数]
  bash install-server.sh status | doctor
  bash install-server.sh uninstall [--yes]     卸载受管服务，保留身份与数据
  bash install-server.sh install --plan        只显示计划，不下载或修改系统

初始化参数：--config FILE --tls-dir DIR --private-ca --admin-email EMAIL
            --admin-alias ALIAS --password-stdin --resume
--private-ca 显式使用实验室私有 CA；正式域名可提供已有证书目录。
--install-only 仅安装程序，稍后运行 configure。密码不接受命令行参数。
从源码安装会自动准备固定 Go/Node 并原生编译；二进制包无需编译器。
开发包不代表公网、VPS 或 72 小时验收已经通过。
HELP
}
fail() { printf '%s\n' "$*" >&2; exit 1; }
set_action=false
while (($#)); do
 case "$1" in
  menu|install|configure|status|doctor|uninstall)
   $set_action && fail '一次只能指定一个操作'; action=$1; set_action=true; shift ;;
  --source|--package|--sha256|--ref|--version)
   (($#>=2)) || fail "缺少 $1 的值"
   case "$1" in --source) source_root=$2;; --package) package=$2;; --sha256) digest=$2;; --ref) ref=$2;; --version) VERSION=$2;; esac
   shift 2 ;;
  --config|--tls-dir|--admin-email|--admin-alias)
   (($#>=2)) || fail "缺少 $1 的值"; setup_args+=("$1" "$2"); shift 2 ;;
  --private-ca|--password-stdin|--resume) setup_args+=("$1"); shift ;;
  --from-source) from_source=true; shift ;;
  --plan) plan=true; shift ;;
  --yes|-y) yes=true; shift ;;
  --install-only) install_only=true; shift ;;
  --help|-h|help) usage; exit 0 ;;
  *) fail "未知参数: $1" ;;
 esac
done
[[ $VERSION =~ ^[0-9]+\.[0-9]+\.[0-9]+(-[a-zA-Z0-9.-]+)?$ ]] || fail '无效版本号'
[[ $ref == main || $ref =~ ^[0-9a-f]{40}$ || $ref =~ ^v[0-9]+\.[0-9]+\.[0-9]+(-[a-zA-Z0-9.-]+)?$ ]] || fail 'ref 仅允许 main、完整 commit 或版本 tag'
[[ -z $package || ( -z $source_root && $from_source == false ) ]] || fail '安装来源参数相互冲突'
[[ -z $digest || -n $package ]] || fail '--sha256 仅用于 --package'
if [[ $action != install && ( -n $source_root || -n $package || $from_source == true || $install_only == true ) ]]; then fail '安装来源参数仅适用于 install'; fi
if $plan; then
 printf 'Plan: action=%s; version=%s; Debian 12/13 amd64; server only.\n' "$action" "$VERSION"
 printf '%s\n' 'Install: verify package, install owned files/services, then explicit site/TLS/admin initialization (unless --install-only).' \
  'Uninstall: use local maintenance files; retain configuration, identities, backups and releases; restore owned forwarding changes.' \
  'No downloads, package installation, service or network changes in --plan.'
 exit 0
fi
if [[ $action == menu ]]; then
 printf '%s\n' 'NetGo 服务端' '1) 安装/检查同版本安装' '2) 首次配置/恢复配置' '3) 状态' '4) 诊断' '5) 卸载（保留数据）' '0) 退出'
 read -r -p '请选择 [1]: ' choice </dev/tty || fail '无交互终端，请显式指定操作'
 case ${choice:-1} in 1) action=install;; 2) action=configure;; 3) action=status;; 4) action=doctor;; 5) action=uninstall;; 0) exit 0;; *) fail '无效选项';; esac
fi
[[ $(uname -s) == Linux && $(uname -m) == x86_64 ]] || fail '仅支持 Linux amd64'
local_root=/opt/netgo/maintenance
if [[ $action == status ]]; then
 if [[ ! -f /opt/netgo/install.manifest ]]; then echo 'NetGo 未安装（如曾卸载，身份与数据可能仍保留）'; exit 0; fi
 /opt/netgo/current/netgoctl version
 systemctl --no-pager --full status netgo-server netbird-server netbird-relay netgo-caddy netgo-router netgo-helper
 exit $?
fi
[[ $EUID -eq 0 ]] || fail '请以 root 执行服务端安装/配置/卸载'
if [[ $action == uninstall ]]; then
 if [[ ! -f /opt/netgo/install.manifest && -f /opt/netgo/uninstalled.manifest ]]; then echo '已卸载，保留的数据未改动。'; exit 0; fi
 [[ -f "$local_root/deploy/linux/uninstall.sh" ]] || fail '没有找到本机受管卸载器；请检查安装状态'
 if ! $yes; then
  read -r -p '停止并卸载 NetGo，保留身份和数据。输入 yes 确认: ' answer </dev/tty || fail '无交互卸载请使用 --yes'
  [[ $answer == yes ]] || { echo '已取消'; exit 0; }
 fi
 exec bash "$local_root/deploy/linux/uninstall.sh"
fi
if [[ $action == doctor ]]; then exec /opt/netgo/current/netgoctl doctor --config /etc/netgo/site.json; fi
if [[ $action == configure ]]; then
 [[ -f "$local_root/scripts/server_setup.py" ]] || fail '请先安装程序'
 exec python3 -B "$local_root/scripts/server_setup.py" "${setup_args[@]}"
fi
os_id=$(sed -n 's/^ID=//p' /etc/os-release | tr -d '"')
os_version=$(sed -n 's/^VERSION_ID=//p' /etc/os-release | tr -d '"')
[[ $os_id == debian && ( $os_version == 12 || $os_version == 13 ) ]] || fail '仅支持 Debian 12/13 amd64'
missing=()
for item in curl ca-certificates python3 unzip; do
 [[ $(dpkg-query -W -f='${Status}' "$item" 2>/dev/null || true) == 'install ok installed' ]] || missing+=("$item")
done
if $from_source && ! command -v git >/dev/null; then missing+=(git); fi
if ((${#missing[@]})); then apt-get update; DEBIAN_FRONTEND=noninteractive apt-get install -y --no-upgrade --no-install-recommends "${missing[@]}"; fi
fetch() { curl --fail --location --silent --show-error --proto '=https' --proto-redir '=https' --retry 2 --connect-timeout 20 --max-time 600 "$1" --output "$2"; }
scratch=''
cleanup() { if [[ -n $scratch && $scratch == /tmp/netgo-server.* && -d $scratch && ! -L $scratch ]]; then rm -rf -- "$scratch"; fi; }
trap cleanup EXIT
if $from_source; then
 install -d -m 0700 /var/cache/netgo
 scratch=$(mktemp -d /tmp/netgo-server.XXXXXXXX)
 git -C "$scratch" init --quiet
 git -C "$scratch" remote add origin "$REPOSITORY.git"
 git -C "$scratch" fetch --depth=1 origin "$ref"
 commit=$(git -C "$scratch" rev-parse FETCH_HEAD)
 [[ $commit =~ ^[0-9a-f]{40}$ ]] || fail '无法确认源码 commit'
 source_root=/var/cache/netgo/source-$commit
 [[ ! -e $source_root ]] || fail "源码缓存已存在，请用 --source $source_root 复用或先审阅它"
 git -C "$scratch" checkout --detach --quiet "$commit"
 mv -- "$scratch" "$source_root"; scratch=''
 printf 'Source commit: %s\n' "$commit"
elif [[ -z $source_root ]]; then
 scratch=$(mktemp -d /tmp/netgo-server.XXXXXXXX)
 name="NetGo-$VERSION-linux-amd64.zip"
 if [[ -z $package ]]; then
  package="$scratch/$name"
  fetch "$REPOSITORY/releases/download/v$VERSION/$name" "$package" || fail '开发包下载失败；可选择 --from-source 或 --source 本地完整包'
  fetch "$REPOSITORY/releases/download/v$VERSION/SHA256SUMS" "$scratch/SHA256SUMS"
  digest=$(python3 - "$scratch/SHA256SUMS" "$name" <<'PY'
import re,sys
lines=[line.split() for line in open(sys.argv[1]) if line.split() and line.split()[-1]==sys.argv[2]]
if len(lines)!=1 or len(lines[0])!=2 or not re.fullmatch('[0-9a-f]{64}',lines[0][0]): raise SystemExit('Missing/duplicate package checksum')
print(lines[0][0])
PY
  )
 fi
 [[ $digest =~ ^[0-9a-fA-F]{64}$ ]] || fail '--package 需要可信的 --sha256 摘要'
 printf '%s  %s\n' "$digest" "$package" | sha256sum --check -
 source_root="$scratch/package"
 python3 - "$package" "$source_root" <<'PY'
from pathlib import Path, PurePosixPath
import shutil,sys,zipfile
root=Path(sys.argv[2]); root.mkdir(mode=0o700)
with zipfile.ZipFile(sys.argv[1]) as z:
    seen=set(); entries=z.infolist()
    if len(entries)>200000 or sum(e.file_size for e in entries)>4*1024**3: raise SystemExit('Package too large')
    for e in entries:
        name=e.filename.rstrip('/'); p=PurePosixPath(name)
        if not name or p.is_absolute() or '..' in p.parts or '\\' in name or ':' in name or str(p)!=name or name in seen: raise SystemExit('Unsafe package path')
        seen.add(name)
        mode=e.external_attr>>16
        if mode & 0o170000 not in (0,0o100000,0o040000): raise SystemExit('Package links/special files forbidden')
    for e in entries:
        path=root/e.filename
        if e.is_dir(): path.mkdir(parents=True,exist_ok=True); continue
        path.parent.mkdir(parents=True,exist_ok=True)
        with z.open(e) as src,path.open('xb') as dst: shutil.copyfileobj(src,dst)
        path.chmod(0o755 if (e.external_attr>>16)&0o111 else 0o644)
PY
 (cd "$source_root" && sha256sum --check --quiet SHA256SUMS)
fi
source_root=$(cd -- "$source_root" && pwd)
[[ -f "$source_root/deploy/linux/install.sh" && -f "$source_root/scripts/server_setup.py" ]] || fail '缺少完整 NetGo 服务端程序/安装文件'
if [[ ! -f "$source_root/dist/upstream-linux-amd64/SHA256SUMS" || ! -f "$source_root/dist/linux-amd64/SHA256SUMS" ]]; then
 bash "$source_root/scripts/bootstrap-linux.sh" --build
 source "$source_root/.tools/linux.env"
 export GOMAXPROCS=${GOMAXPROCS:-2} GOFLAGS=${GOFLAGS:--p=1} GOMEMLIMIT=${GOMEMLIMIT:-2GiB}
 bash "$source_root/scripts/build.sh"
 bash "$source_root/scripts/build-upstream.sh"
else
 bash "$source_root/scripts/bootstrap-linux.sh" --runtime
fi
bash "$source_root/deploy/linux/install.sh" "$VERSION"
if ! $install_only; then python3 -B "$local_root/scripts/server_setup.py" "${setup_args[@]}"; fi
printf '%s\n' '程序安装完成。使用 netgo-serverctl 打开维护菜单。'
