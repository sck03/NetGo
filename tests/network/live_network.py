#!/usr/bin/env python3
"""Explicit lab-only real NetBird/TCP/UDP/lease tests using isolated Linux clients."""
import argparse
from datetime import datetime, timezone
import hashlib
import http.client
import ipaddress
import json
import os
from pathlib import Path
import secrets
import selectors
import shutil
import signal
import socket
import subprocess
import sys
import time

from live_api import Session, require

CORE='/opt/netgo/upstream/netbird'
CLIENT='/opt/netgo/current/netgo-client-service'
TARGET='198.19.240.2'
HASH=hashlib.sha256(bytes(range(256))*1024).hexdigest()


def command(args,**kwargs):
    result=subprocess.run(args,capture_output=True,text=True,timeout=kwargs.pop('timeout',30),**kwargs)
    if result.returncode:raise RuntimeError('lab command failed: '+Path(args[0]).name+'; '+result.stderr[:400])
    return result.stdout


class UnixHTTP(http.client.HTTPConnection):
    def __init__(self,path):super().__init__('localhost',timeout=40);self.path=path
    def connect(self):
        self.sock=socket.socket(socket.AF_UNIX,socket.SOCK_STREAM)
        self.sock.settimeout(self.timeout);self.sock.connect(str(self.path))


