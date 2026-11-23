#!/usr/bin/env python3
"""Initialize a locally owned NetGo server through the supported NetBird APIs."""
import argparse
from datetime import datetime, timedelta, timezone
import getpass
import hashlib
import ipaddress
import json
import os
from pathlib import Path
import re
import shutil
import socket
import ssl
import subprocess
import sys
import time
import urllib.error
import urllib.request
from urllib.parse import quote, urlsplit

from deploy_config import configuration, hostname, read_json, render

ETC = Path('/etc/netgo')
STATE = ETC / 'setup-state.json'
CTL = '/opt/netgo/current/netgoctl'
CORE = '/opt/netgo/upstream/netbird'
UNITS = ('netbird-server', 'netbird-relay', 'netgo-caddy', 'netgo-helper', 'netgo-router', 'netgo-server')
FORWARD_FILE = Path('/etc/sysctl.d/90-netgo-forwarding.conf')
FORWARD_CONTENT = '# Managed by NetGo v1\nnet.ipv4.ip_forward=1\n'


def run(args, input=None, timeout=60):
    env = dict(os.environ, NB_STATE_DIR='/var/lib/netgo-router') if args and args[0] == CORE else None
    result = subprocess.run(args, input=input, capture_output=True, text=True, timeout=timeout, env=env)
    if result.returncode:
        # Upstream errors may echo request arguments or credentials. Keep output private.
        if ETC.is_dir():
            write(ETC/'setup-command-error.txt', result.stdout + result.stderr, group=False)
        raise RuntimeError(Path(args[0]).name + ' failed (exit ' + str(result.returncode) + '); see /etc/netgo/setup-command-error.txt')
    return result.stdout.strip()


def write(path, value, group=True):
    path = Path(path)
    path.parent.mkdir(mode=0o750, parents=True, exist_ok=True)
    if path.is_symlink() or path.parent.is_symlink():
        raise ValueError('refusing a symlink configuration path')
    temporary = path.with_name(path.name + '.new')
    fd = os.open(temporary, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o640 if group else 0o600)
    try:
        os.fchmod(fd, 0o640 if group else 0o600)
        with os.fdopen(fd, 'w', encoding='utf-8') as stream:
            stream.write(value)
            stream.flush()
            os.fsync(stream.fileno())
        if group:
            import grp
            os.chown(temporary, 0, grp.getgrnam('netgo').gr_gid)
        temporary.replace(path)
    finally:
        if temporary.exists():
            temporary.unlink()


def save(state):
    write(STATE, json.dumps(state, indent=2) + '\n', group=False)


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


def api(config, path, method='GET', body=None, token=None):
    request = urllib.request.Request(config['ManagementURL'] + '/api' + path,
        data=None if body is None else json.dumps(body).encode(), method=method)
    request.add_header('Content-Type', 'application/json')
    if token:
        request.add_header('Authorization', 'Token ' + token)
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}), NoRedirect())
    try:
        with opener.open(request, timeout=20) as response:
            data = response.read(2*1024*1024)
            return json.loads(data) if data else None
    except urllib.error.HTTPError as error:
        write(ETC/'setup-http-error.txt',error.read(16384).decode('utf-8',errors='replace'),group=False)
        raise RuntimeError('Management ' + method + ' ' + path + ': HTTP ' + str(error.code)) from None


def wait_for(check, description, seconds=60):
    end = time.monotonic() + seconds
    while True:
        try:
            result = check()
            if result:
                return result
        except (OSError, ValueError, RuntimeError, urllib.error.URLError):
            pass
        if time.monotonic() >= end:
            raise RuntimeError('Timed out waiting for ' + description)
        time.sleep(1)


