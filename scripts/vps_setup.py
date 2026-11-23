#!/usr/bin/env python3
"""Install and maintain the independently owned NetGo auxiliary node."""
import argparse
from datetime import datetime, timezone
import hashlib
import ipaddress
import json
import os
from pathlib import Path
import re
import shutil
import socket
import subprocess
import sys
import time
import tomllib
import urllib.request
from urllib.parse import urlsplit

BASE = Path('/opt/netgo-vps')
ETC = Path('/etc/netgo-vps')
UNIT_DIR = Path('/etc/systemd/system')
LINK = Path('/usr/local/bin/netgo-vpsctl')
OWNER = 'NetGo VPS v1'
MARKER = '# Managed by ' + OWNER + '\n'
NAMES = ('frps', 'relay', 'haproxy', 'redirect')
TLS = {'tls/relay.crt', 'tls/relay.key', 'tls/tunnel.crt', 'tls/tunnel.key', 'tls/ca.crt'}
CONFIG = {'auxiliary/frps.toml', 'auxiliary/relay.env', 'auxiliary/haproxy.cfg', 'auxiliary/relay-port.nft'}

def sha(path): return hashlib.sha256(Path(path).read_bytes()).hexdigest()

def safe(path):
    path = Path(path)
    for part in (path, *path.parents):
        if part.is_symlink(): raise ValueError('Symlink path rejected: ' + str(part))
    return path

def write(path, data, mode=0o600):
    path = safe(path)
    parent_exists = path.parent.exists()
    path.parent.mkdir(mode=0o750, parents=True, exist_ok=True)
    if not parent_exists: path.parent.chmod(0o750)
    temporary = path.with_name(path.name + '.new')
    fd = os.open(temporary, os.O_WRONLY | os.O_CREAT | os.O_EXCL, mode)
    try:
        os.fchmod(fd, mode)
        with os.fdopen(fd, 'wb') as handle:
            handle.write(data.encode() if isinstance(data, str) else data); handle.flush(); os.fsync(handle.fileno())
        temporary.replace(path)
    finally:
        temporary.unlink(missing_ok=True)

def run(args):
    result = subprocess.run(list(map(str, args)), capture_output=True, text=True, timeout=60)
    if result.returncode:
        write(BASE / 'command-error.txt', result.stdout + result.stderr)
        raise RuntimeError(Path(str(args[0])).name + ' failed; private diagnostics: /opt/netgo-vps/command-error.txt')
    return result.stdout.strip()

def port(value):
    if isinstance(value, bool) or not isinstance(value, int) or not 1 <= value <= 65535: raise ValueError('Invalid port')
    return value