class Client:
    def __init__(self,label,number,root,profile):
        self.name='netgo-lab-client-'+label;self.link='ngclient'+label
        self.directory=root/('client-'+label);self.directory.mkdir(mode=0o700)
        self.profile=profile;self.created=False;self.link_created=False;self.resolver_created=False;self.processes=[];self.logs=[];self.product=None;self.uplink_rules=[]
        self.core_socket=self.directory/'core.sock';self.socket=self.directory/'client.sock'
        self.network=ipaddress.ip_network('10.253.'+str(number)+'.0/30')
        self.host=str(self.network[1]);self.address=str(self.network[2])
        self.resolver=Path('/etc/netns')/self.name

    def start(self):
        names=command(['ip','netns','list'])
        require(not any(line.split()[0]==self.name for line in names.splitlines()),'lab namespace already exists')
        require(subprocess.run(['ip','link','show',self.link],capture_output=True).returncode!=0,'lab veth already exists')
        for route in json.loads(command(['ip','-j','route','show','table','all'])):
            dst=route.get('dst','default')
            if dst=='default':continue
            try:network=ipaddress.ip_network(dst,strict=False)
            except ValueError:continue
            require(not network.overlaps(self.network),'lab client subnet overlaps host routing')
        require(not self.resolver.exists(),'per-namespace resolver directory already exists')
        self.resolver.mkdir(mode=0o700,parents=True)
        self.resolver_created=True
        shutil.copyfile('/etc/resolv.conf',self.resolver/'resolv.conf')
        command(['ip','netns','add',self.name]);self.created=True
        peer='ngpeer'+self.name[-1]
        command(['ip','link','add',self.link,'type','veth','peer','name',peer])
        self.link_created=True
        command(['ip','link','set',peer,'netns',self.name])
        command(['ip','address','add',self.host+'/30','dev',self.link])
        command(['ip','link','set',self.link,'up'])
        self.exec(['ip','link','set','lo','up'])
        self.exec(['ip','link','set',peer,'name','eth0'])
        self.exec(['ip','address','add',self.address+'/30','dev','eth0'])
        self.exec(['ip','link','set','eth0','up'])
        self.exec(['ip','route','add','default','via',self.host])
        # Without this guard a namespace could reach the target via the host's
        # ordinary forwarding path, producing a false positive without a VPN.
        self.exec(['nft','-f','-'],input='add table inet netgo_lab_transport\nadd chain inet netgo_lab_transport output { type filter hook output priority -200; policy accept; }\nadd rule inet netgo_lab_transport output oifname "eth0" ip daddr 198.19.240.0/30 drop\n')
        (self.directory/'config.json').write_text(json.dumps({'DisableAutoConnect':True,'DisableDNS':True,'ManagementURL':{'Scheme':'https','Host':'127.0.0.1:1'}})+'\n')
        env=dict(os.environ,NB_STATE_DIR=str(self.directory/'core-state'),NB_LAZY_CONN='false')
        # The profile CA must reach management, Signal, WS and QUIC; relying on
        # Linux's SSL_CERT_FILE would hide the Windows service trust defect.
        env.pop('SSL_CERT_FILE',None)
        (self.directory/'no-system-bus').write_bytes(b'')
        self.spawn([CORE,'--config',str(self.directory/'config.json'),'--daemon-addr','unix://'+str(self.core_socket),
                    '--log-file',str(self.directory/'core.log'),'service','run'],'daemon',env)
        self.wait(lambda:self.core_socket.exists(),'core socket')
        self.product=self.spawn([CLIENT,'--root',str(self.directory/'profiles'),'--socket',str(self.socket),'--daemon',str(self.core_socket)],'client',env)
        self.wait(lambda:self.socket.exists(),'client socket')
        self.ipc('import','POST',self.profile)

    def exec(self,args,**kwargs):return command(['ip','netns','exec',self.name,*args],**kwargs)

    def public_uplink(self,address):
        """Give only this isolated client a return path from one public test VPS."""
        public=ipaddress.ip_address(address)
        require(public.version==4 and public.is_global,'explicit public IPv4 VPS required')
        rule=['POSTROUTING','-s',self.address+'/32','-d',str(public)+'/32','-m','comment','--comment',self.name,'-j','MASQUERADE']
        command(['iptables','-t','nat','-I',*rule])
        self.uplink_rules.append(rule)

    def spawn(self,args,name,env):
        log=(self.directory/(name+'.txt')).open('w');self.logs.append(log)
        # ip netns exec binds /etc/netns/<name>/resolv.conf privately. Also hide
        # the host system bus so a root test daemon cannot reconfigure the host
        # resolver or NetworkManager through a shared Unix socket.
        launcher='if [ -S /run/dbus/system_bus_socket ]; then mount --bind "$1" /run/dbus/system_bus_socket || exit; fi; shift; exec "$@"'
        process=subprocess.Popen(['ip','netns','exec',self.name,'unshare','--mount','--propagation','private','--','sh','-c',launcher,
            'netgo-lab',str(self.directory/'no-system-bus'),*args],stdout=log,stderr=subprocess.STDOUT,env=env)
        self.processes.append(process);return process

    def wait(self,test,label,seconds=65):
        deadline=time.monotonic()+seconds
        while time.monotonic()<deadline:
            if test():return
            if any(p.poll() is not None for p in self.processes):raise RuntimeError('lab client process stopped before '+label)
            time.sleep(.5)
        raise RuntimeError('timeout waiting for '+label)

    def ipc(self,path,method='GET',body=None):
        connection=UnixHTTP(self.socket)
        try:
            connection.request(method,'/desktop/'+path,body=None if body is None else json.dumps(body),headers={'Content-Type':'application/json'})
            response=connection.getresponse();value=json.loads(response.read())
            if response.status!=200:raise RuntimeError('client IPC '+path+': '+str(value.get('error')))
            return value
        finally:connection.close()

    def connect(self,alias,password,replace=False,expected=None):
        self.ipc('connect','POST',{'Site':self.profile['ID'],'Alias':alias,'Password':password,'Replace':replace})
        deadline=time.monotonic()+70
        while time.monotonic()<deadline:
            state=self.ipc('status')
            if state['state']=='failed':
                if expected and expected in state.get('error',''):return state
                raise RuntimeError('real client connection failed: '+state.get('error','unknown'))
            if state.get('lease',{}).get('id'):
                require(not expected,'conflicting connection unexpectedly acquired a lease')
                return state
            time.sleep(.5)
        raise RuntimeError('real client connection timed out')

    def probe(self,kind='http',port=18080,success=True,timeout=2):
        code=r"""import hashlib,json,socket,sys,urllib.request
kind,port,timeout=sys.argv[1],int(sys.argv[2]),float(sys.argv[3])
try:
 if kind=='http':
  opener=urllib.request.build_opener(urllib.request.ProxyHandler({}))
  with opener.open('http://198.19.240.2:'+str(port),timeout=timeout) as response:
   data=response.read(); result={'ok':True,'sha256':hashlib.sha256(data).hexdigest(),'source':response.headers.get('X-NetGo-Test-Source')}
 else:
  s=socket.socket(socket.AF_INET,socket.SOCK_DGRAM); s.settimeout(timeout); payload=b'netgo-udp-integrity'*64
  s.sendto(payload,('198.19.240.2',port));reply=s.recv(4096);result={'ok':True,'valid':False};s.close()
  if b'\n' in reply:
   source,data=reply.split(b'\n',1);result.update(valid=data==payload,source=source.decode().removeprefix('netgo-source:'))
except OSError:result={'ok':False}
print(json.dumps(result))
"""
        result=json.loads(self.exec(['python3','-c',code,kind,str(port),str(timeout)],timeout=timeout+6))
        require(result['ok']==success,'unexpected '+kind+' reachability on port '+str(port))
        if success and kind=='http':
            require(result['sha256']==HASH,'LAN payload checksum mismatch')
        if success and kind=='udp':
            require(result.get('valid',False),'LAN UDP target returned an invalid payload/protocol')
        if success:
            require(result['source']=='198.19.240.1','routing peer did not masquerade traffic')
        return result

    def disconnect(self):
        if self.product and self.product.poll() is None:self.ipc('disconnect','POST')

    def close(self):
        try:
            self.disconnect()
            if self.created and self.core_socket.exists():
                self.exec([CORE,'--config',str(self.directory/'config.json'),'--daemon-addr','unix://'+str(self.core_socket),'down'],timeout=15,
                          env=dict(os.environ,NB_STATE_DIR=str(self.directory/'core-state')))
        finally:
            for process in reversed(self.processes):
                if process.poll() is None:
                    process.terminate()
                    try:process.wait(timeout=15)
                    except subprocess.TimeoutExpired:process.kill();process.wait(timeout=5)
            for log in self.logs:log.close()
            for rule in reversed(self.uplink_rules):command(['iptables','-t','nat','-D',*rule])
            self.uplink_rules.clear()
            if self.created:command(['ip','netns','delete',self.name])
            if self.link_created and subprocess.run(['ip','link','show',self.link],capture_output=True).returncode==0:
                command(['ip','link','delete',self.link])
            if self.resolver_created and self.resolver.exists():
                for path in self.resolver.iterdir():
                    if path.name!='resolv.conf':raise RuntimeError('unexpected namespace resolver file retained')
                    path.unlink()
                self.resolver.rmdir()