def prompt_config():
    if not sys.stdin.isatty():
        raise ValueError('noninteractive setup requires --config, --admin-email and --password-stdin')
    host = hostname(input('控制域名或服务器 IP: ').strip())
    relay = hostname(input('Relay 域名/IP [' + host + ']: ').strip() or host)
    routes = json.loads(run(['ip', '-j', 'route', 'show', 'default']))
    default_if = routes[0].get('dev', '') if routes else ''
    interface = input('LAN 网卡 [' + default_if + ']: ').strip() or default_if
    addresses = json.loads(run(['ip', '-j', 'address', 'show', 'dev', interface]))
    cidrs = [str(ipaddress.ip_network(info['local']+'/'+str(info['prefixlen']), strict=False))
             for row in addresses for info in row.get('addr_info', []) if info['family']=='inet']
    network = input('LAN 网段 [' + ','.join(cidrs) + ']: ').strip() or ','.join(cidrs)
    site_id = input('站点 ID [site1]: ').strip() or 'site1'
    return {'Site': {'Protocol':1, 'ConfigRevision':1, 'ID':site_id, 'Name':'NetGo '+site_id,
        'Origin':'https://'+host+':10443', 'Candidates':[{'ID':'local','Address':host+':10443','Kind':'direct'}],
        'CA':'', 'GatewayPeerID':'', 'GatewayIP':'', 'LocalRelay':relay, 'AuxiliaryRelay':''},
        'Listen':'127.0.0.1:18081', 'Database':'/var/lib/netgo/netgo.db', 'ManagementURL':'http://127.0.0.1:18080',
        'ManagementTokenFile':'/etc/netgo/management.token', 'NativeSecretFile':'/etc/netgo/native.secret',
        'HelperSocket':'/run/netgo-helper/helper.sock', 'Interface':'wt0', 'LANInterface':interface,
        'OverlayCIDR':'100.96.0.0/16', 'LocalCIDRs':[item.strip() for item in network.split(',')],
        'LeaseSeconds':120, 'Auxiliaries':[], 'Deployment': {'Mode':'diagnostic','LocalRelayPort':18443,'LocalSTUNPort':3478,
        'ManagementHealthPort':19000,'ManagementMetricsPort':19090,'RelayHealthPort':19001,'RelayMetricsPort':19091,'CaddyAdminPort':12019}}


def validate_setup(config):
    values, manifest = configuration(config)
    if config.get('Auxiliaries') or manifest['local']['mode'] != 'diagnostic':
        raise ValueError('first-time setup uses the local diagnostic template; add the optional VPS/443 template after baseline verification')
    fixed = {'Database':'/var/lib/netgo/netgo.db', 'ManagementTokenFile':'/etc/netgo/management.token',
             'NativeSecretFile':'/etc/netgo/native.secret', 'HelperSocket':'/run/netgo-helper/helper.sock'}
    if any(config.get(key) != value for key, value in fixed.items()):
        raise ValueError('one-command setup requires the managed database and secret paths')
    if not re.fullmatch(r'[a-zA-Z0-9][a-zA-Z0-9_.-]{0,14}', config.get('LANInterface', '')):
        raise ValueError('invalid LAN interface')
    if not re.fullmatch(r'[a-zA-Z0-9][a-zA-Z0-9_.-]{0,14}', config.get('Interface', '')):
        raise ValueError('invalid tunnel interface')
    if config['Interface'] == config['LANInterface']:
        raise ValueError('LAN and tunnel interfaces must differ')
    overlay = ipaddress.ip_network(config['OverlayCIDR'])
    if overlay.version != 4 or not 8 <= overlay.prefixlen <= 24:
        raise ValueError('overlay must be an explicit IPv4 pool with prefix length 8..24')
    for item in config.get('LocalCIDRs', []):
        network = ipaddress.ip_network(item)
        if network.version != 4 or network.overlaps(overlay):
            raise ValueError('LAN overlaps the overlay or uses unsupported IPv6')
    if not config.get('LocalCIDRs'):
        raise ValueError('at least one explicit LAN network is required')
    return values, manifest