def load_bundle(folder):
    folder = safe(Path(folder).absolute()); data = json.loads(safe(folder / 'vps.json').read_text())
    if data.get('schema') != 1 or data.get('owner') != 'NetGo VPS deployment': raise ValueError('Not a VPS-only export')
    if not re.fullmatch(r'[A-Za-z0-9_-]{1,128}', data.get('site_id','')): raise ValueError('Invalid site ID')
    files = data['files']; required = {'auxiliary/frps.toml', 'auxiliary/relay.env'} | (TLS - {'tls/ca.crt'})
    if not required <= files.keys() or files.keys() - (CONFIG | TLS): raise ValueError('Unexpected or missing bundle files')
    for name, digest in files.items():
        path = safe(folder / name)
        if not re.fullmatch('[0-9a-f]{64}', digest) or not path.is_file() or sha(path) != digest: raise ValueError('Bundle digest mismatch: ' + name)
    aux = data['auxiliary']
    if aux.get('mode') not in ('diagnostic', '443'): raise ValueError('Unsupported VPS mode')
    for key in ('control_port','control_backend_port','tunnel_port','tunnel_bind_port','relay_port','relay_bind_port','stun_port'): port(aux[key])
    uri = urlsplit(aux['relay_endpoint'])
    if uri.scheme != 'rels' or uri.port != aux['relay_port'] or not uri.hostname or uri.path or uri.username or uri.query or uri.fragment: raise ValueError('Invalid relay endpoint')
    for host in (uri.hostname, aux['control_host'], aux['tunnel_host'], aux['host']):
        if not re.fullmatch(r'[A-Za-z0-9.-]{1,253}', host): raise ValueError('Invalid auxiliary hostname')
    frp = tomllib.loads((folder / 'auxiliary/frps.toml').read_text())
    expected_keys = {'bindAddr','bindPort','proxyBindAddr','auth','transport','allowPorts','maxPortsPerClient'}
    if set(frp) != expected_keys or frp['bindPort'] != aux['tunnel_bind_port'] or frp['allowPorts'] != [{'single':aux['control_backend_port']}] or frp['maxPortsPerClient'] != 1: raise ValueError('Unsupported frps configuration')
    address = '127.0.0.1' if aux['mode'] == '443' else '0.0.0.0'
    if frp['bindAddr'] != address or frp['proxyBindAddr'] != address: raise ValueError('Invalid frps listen address')
    if set(frp['auth']) != {'method','token'} or frp['auth']['method'] != 'token' or not re.fullmatch('[0-9a-f]{64}', frp['auth']['token']): raise ValueError('Invalid frps authentication')
    if frp['transport'] != {'tls':{'force':True,'certFile':'/etc/netgo/tls/tunnel.crt','keyFile':'/etc/netgo/tls/tunnel.key'}}: raise ValueError('Invalid frps TLS configuration')
    env = dict(line.split('=',1) for line in (folder / 'auxiliary/relay.env').read_text().splitlines() if line)
    expected = {'NB_LISTEN_ADDRESS':':' + str(aux['relay_bind_port']), 'NB_EXPOSED_ADDRESS':aux['relay_endpoint'],
                'NB_ENABLE_STUN':'true','NB_STUN_PORTS':str(aux['stun_port']), 'NB_METRICS_PORT':'9091',
                'NB_METRICS_LISTEN_ADDRESS':'127.0.0.1:9091','NB_HEALTH_LISTEN_ADDRESS':'127.0.0.1:9001',
                'NB_TLS_CERT_FILE':'/etc/netgo/tls/relay.crt','NB_TLS_KEY_FILE':'/etc/netgo/tls/relay.key','NB_LOG_LEVEL':'info'}
    if set(env) != set(expected) | {'NB_AUTH_SECRET'} or any(env[k] != v for k,v in expected.items()) or not re.fullmatch('[0-9a-f]{64}', env['NB_AUTH_SECRET']): raise ValueError('Unsupported relay configuration')
    if aux['mode'] == '443' and not {'auxiliary/haproxy.cfg','auxiliary/relay-port.nft'} <= files.keys(): raise ValueError('443 mode requires HAProxy and QUIC redirect')
    if aux['mode'] == 'diagnostic' and {'auxiliary/haproxy.cfg','auxiliary/relay-port.nft'} & files.keys(): raise ValueError('Unexpected 443 configuration')
    if aux['mode'] == '443':
        expected_haproxy = haproxy_config(aux,uri.hostname)
        if (folder / 'auxiliary/haproxy.cfg').read_text() != expected_haproxy: raise ValueError('Unsupported HAProxy configuration')
        expected_nft = 'add table inet netgo_relay { comment "NetGo relay redirect v1"; }\nflush table inet netgo_relay\nadd chain inet netgo_relay prerouting { type nat hook prerouting priority dstnat; policy accept; }\nadd rule inet netgo_relay prerouting udp dport ' + str(aux['relay_port']) + ' redirect to :' + str(aux['relay_bind_port']) + '\n'
        if (folder / 'auxiliary/relay-port.nft').read_text() != expected_nft: raise ValueError('Unsupported redirect configuration')
    validate_tls(folder, uri.hostname, aux['tunnel_host'])
    return data