def main():
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--site',type=Path,required=True)
    parser.add_argument('--alias',required=True)
    parser.add_argument('--password-stdin',action='store_true',required=True)
    parser.add_argument('--output',type=Path,required=True)
    parser.add_argument('--run',action='store_true',required=True)
    args=parser.parse_args()
    if sys.platform!='linux' or os.geteuid()!=0:parser.error('root Linux lab required')
    if args.output.exists():parser.error('use a new output directory')
    args.output.mkdir(mode=0o700,parents=True)
    os.umask(0o077)
    profile=json.loads(args.site.read_text());password=sys.stdin.readline().rstrip('\r\n')
    records=[];clients=[];users=[];resources=[];frozen=False;admin=Session(profile);failed=False
    dns_before=hashlib.sha256(Path('/etc/resolv.conf').read_bytes()).hexdigest()
    def check(name,action):
        record={'name':name,'status':'FAIL'};started=time.monotonic()
        try:
            value=action();record['status']='PASS';return value
        except BaseException as error:record['error']=str(error);raise
        finally:
            record['seconds']=round(time.monotonic()-started,3);records.append(record)
            print(record['status']+' '+name,flush=True)
    def resumed():
        nonlocal frozen
        if frozen:
            command(['systemctl','kill','--kill-whom=main','--signal=CONT','netgo-server','netgo-helper'])
            frozen=False
    def interrupted(signum,frame):raise KeyboardInterrupt('lab test interrupted')
    signal.signal(signal.SIGTERM,interrupted)
    try:
        check('administrator_login',lambda:admin.login(args.alias,password))
        suffix=secrets.token_hex(4)
        def create_user(label):
            result=admin.call('/netgo/v2/admin/users','POST',{'Alias':'vpn'+label+suffix,'Email':'vpn'+label+suffix+'@netgo.invalid','Name':'Isolated VPN client '+label})
            users.append(result)
            require(bool(result.get('initial_password')),'missing local account password')
            return result
        owner=check('create_authorized_user',lambda:create_user('a'))
        outsider=check('create_unprivileged_user',lambda:create_user('b'))
        for protocol,ports in (('tcp',[18080,18083]),('udp',[18081])):
            resource={'id':'vpn'+suffix+protocol,'name':'Isolated '+protocol,'cidr':TARGET+'/32','protocol':protocol,'ports':ports,'enabled':True}
            resources.append(resource)
            check('create_'+protocol+'_resource',lambda r=resource:admin.call('/netgo/v2/admin/resources/'+r['id'],'PUT',r))
        owner['user']['resources']=[r['id'] for r in resources];owner['user']['max_devices']=10
        check('assign_only_authorized_resources',lambda:admin.call('/netgo/v2/admin/users/'+owner['user']['id'],'PUT',owner['user']))
        for label,number in (('a',241),('b',242),('c',243)):
            client=Client(label,number,args.output,profile);clients.append(client)
            check('start_isolated_client_'+label,client.start)
        a,b,c=clients
        check('no_direct_transport_bypass',lambda:a.probe(success=False))
        first=check('real_client_login_and_lease',lambda:a.connect(owner['user']['alias'],owner['initial_password']))
        # A negotiated peer path may follow the lease/routing update asynchronously.
        def reachable():
            deadline=time.monotonic()+25
            while True:
                try:return a.probe()
                except AssertionError:
                    if time.monotonic()>deadline:raise
                    time.sleep(1)
        check('tcp_payload_and_masquerade',reachable)
        check('udp_payload_and_masquerade',lambda:a.probe('udp',18081))
        check('ungranted_tcp_port_blocked',lambda:a.probe(port=18082,success=False))
        check('second_user_without_resources_cannot_acquire_a_lease',lambda:b.connect(outsider['user']['alias'],outsider['initial_password'],expected='forbidden'))
        check('unprivileged_user_cannot_reach_lan',lambda:b.probe(success=False))
        isolated={'id':'vpn'+suffix+'other','name':'Other user isolated TCP','cidr':TARGET+'/32','protocol':'tcp','ports':[18082],'enabled':True}
        resources.append(isolated)
        check('create_distinct_second_user_resource',lambda:admin.call('/netgo/v2/admin/resources/'+isolated['id'],'PUT',isolated))
        outsider['user']['resources']=[isolated['id']]
        check('assign_only_second_user_resource',lambda:admin.call('/netgo/v2/admin/users/'+outsider['user']['id'],'PUT',outsider['user']))
        check('second_user_authorized_connection',lambda:b.connect(outsider['user']['alias'],outsider['initial_password']))
        check('second_user_can_reach_own_port',lambda:b.probe(port=18082))
        check('second_user_cannot_reach_first_user_port',lambda:b.probe(success=False))
        check('first_user_cannot_reach_second_user_port',lambda:a.probe(port=18082,success=False))
        def revoke_existing_flow():
            code="""import socket,time
s=socket.create_connection(('198.19.240.2',18083),timeout=3);s.settimeout(1)
s.sendall(b'proof');assert s.recv(5)==b'proof';print('READY',flush=True)
while True:
 try:s.sendall(b'proof');assert s.recv(5)==b'proof';time.sleep(.2)
 except (OSError,AssertionError):print('BLOCKED',flush=True);break
"""
            stream=subprocess.Popen(['ip','netns','exec',a.name,'python3','-u','-c',code],stdout=subprocess.PIPE,stderr=subprocess.PIPE,text=True)
            try:
                select=selectors.DefaultSelector();select.register(stream.stdout,selectors.EVENT_READ)
                require(bool(select.select(5)) and stream.stdout.readline().strip()=='READY','established TCP flow not ready')
                owner['user']['disabled']=True;started=time.monotonic()
                admin.call('/netgo/v2/admin/users/'+owner['user']['id'],'PUT',owner['user'])
                require(bool(select.select(5)) and stream.stdout.readline().strip()=='BLOCKED','old TCP connection survived revocation')
                require(time.monotonic()-started<=5,'revocation exceeded five seconds')
                select.close()
            finally:
                if stream.poll() is None:stream.kill()
                stream.wait(timeout=5);stream.stdout.close();stream.stderr.close()
        check('disable_revokes_established_tcp_within_five_seconds',revoke_existing_flow)
        check('disabled_peer_new_traffic_denied',lambda:a.probe(success=False))
        owner['user']['disabled']=False
        check('reenable_account',lambda:admin.call('/netgo/v2/admin/users/'+owner['user']['id'],'PUT',owner['user']))
        check('client_disconnect',a.disconnect)
        again=check('same_device_reconnect',lambda:a.connect(owner['user']['alias'],owner['initial_password']))
        check('reconnect_preserves_peer_and_increases_generation',lambda:require(again['lease']['peer_id']==first['lease']['peer_id'] and again['generation']>first['generation'],'reconnect changed identity or reused generation'))
        check('reconnected_tcp_flow',reachable)
        check('concurrent_connection_rejected',lambda:c.connect(owner['user']['alias'],owner['initial_password'],expected='session_conflict'))
        replacement=check('explicit_replace_acquires_new_lease',lambda:c.connect(owner['user']['alias'],owner['initial_password'],replace=True))
        check('replaced_peer_is_blocked',lambda:a.probe(success=False))
        check('replacement_peer_can_access_lan',lambda:c.probe())
        def kernel_expiry():
            nonlocal frozen
            # Freeze both product processes, preserving the actual kernel timeout;
            # a graceful stop would revoke immediately and would not test expiry.
            c.product.kill();c.product.wait(timeout=5)
            live=admin.call('/netgo/v2/admin/state')['leases'][replacement['lease']['id']]
            expiry=datetime.fromisoformat(live['expires_at'].replace('Z','+00:00')).timestamp()
            frozen=True
            command(['systemctl','kill','--kill-whom=main','--signal=STOP','netgo-server','netgo-helper'])
            c.probe()
            last_notice=0
            while time.time()<expiry+6:
                try:c.probe()
                except AssertionError:
                    require(time.time()>=expiry-4,'flow blocked before expected kernel expiry')
                    return
                remaining=max(0,int(expiry-time.time()))
                if time.monotonic()-last_notice>=20:
                    print('Kernel lease expiry observation: '+str(remaining)+' seconds remaining',flush=True);last_notice=time.monotonic()
                time.sleep(1)
            raise AssertionError('kernel allowed traffic beyond lease expiry')
        check('kernel_timeout_blocks_noncooperating_client_with_both_services_frozen',kernel_expiry)
        resumed()
        check('control_service_recovers_after_resume',lambda:admin.call('/netgo/v2/admin/state'))
        for client in (a,b):
            check('disconnect_'+client.name[-1],client.disconnect)
            check('route_cleanup_'+client.name[-1],lambda item=client:require('wt0' not in item.exec(['ip','route','show','table','all']),'tunnel route remained after disconnect'))
    except BaseException as error:
        failed=True;print('Live network test stopped: '+str(error),file=sys.stderr,flush=True)
    finally:
        try:resumed()
        except BaseException as error:failed=True;print('Resume requires attention: '+str(error),file=sys.stderr)
        for client in reversed(clients):
            try:check('cleanup_'+client.name,client.close)
            except BaseException:failed=True
        for item in users:
            try:check('delete_'+item['user']['alias'],lambda user=item['user']:admin.call('/netgo/v2/admin/users/'+user['id'],'DELETE'))
            except BaseException:failed=True
        for resource in resources:
            try:check('delete_'+resource['id'],lambda r=resource:admin.call('/netgo/v2/admin/resources/'+r['id'],'DELETE'))
            except BaseException:failed=True
        try:check('host_dns_preserved',lambda:require(hashlib.sha256(Path('/etc/resolv.conf').read_bytes()).hexdigest()==dns_before,'host DNS changed during namespace test'))
        except BaseException:failed=True
        (args.output/'result.json').write_text(json.dumps({'schema':1,'status':'FAIL' if failed else 'PASS','checks':records,
            'generated_utc':datetime.now(timezone.utc).isoformat(),
            'topology':'Real Linux services, three network namespace clients, independent non-VPN LAN target',
            'limits':['Namespaces do not prove Internet NAT, public DDNS, VPS, Windows or 72-hour reliability']},indent=2)+'\n')
    return 1 if failed else 0


if __name__=='__main__':raise SystemExit(main())