def check_ports(config, manifest):
    local = manifest['local']
    tcp = [local['control_backend_port'],local['relay_bind_port'],urlsplit(config['ManagementURL']).port,
           urlsplit('http://'+config['Listen']).port,*local['internal_ports'].values()]
    udp = [local['relay_bind_port'],local['stun_port'],51820]
    for kind, ports in ((socket.SOCK_STREAM,tcp),(socket.SOCK_DGRAM,udp)):
        for port in ports:
            with socket.socket(socket.AF_INET,kind) as sock:
                try:
                    sock.bind(('0.0.0.0',port))
                except OSError:
                    raise ValueError(('TCP ' if kind==socket.SOCK_STREAM else 'UDP ')+str(port)+' is already in use; choose different configuration ports') from None


def certificates(config, supplied, private_ca):
    tls = ETC/'tls'
    if tls.exists():
        raise ValueError('TLS directory already exists; use --resume after inspecting the setup journal')
    tls.mkdir(mode=0o750)
    tls.chmod(0o750)
    import grp
    os.chown(tls,0,grp.getgrnam('netgo').gr_gid)
    hosts = {'control':urlsplit(config['Site']['Origin']).hostname, 'relay':config['Site']['LocalRelay']}
    if private_ca:
        run(['openssl','req','-x509','-newkey','rsa:3072','-nodes','-keyout',str(tls/'ca.key'),'-out',str(tls/'ca.crt'),
             '-days','3650','-subj','/CN=NetGo '+config['Site']['ID']+' private CA','-addext','basicConstraints=critical,CA:TRUE',
             '-addext','keyUsage=critical,keyCertSign,cRLSign'])
        (tls/'ca.key').chmod(0o600)
        for role, host in hosts.items():
            try:
                ipaddress.ip_address(host)
                san = 'IP:'+host
            except ValueError:
                san = 'DNS:'+host
            extensions=tls/(role+'.extensions')
            write(extensions,'basicConstraints=critical,CA:FALSE\nkeyUsage=critical,digitalSignature,keyEncipherment\nextendedKeyUsage=serverAuth\nsubjectAltName='+san+'\n',group=False)
            run(['openssl','req','-new','-newkey','rsa:3072','-nodes','-keyout',str(tls/(role+'.key')),
                 '-out',str(tls/(role+'.csr')),'-subj','/CN='+host])
            run(['openssl','x509','-req','-in',str(tls/(role+'.csr')),'-CA',str(tls/'ca.crt'),'-CAkey',str(tls/'ca.key'),
                 '-CAcreateserial','-days','365','-out',str(tls/(role+'.crt')),'-extfile',str(extensions)])
            (tls/(role+'.csr')).unlink()
            extensions.unlink()
        config['Site']['CA']=(tls/'ca.crt').read_text()
    else:
        if not supplied:
            raise ValueError('provide --tls-dir with control.crt/key and relay.crt/key, or explicitly choose --private-ca')
        for role in hosts:
            for suffix in ('.crt','.key'):
                path=Path(supplied)/(role+suffix)
                if not path.is_file() or path.is_symlink():
                    raise ValueError('missing regular certificate/key: '+path.name)
                shutil.copyfile(path,tls/path.name)
        ca=Path(supplied)/'ca.crt'
        if ca.is_file():
            shutil.copyfile(ca,tls/'ca.crt')
            config['Site']['CA']=ca.read_text()
        else:
            config['Site']['CA']=''
    roots=Path('/etc/ssl/certs/ca-certificates.crt').read_text()
    write(tls/'trust.pem',roots+'\n'+config['Site']['CA'])
    for role,host in hosts.items():
        cert,key=tls/(role+'.crt'),tls/(role+'.key')
        run(['openssl','x509','-in',str(cert),'-checkend','86400','-noout'])
        try:
            ipaddress.ip_address(host); flag='-checkip'
        except ValueError:
            flag='-checkhost'
        run(['openssl','x509','-in',str(cert),flag,host,'-noout'])
        if run(['openssl','x509','-in',str(cert),'-pubkey','-noout']) != run(['openssl','pkey','-in',str(key),'-pubout','-passin','pass:']):
            raise ValueError('certificate/key mismatch: '+role)
        run(['openssl','verify','-CAfile',str(tls/'trust.pem'),str(cert)])
        for path in (cert,key):
            os.chown(path,0,grp.getgrnam('netgo').gr_gid); path.chmod(0o640)