def haproxy_config(a, relay_host):
    return f'''global
 maxconn 4096
 user haproxy
 group haproxy
 log stdout format raw local0
defaults
 mode tcp
 timeout connect 5s
 timeout client 1h
 timeout server 1h
frontend netgo_tls
 bind :443
 tcp-request inspect-delay 5s
 tcp-request content accept if {{ req.ssl_hello_type 1 }}
 use_backend control if {{ req.ssl_sni -i {a['control_host']} }}
 use_backend relay if {{ req.ssl_sni -i {relay_host} }}
 use_backend tunnel if {{ req.ssl_sni -i {a['tunnel_host']} }}
 default_backend reject_unknown
backend control
 server frp 127.0.0.1:{a['control_backend_port']}
backend relay
 server local 127.0.0.1:{a['relay_bind_port']}
backend tunnel
 server frp 127.0.0.1:{a['tunnel_bind_port']}
backend reject_unknown
 tcp-request content reject
'''

def validate_tls(folder, relay_host, tunnel_host):
    from cryptography import x509
    from cryptography.hazmat.primitives import serialization
    now = datetime.now(timezone.utc)
    for name, host in (('relay', relay_host), ('tunnel', tunnel_host)):
        cert = x509.load_pem_x509_certificate((folder / ('tls/' + name + '.crt')).read_bytes())
        key = serialization.load_pem_private_key((folder / ('tls/' + name + '.key')).read_bytes(), password=None)
        public = lambda k:k.public_bytes(serialization.Encoding.DER, serialization.PublicFormat.SubjectPublicKeyInfo)
        start = cert.not_valid_before_utc if hasattr(cert,'not_valid_before_utc') else cert.not_valid_before.replace(tzinfo=timezone.utc)
        end = cert.not_valid_after_utc if hasattr(cert,'not_valid_after_utc') else cert.not_valid_after.replace(tzinfo=timezone.utc)
        if public(key.public_key()) != public(cert.public_key()) or not start <= now < end: raise ValueError('TLS key or validity mismatch: ' + name)
        san = cert.extensions.get_extension_for_class(x509.SubjectAlternativeName).value
        try: matched = ipaddress.ip_address(host) in san.get_values_for_type(x509.IPAddress)
        except ValueError:
            host = host.lower(); names = san.get_values_for_type(x509.DNSName)
            matched = any(host == n.lower() or (n.startswith('*.') and host.split('.',1)[-1] == n[2:].lower()) for n in names)
        if not matched: raise ValueError('TLS SAN mismatch: ' + name)

def listeners(data):
    a = data['auxiliary']
    tcp = {a['tunnel_bind_port'], a['relay_bind_port'], a['control_backend_port'], 9001, 9091}
    if a['mode'] == '443': tcp.add(443)
    return sorted(tcp), sorted({a['relay_port'], a['relay_bind_port'], a['stun_port']})

def check_ports(data):
    tcp, udp = listeners(data)
    for kind, ports in ((socket.SOCK_STREAM,tcp),(socket.SOCK_DGRAM,udp)):
        for number in ports:
            for family, addr in ((socket.AF_INET,'0.0.0.0'),(socket.AF_INET6,'::')):
                with socket.socket(family, kind) as probe:
                    if kind == socket.SOCK_STREAM and os.name != 'nt': probe.setsockopt(socket.SOL_SOCKET,socket.SO_REUSEADDR,1)
                    elif os.name == 'nt': probe.setsockopt(socket.SOL_SOCKET,socket.SO_EXCLUSIVEADDRUSE,1)
                    if family == socket.AF_INET6: probe.setsockopt(socket.IPPROTO_IPV6, socket.IPV6_V6ONLY, 1)
                    try: probe.bind((addr,number))
                    except OSError as error:
                        if error.errno in (97,99): continue
                        raise ValueError('Port already in use: ' + str(number)) from error

def unit(name, private_ca=False):
    common = '[Unit]\nDescription=NetGo VPS ' + name + '\nAfter=network-online.target\nWants=network-online.target\n\n[Service]\n'
    commands = {'frps':f'ExecStart={BASE}/bin/frps -c {ETC}/frps.toml\n',
                'relay':f'EnvironmentFile={ETC}/relay.env\nExecStart={BASE}/bin/netbird-relay\n',
                'haproxy':f'ExecStart=/usr/sbin/haproxy -W -db -f {ETC}/haproxy.cfg\n'}
    if private_ca:
        commands['relay'] = f'Environment=SSL_CERT_FILE={ETC}/tls/ca.crt\n' + commands['relay']
    if name == 'redirect':
        service = f'Type=oneshot\nRemainAfterExit=yes\nExecStart=/usr/sbin/nft -f {ETC}/relay-port.nft\nExecStop=/usr/sbin/nft delete table inet netgo_vps_relay\n'
    else:
        service = 'User=netgo-vps\nGroup=netgo-vps\n' + commands[name] + 'Restart=on-failure\nRestartSec=3\nNoNewPrivileges=yes\nProtectSystem=strict\nProtectHome=yes\nPrivateTmp=yes\nAmbientCapabilities=CAP_NET_BIND_SERVICE\nCapabilityBoundingSet=CAP_NET_BIND_SERVICE\n'
    return MARKER + common + service + '\n[Install]\nWantedBy=multi-user.target\n'

def verify_owned(state):
    if state.get('owner') != OWNER: raise ValueError('Ownership marker missing')
    if state.get('status') not in ('installed','active','uninstalled') or set(state['units']) - {'netgo-vps-' + n for n in NAMES}: raise ValueError('Invalid installation state')
    allowed = {str(BASE / 'bin' / n) for n in ('frps','netbird-relay')} | {str(BASE / 'maintenance' / n) for n in ('install-vps.sh','scripts/vps_setup.py')}
    allowed |= {str(UNIT_DIR / ('netgo-vps-' + n + '.service')) for n in NAMES}
    for path,digest in state['files'].items():
        if path not in allowed or sha(safe(path)) != digest: raise ValueError('Managed file changed: ' + path)
    if state.get('config'):
        allowed_config = {'frps.toml','relay.env','haproxy.cfg','relay-port.nft','vps.json'} | {n for n in TLS}
        for name,digest in state['config'].items():
            if name not in allowed_config or sha(safe(ETC / name)) != digest: raise ValueError('Managed configuration changed: ' + name)
    link = LINK
    if link.is_symlink() and os.readlink(link) == str(BASE / 'maintenance/install-vps.sh'): return
    if state['status'] != 'uninstalled' or link.exists(): raise ValueError('Unrecognized maintenance command')