def forward_on():
    path=ETC/'network-state.json'
    if not path.exists():
        previous=run(['sysctl','-n','net.ipv4.ip_forward'])
        if FORWARD_FILE.exists():
            raise ValueError('forwarding configuration exists without a NetGo ownership record')
        write(path,json.dumps({'schema':1,'owner':'NetGo v1','ip_forward':previous,'created_sysctl':previous=='0'})+'\n',group=False)
    saved=json.loads(path.read_text())
    if saved['created_sysctl']:
        write(FORWARD_FILE,FORWARD_CONTENT,group=False)
    run(['sysctl','-w','net.ipv4.ip_forward=1'])


def restore_forwarding():
    path=ETC/'network-state.json'
    if not path.exists():
        return
    state=json.loads(path.read_text())
    if state.get('owner')!='NetGo v1' or state.get('ip_forward') not in ('0','1'):
        raise ValueError('invalid forwarding ownership record')
    if state['created_sysctl']:
        if FORWARD_FILE.exists():
            if FORWARD_FILE.is_symlink() or FORWARD_FILE.read_text()!=FORWARD_CONTENT:
                raise ValueError('forwarding file changed outside NetGo; refusing to remove it')
            FORWARD_FILE.unlink()
        run(['sysctl','-w','net.ipv4.ip_forward='+state['ip_forward']])


def health(config):
    roots=ssl.create_default_context(cafile=str(ETC/'tls/trust.pem'))
    opener=urllib.request.build_opener(urllib.request.ProxyHandler({}),NoRedirect(),urllib.request.HTTPSHandler(context=roots))
    with opener.open(config['Site']['Origin']+'/netgo/v1/health',timeout=5) as response:
        data=json.load(response)
        return response.status==200 and data


def runtime_permissions():
    import grp
    gid=grp.getgrnam('netgo').gr_gid
    for path in (ETC,ETC/'tls'):
        if path.is_symlink() or path.stat().st_uid!=0:
            raise ValueError('runtime configuration directory is not owned by root')
        os.chown(path,0,gid);path.chmod(0o750)
    paths=[ETC/name for name in ('site.json','netbird.yaml','relay.env','Caddyfile','native.secret','management.token')]
    paths.extend(ETC/'tls'/name for name in ('control.crt','control.key','relay.crt','relay.key','trust.pem'))
    for path in paths:
        if not path.exists():continue
        if path.is_symlink() or not path.is_file() or path.stat().st_uid!=0 or path.stat().st_nlink!=1:
            raise ValueError('unowned runtime configuration file')
        os.chown(path,0,gid);path.chmod(0o640)


def disabled_default_policy(policy):
    rules=[]
    for rule in policy.get('rules',[]):
        out={key:rule[key] for key in ('name','description','enabled','action','bidirectional','protocol','ports','port_ranges') if key in rule}
        for key in ('sources','destinations'):
            out[key]=[item if isinstance(item,str) else item['id'] for item in rule.get(key,[])]
        rules.append(out)
    if not rules:raise ValueError('initial default policy has no rules')
    return {'name':policy['name'],'description':policy.get('description',''),'enabled':False,'rules':rules}