def install(source):
    source = safe(Path(source).absolute()); state_path = BASE / 'install.json'
    safe(state_path)
    if state_path.exists():
        state = json.loads(state_path.read_text()); verify_owned(state)
        for name in ('frps','netbird-relay'):
            if sha(source / 'dist/vps/bin' / name) != sha(BASE / 'bin' / name): raise ValueError('Different installed version; review upgrade first')
        for name in ('install-vps.sh','scripts/vps_setup.py'):
            if sha(source / name) != sha(BASE / 'maintenance' / name): raise ValueError('Different maintenance version; review upgrade first')
        if state['status'] == 'uninstalled':
            for name in state['units']:
                path = UNIT_DIR / (name + '.service')
                if path.exists(): raise ValueError('Existing unit: ' + name)
                write(path, unit(name.removeprefix('netgo-vps-'), 'tls/ca.crt' in state['config']), 0o644); state['files'][str(path)] = sha(path)
            LINK.symlink_to(BASE / 'maintenance/install-vps.sh')
            state['status'] = 'installed'; save(state); run(['systemctl','daemon-reload'])
        return state
    for path in (BASE, ETC, LINK):
        safe(path)
        if path == BASE and path.is_dir() and set(p.name for p in path.iterdir()) == {'.install.lock'}: continue
        if path.exists(): raise ValueError('Existing unowned installation path: ' + str(path))
    for n in NAMES:
        path = UNIT_DIR / ('netgo-vps-' + n + '.service')
        if path.exists() or path.is_symlink(): raise ValueError('Existing unit: ' + str(path))
    created = []; committed = False
    try:
        BASE.mkdir(mode=0o755, exist_ok=True); BASE.chmod(0o755)
        (BASE / 'bin').mkdir(mode=0o755); (BASE / 'bin').chmod(0o755)
        for relative in ('bin/frps','bin/netbird-relay'):
            src = source / 'dist/vps' / relative
            raw = safe(src).read_bytes()
            if raw[:5] != b'\x7fELF\x02' or raw[18:20] != b'\x3e\x00': raise ValueError('Native Linux amd64 binary required')
            target = BASE / relative; write(target, raw, 0o755); created.append(target)
        for name in ('install-vps.sh','scripts/vps_setup.py'):
            target = BASE / 'maintenance' / name; write(target, (source / name).read_bytes(), 0o755 if name.endswith('.sh') else 0o644); created.append(target)
        import pwd, grp
        try:
            user = pwd.getpwnam('netgo-vps')
            if user.pw_uid >= 1000 or user.pw_shell not in ('/usr/sbin/nologin','/bin/false'): raise ValueError('Existing non-system netgo-vps account')
        except KeyError:
            run(['useradd','--system','--user-group','--no-create-home','--home-dir','/nonexistent','--shell','/usr/sbin/nologin','netgo-vps'])
        ETC.mkdir(mode=0o750); ETC.chmod(0o750); os.chown(ETC, 0, grp.getgrnam('netgo-vps').gr_gid)
        state = {'owner':OWNER,'status':'installed','files':{str(p):sha(p) for p in created},'units':[], 'config':{}}
        link = LINK; link.symlink_to(BASE / 'maintenance/install-vps.sh'); created.append(link)
        save(state); committed = True
        return state
    finally:
        if not committed:
            for path in reversed(created): path.unlink(missing_ok=True)

def save(state): write(BASE / 'install.json', json.dumps(state, indent=2) + '\n')

def configure(state, folder):
    verify_owned(state); bundle = load_bundle(folder)
    if state['status'] == 'active':
        if state.get('bundle_sha256') != sha(Path(folder) / 'vps.json'): raise ValueError('Different active deployment; review replacement first')
        doctor(state); return
    check_ports(bundle)
    import grp
    gid = grp.getgrnam('netgo-vps').gr_gid
    for name in bundle['files']:
        target_name = name.removeprefix('auxiliary/')
        target = safe(ETC / target_name)
        raw = (Path(folder) / name).read_bytes()
        if name.startswith('auxiliary/'):
            text = raw.decode().replace('/etc/netgo/tls/', str(ETC / 'tls') + '/')
            if name.endswith('.nft'): text = text.replace('netgo_relay','netgo_vps_relay').replace('NetGo relay redirect v1',OWNER + ' relay redirect')
            if name.endswith('haproxy.cfg'): text = text.replace(' user haproxy\n','').replace(' group haproxy\n','')
            raw = text.encode()
        if target.exists() and (target_name not in state['config'] or sha(target) != state['config'][target_name]): raise ValueError('Existing unowned configuration: ' + target_name)
        write(target, raw, 0o640); os.chown(target, 0, gid); os.chown(target.parent,0,gid); target.parent.chmod(0o750)
        state['config'][target_name] = sha(target); save(state)
    target = ETC / 'vps.json'; write(target, (Path(folder) / 'vps.json').read_bytes()); state['config']['vps.json'] = sha(target)
    run([BASE / 'bin/frps','verify','-c',ETC / 'frps.toml'])
    names = ['frps','relay']
    if bundle['auxiliary']['mode'] == '443':
        # HAProxy is a distribution package; its own service is never enabled here.
        run(['/usr/sbin/haproxy','-c','-f',ETC / 'haproxy.cfg'])
        result = subprocess.run(['/usr/sbin/nft','list','table','inet','netgo_vps_relay'], capture_output=True)
        if result.returncode == 0: raise ValueError('Existing redirect table; inspect its ownership before activation')
        run(['/usr/sbin/nft','-c','-f',ETC / 'relay-port.nft']); names += ['redirect','haproxy']
    for name in names:
        path = UNIT_DIR / ('netgo-vps-' + name + '.service')
        if path.exists() and str(path) not in state['files']: raise ValueError('Existing unowned unit')
        write(path, unit(name, 'tls/ca.crt' in state['config']), 0o644); state['files'][str(path)] = sha(path)
    state['units'] = ['netgo-vps-' + n for n in names]; state['bundle_sha256'] = sha(Path(folder) / 'vps.json'); save(state)
    run(['systemctl','daemon-reload'])
    try:
        for name in state['units']: run(['systemctl','enable','--now',name + '.service'])
        for _ in range(20):
            try: doctor(state); break
            except (RuntimeError,OSError): time.sleep(1)
        else: raise RuntimeError('VPS health check failed')
        state['status'] = 'active'; save(state)
    except BaseException:
        for name in reversed(state['units']): subprocess.run(['systemctl','disable','--now',name + '.service'],capture_output=True)
        state['status'] = 'installed'; save(state)
        raise

def doctor(state):
    verify_owned(state)
    if not state['units']: raise RuntimeError('VPS programs installed; configure the exported bundle')
    for name in state['units']: run(['systemctl','is-active','--quiet',name + '.service'])
    data = json.loads((ETC / 'vps.json').read_text()); a = data['auxiliary']
    for number in (a['tunnel_bind_port'], a['relay_bind_port']):
        with socket.create_connection(('127.0.0.1',number),timeout=2): pass
    # Proxy environment must not redirect the local health check.
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
    with opener.open('http://127.0.0.1:9001/health',timeout=2) as response:
        if response.status != 200: raise RuntimeError('Relay health failed')
    print('All managed VPS services and local health checks passed')

def uninstall(state):
    if state['status'] == 'uninstalled': print('Already uninstalled; configuration retained'); return
    verify_owned(state)
    for name in reversed(state['units']): run(['systemctl','disable','--now',name + '.service'])
    for name in state['units']:
        path = UNIT_DIR / (name + '.service'); path.unlink(); state['files'].pop(str(path))
    LINK.unlink(); run(['systemctl','daemon-reload'])
    state['status'] = 'uninstalled'; save(state)
    print('Owned VPS services removed; certificates, configuration, binaries and account retained')

def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('action', choices=('install','configure','status','doctor','uninstall'))
    p.add_argument('--source', type=Path); p.add_argument('--config-dir', type=Path)
    a = p.parse_args()
    if a.action == 'status':
        if not (BASE / 'install.json').exists(): print('NetGo VPS not installed'); return
        state = json.loads((BASE / 'install.json').read_text()); print('NetGo VPS:', state['status'])
        if state['status'] == 'active': doctor(state)
        return
    if os.geteuid() != 0 or not Path('/run/systemd/system').is_dir(): raise ValueError('Root on Linux with running systemd required')
    safe(BASE); safe(ETC); BASE.mkdir(mode=0o755, exist_ok=True)
    import fcntl
    with safe(BASE / '.install.lock').open('a') as lock:
        fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        if a.action == 'install':
            state = install(a.source)
            if a.config_dir: configure(state,a.config_dir)
        else:
            state = json.loads((BASE / 'install.json').read_text())
            if a.action == 'configure': configure(state,a.config_dir)
            elif a.action == 'doctor': doctor(state)
            elif a.action == 'uninstall': uninstall(state)

if __name__ == '__main__':
    try: main()
    except Exception as error: print(str(error), file=sys.stderr); sys.exit(1)