def connect_router(config, state):
    # Only an already enrolled, owned routing peer may reconnect automatically.
    # Bootstrap still uses the unreachable placeholder with auto-connect off.
    if not state.get('router_id') or not state.get('router_ip'):
        raise ValueError('trusted router identity missing')
    run([CORE,'--config','/var/lib/netgo-router/config.json','--daemon-addr','unix:///run/netgo-router/netbird.sock',
         'up','--management-url',config['Site']['Origin'],'--disable-auto-connect=false'],timeout=120)
    token=(ETC/'management.token').read_text().strip()
    def ready():
        peer=api(config,'/peers/'+quote(state['router_id'],safe=''),token=token)
        return peer.get('connected') and peer.get('ip')==state['router_ip']
    wait_for(ready,'enrolled routing peer connection')


def initialize(args):
    if not Path('/opt/netgo/install.manifest').is_file():
        raise ValueError('owned program installation required before configuration')
    if STATE.exists():
        state=json.loads(STATE.read_text())
        if state.get('owner')!='NetGo v1':
            raise ValueError('unknown setup journal owner')
        config,_=read_json(ETC/'site.json')
        if state.get('complete'):
            runtime_permissions()
            forward_on()
            run(['systemctl','enable','--now',*UNITS],timeout=120)
            wait_for(lambda:health(config),'HTTPS product health')
            connect_router(config,state)
            print('Existing site started; identities, passwords and configuration retained.')
            return
        if not args.resume:
            raise ValueError('interrupted setup found; inspect /etc/netgo/setup-state.json, then configure --resume')
    else:
        if (ETC/'site.json').exists():
            raise ValueError('existing site has no setup journal; refusing to overwrite it')
        config,_=read_json(args.config) if args.config else (prompt_config(),None)
        values,manifest=validate_setup(config)
        if not Path('/sys/class/net',config['LANInterface']).is_dir():
            raise ValueError('LAN interface not found')
        if Path('/sys/class/net',config['Interface']).exists():
            raise ValueError('tunnel interface already exists; refusing to take ownership')
        check_ports(config,manifest)
        if not args.tls_dir and not args.private_ca:
            if not sys.stdin.isatty():
                raise ValueError('choose --tls-dir or --private-ca')
            answer=input('证书目录（control.crt/key、relay.crt/key；输入 private-ca 生成实验室 CA）: ').strip()
            if answer=='private-ca': args.private_ca=True
            else: args.tls_dir=Path(answer)
        args.admin_email=args.admin_email or (input('管理员邮箱: ').strip() if sys.stdin.isatty() else '')
        if not re.fullmatch(r'[^\s@]+@[^\s@]+',args.admin_email):
            raise ValueError('valid --admin-email required')
        if not re.fullmatch(r'[a-zA-Z0-9][a-zA-Z0-9_.-]{0,62}',args.admin_alias):
            raise ValueError('invalid administrator alias')
        state={'schema':1,'owner':'NetGo v1','complete':False,'phase':'prepare','admin_email':args.admin_email,'admin_alias':args.admin_alias}
        # Record ownership before the first potentially partial operation.
        write(ETC/'site.json',json.dumps(config,indent=2)+'\n')
        save(state)
        certificates(config,args.tls_dir,args.private_ca)
        write(ETC/'site.json',json.dumps(config,indent=2)+'\n')
        render(ETC/'site.json',ETC/'rendered')
        for name in ('netbird.yaml','relay.env','Caddyfile','native.secret'):
            write(ETC/name,(ETC/'rendered/local'/name).read_text())
        state['phase']='configured'; save(state)
    if state['phase']=='prepare':
        raise ValueError('certificate preparation was interrupted; retain the protected directory and repair it before resuming')
    validate_setup(config)
    runtime_permissions()
    run([CTL,'config','validate','--config',str(ETC/'site.json')])
    run(['runuser','-u','netgo','--','/opt/netgo/upstream/caddy','validate','--config',str(ETC/'Caddyfile'),'--adapter','caddyfile'])
    password=None
    if not state.get('admin_id'):
        password=sys.stdin.readline().rstrip('\r\n') if args.password_stdin else getpass.getpass('管理员密码: ')
        if not 8 <= len(password.encode()) <= 72 or '\x00' in password:
            raise ValueError('password must be 8..72 UTF-8 bytes')
        if not args.password_stdin and getpass.getpass('再次输入密码: ')!=password:
            raise ValueError('passwords differ')
    try:
        print('Starting private identity and TLS services...',flush=True)
        write(ETC/'bootstrap.env','NB_SETUP_PAT_ENABLED='+('true' if not state.get('admin_id') else 'false')+'\n',group=False)
        run(['systemctl','start','netbird-server','netgo-caddy','netbird-relay'],timeout=90)
        wait_for(lambda:api(config,'/instance'),'identity setup API')
        if not state.get('admin_id'):
            if not api(config,'/instance').get('setup_required'):
                raise ValueError('identity already initialized without a saved NetGo administrator; manual recovery required')
            created=api(config,'/setup','POST',{'email':state['admin_email'],'name':state['admin_alias'],
                        'password':password,'create_pat':True,'pat_expire_in':365})
            password=None
            token=created.get('personal_access_token')
            if not token or not created.get('user_id'):
                raise ValueError('identity bootstrap did not return an administrator and PAT')
            write(ETC/'management.token',token+'\n')
            state.update(admin_id=created['user_id'],phase='identity',pat_expires=(datetime.now(timezone.utc)+timedelta(days=365)).isoformat())
            save(state)
        token=(ETC/'management.token').read_text().strip()
        write(ETC/'bootstrap.env','NB_SETUP_PAT_ENABLED=false\n',group=False)
        for policy in api(config,'/policies',token=token):
            if policy.get('enabled'):
                if state['phase']!='identity' or policy.get('name')!='Default':
                    raise ValueError('unexpected enabled policy; inspect before continuing initialization')
                api(config,'/policies/'+quote(policy['id'],safe=''),'PUT',disabled_default_policy(policy),token)
        accounts=api(config,'/accounts',token=token)
        if len(accounts)!=1:
            raise ValueError('exactly one local account expected')
        settings=accounts[0]['settings']
        settings.update(network_range=config['OverlayCIDR'],ipv6_enabled_groups=[],auto_update_version='disabled')
        api(config,'/accounts/'+quote(accounts[0]['id'],safe=''),'PUT',{'settings':settings},token)
        print('Enrolling the dedicated routing peer...',flush=True)
        router_path=Path('/var/lib/netgo-router/config.json')
        router=json.loads(router_path.read_text())
        if router=={'DisableAutoConnect':True,'ManagementURL':'https://127.0.0.1:1'}:
            router['ManagementURL']={'Scheme':'https','Host':'127.0.0.1:1'}
            write(router_path,json.dumps(router)+'\n',group=False)
        run(['systemctl','start','netgo-helper','netgo-router'],timeout=60)
        wait_for(lambda:Path('/run/netgo-router/netbird.sock').exists(),'router IPC')
        if not state.get('router_id'):
            key=api(config,'/setup-keys','POST',{'name':'netgo-'+config['Site']['ID']+'-router','type':'one-off',
                'expires_in':3600,'auto_groups':[],'usage_limit':1,'ephemeral':False},token)
            keyfile=Path('/run/netgo-router/enrollment.key')
            try:
                write(keyfile,key['key']+'\n',group=False)
                run([CORE,'--config','/var/lib/netgo-router/config.json','--daemon-addr','unix:///run/netgo-router/netbird.sock',
                    'up','--management-url',config['Site']['Origin'],'--setup-key-file',str(keyfile),
                    '--hostname','netgo-'+config['Site']['ID']+'-router','--interface-name',config['Interface'],
                    '--wireguard-port','51820','--disable-dns','--disable-client-routes','--disable-ipv6','--disable-auto-connect=false'],timeout=120)
            finally:
                if keyfile.exists(): keyfile.unlink()
                api(config,'/setup-keys/'+quote(str(key['id']),safe=''),'PUT',{'auto_groups':[],'revoked':True},token)
            peers=api(config,'/peers',token=token)
            peers=[peer for peer in peers if peer.get('name')=='netgo-'+config['Site']['ID']+'-router']
            if len(peers)!=1 or ipaddress.ip_address(peers[0]['ip']) not in ipaddress.ip_network(config['OverlayCIDR']):
                raise ValueError('cannot establish the authoritative router identity and overlay address')
            state['router_id']=peers[0]['id']; state['router_ip']=peers[0]['ip']; state['phase']='router'; save(state)
        connect_router(config,state)
        config['Site']['GatewayPeerID']=state['router_id']; config['Site']['GatewayIP']=state['router_ip']
        write(ETC/'site.json',json.dumps(config,indent=2)+'\n')
        if not state.get('alias_created'):
            run(['runuser','-u','netgo','--',CTL,'init-admin','--config',str(ETC/'site.json'),
                 '--admin-id',state['admin_id'],'--admin-alias',state['admin_alias'],'--admin-email',state['admin_email']])
            state['alias_created']=True; save(state)
        forward_on()
        run(['runuser','-u','netgo','--',CTL,'reconcile','--config',str(ETC/'site.json')])
        run(['systemctl','restart','netbird-server','netgo-helper','netgo-server'],timeout=90)
        wait_for(lambda:health(config),'trusted HTTPS product health')
        run(['systemctl','enable',*UNITS])
        run([CTL,'export-site','--config',str(ETC/'site.json'),'--output',str(ETC/'client-site.json')])
        state.update(complete=True,phase='ready'); save(state)
        print('Site initialized: '+config['Site']['Origin'])
        print('Administrator alias: '+state['admin_alias'])
        print('Client import: /etc/netgo/client-site.json (contains public trust, no password/private key).')
        print('No LAN resource is granted by default. Add explicit resources and permissions in the management UI.')
    except BaseException:
        password=None
        for unit in reversed(UNITS):
            subprocess.run(['systemctl','stop',unit],capture_output=True,timeout=45)
        restore_forwarding()
        state['last_attempt']='failed'; save(state)
        raise


def main():
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--config',type=Path)
    parser.add_argument('--tls-dir',type=Path)
    parser.add_argument('--private-ca',action='store_true')
    parser.add_argument('--admin-email')
    parser.add_argument('--admin-alias',default='admin')
    parser.add_argument('--password-stdin',action='store_true')
    parser.add_argument('--resume',action='store_true')
    parser.add_argument('--restore-forwarding',action='store_true')
    parser.add_argument('--lock-fd',type=int,choices=[9],help=argparse.SUPPRESS)
    args=parser.parse_args()
    if args.tls_dir and args.private_ca:
        parser.error('--tls-dir and --private-ca cannot be combined')
    if sys.platform!='linux' or os.geteuid()!=0:
        parser.error('run as root on Linux')
    import fcntl
    os.umask(0o077)
    lock_path=Path('/opt/netgo/.install.lock')
    if args.lock_fd is None:
        lock=lock_path.open('a')
    else:
        descriptor=os.fstat(args.lock_fd)
        expected=lock_path.stat()
        if (descriptor.st_dev,descriptor.st_ino)!=(expected.st_dev,expected.st_ino):
            parser.error('inherited lifecycle lock does not identify the managed lock file')
        lock=os.fdopen(os.dup(args.lock_fd),'a')
    try:
        fcntl.flock(lock,fcntl.LOCK_EX|fcntl.LOCK_NB)
        if args.restore_forwarding: restore_forwarding()
        else: initialize(args)
    except (OSError,ValueError,RuntimeError,subprocess.SubprocessError,KeyboardInterrupt) as error:
        print('Setup stopped: '+str(error),file=sys.stderr)
        print('Configuration/identity retained. Inspect setup-state.json and the protected error log before configure --resume.',file=sys.stderr)
        return 1
    finally:
        lock.close()
    return 0


if __name__=='__main__':
    raise SystemExit(main())
