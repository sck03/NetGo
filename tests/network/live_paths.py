#!/usr/bin/env python3
"""Real local Relay/TCP fallback and ten-session laboratory acceptance."""
import argparse
from concurrent.futures import ThreadPoolExecutor
from datetime import datetime, timezone
import hashlib
import json
import os
from pathlib import Path
import secrets
import sys
import time
from urllib.parse import urlsplit

from live_api import Session, require
from live_network import Client


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--site', type=Path, required=True)
    parser.add_argument('--alias', required=True)
    parser.add_argument('--password-stdin', action='store_true', required=True)
    parser.add_argument('--mode', choices=('relay', 'auxiliary-relay', 'concurrency'), required=True)
    parser.add_argument('--output', type=Path, required=True)
    parser.add_argument('--run', action='store_true', required=True)
    args = parser.parse_args()
    require(sys.platform == 'linux' and os.geteuid() == 0, 'root Linux lab required')
    require(not args.output.exists(), 'choose a new output directory')
    profile = json.loads(args.site.read_text())
    require(profile['ID'].startswith('netgo-lab-'), 'dedicated lab site required')
    if args.mode == 'auxiliary-relay':
        require(profile.get('AuxiliaryRelay') and any(c['Kind']=='auxiliary' for c in profile['Candidates']), 'preconfigured public auxiliary required')
    if args.mode == 'concurrency':
        available = next(int(line.split()[1]) for line in Path('/proc/meminfo').read_text().splitlines() if line.startswith('MemAvailable:'))
        require(available >= 1536*1024, 'ten-client lab needs at least 1.5 GiB currently available memory')
    os.umask(0o077)
    args.output.mkdir(mode=0o700, parents=True)
    password = sys.stdin.readline().rstrip('\r\n')
    admin = Session(profile)
    clients, resources, records = [], [], []
    user = None
    failed = False
    dns = hashlib.sha256(Path('/etc/resolv.conf').read_bytes()).digest()
    def check(name, action):
        record = {'name': name, 'status': 'FAIL'}
        started = time.monotonic()
        try:
            value = action()
            record['status'] = 'PASS'
            return value
        except BaseException as error:
            record['error'] = str(error)
            raise
        finally:
            record['seconds'] = round(time.monotonic()-started, 3)
            records.append(record)
            print(record['status']+' '+name, flush=True)
    try:
        check('administrator_login', lambda: admin.login(args.alias, password))
        suffix = secrets.token_hex(4)
        created = admin.call('/netgo/v2/admin/users', 'POST', {'Alias':'path'+suffix,'Email':'path'+suffix+'@netgo.invalid','Name':'Path acceptance user'})
        user = created['user']
        for protocol, ports in (('tcp',[18080]),('udp',[18081])):
            resource = {'id':'path'+suffix+protocol,'name':'Path lab '+protocol,'cidr':'198.19.240.2/32','protocol':protocol,'ports':ports,'enabled':True}
            admin.call('/netgo/v2/admin/resources/'+resource['id'], 'PUT', resource)
            resources.append(resource['id'])
        count = 10 if args.mode == 'concurrency' else 1
        user.update(resources=resources, max_devices=count, max_sessions=count)
        check('grant_bounded_lab_resources_and_session_limit', lambda: admin.call('/netgo/v2/admin/users/'+user['id'], 'PUT', user))
        for index in range(count):
            client = Client(chr(ord('a')+index), 241+index, args.output, profile)
            clients.append(client)
            check('start_isolated_client_'+str(index+1), client.start)
        if args.mode in ('relay', 'auxiliary-relay'):
            client = clients[0]
            if args.mode == 'auxiliary-relay':
                public=urlsplit(profile['AuxiliaryRelay']).hostname
                check('bounded_public_vps_return_path',lambda:client.public_uplink(public))
            check('block_all_client_underlay_udp', lambda: client.exec(['nft','-f','-'], input='add chain inet netgo_lab_transport relay_only { type filter hook output priority -210; policy accept; }\nadd rule inet netgo_lab_transport relay_only oifname "eth0" meta l4proto udp counter drop\n'))
            if args.mode == 'auxiliary-relay':
                addresses={urlsplit('https://'+c['Address']).hostname for c in profile['Candidates'] if c['Kind']=='direct'}
                addresses.add(urlsplit(profile['LocalRelay'] if '://' in profile['LocalRelay'] else 'rels://'+profile['LocalRelay']).hostname)
                import ipaddress
                rules=[]
                for address in sorted(addresses):
                    parsed=ipaddress.ip_address(address)
                    require(parsed.version==4,'this laboratory blocks explicit IPv4 direct candidates')
                    rules.append('add rule inet netgo_lab_transport relay_only oifname "eth0" ip daddr '+str(parsed)+' counter drop')
                check('block_direct_control_and_local_relay',lambda:client.exec(['nft','-f','-'],input='\n'.join(rules)+'\n'))
            check('login_and_lease_with_udp_unavailable', lambda: client.connect(user['alias'], created['initial_password']))
            if args.mode == 'auxiliary-relay':
                check('public_auxiliary_control_selected',lambda:require(client.ipc('status').get('control')=='auxiliary','direct control was used despite blocking'))
            def relay_path():
                deadline = time.monotonic()+90
                while time.monotonic()<deadline:
                    state=client.ipc('status')
                    if state['state']=='failed':
                        raise RuntimeError('relay connection failed: '+state.get('error','unknown'))
                    if state.get('data',{}).get('path')==('auxiliary_relay' if args.mode=='auxiliary-relay' else 'local_relay'):
                        return
                    time.sleep(1)
                raise RuntimeError('expected Relay was not reported by the real network core')
            check('real_core_reports_'+args.mode, relay_path)
            transfer_timeout=10 if args.mode=='auxiliary-relay' else 2
            check('tcp_integrity_through_'+args.mode, lambda:client.probe(timeout=transfer_timeout))
            check('udp_application_payload_through_tcp_relay', lambda: client.probe('udp',18081,timeout=transfer_timeout))
            check('ungranted_port_remains_blocked_on_relay', lambda: client.probe(port=18082,success=False))
            def drops():
                rules=json.loads(client.exec(['nft','-j','list','chain','inet','netgo_lab_transport','relay_only']))
                packets=sum(expr.get('counter',{}).get('packets',0) for item in rules['nftables'] for expr in item.get('rule',{}).get('expr',[]))
                require(packets>0,'UDP failure was not actually exercised')
            check('kernel_counters_prove_underlay_udp_blocked', drops)
        else:
            def connect_all():
                def connect_with_limit(client):
                    deadline=time.monotonic()+150
                    while True:
                        try:
                            return client.connect(user['alias'],created['initial_password'])
                        except RuntimeError as error:
                            if 'rate_limited' not in str(error) or time.monotonic()>=deadline:
                                raise
                            # Respect the production account's five-attempts
                            # per minute limit instead of weakening it for load.
                            time.sleep(15)
                with ThreadPoolExecutor(max_workers=count) as pool:
                    futures=[pool.submit(connect_with_limit,client) for client in clients]
                    return [future.result() for future in futures]
            states=check('ten_authorized_sessions_with_bounded_login_rate_retry',connect_all)
            check('ten_distinct_verified_peer_identities',lambda:require(len({state['lease']['peer_id'] for state in states})==count,'clients shared a peer identity'))
            def active():
                leases=admin.call('/netgo/v2/admin/state')['leases']
                require(sum(lease['user_id']==user['id'] for lease in leases.values())==count,'active lease count differs')
            check('ten_current_server_authorized_sessions',active)
            def all_payloads():
                with ThreadPoolExecutor(max_workers=count) as pool:
                    futures=[pool.submit(client.probe) for client in clients]
                    for future in futures:
                        future.result()
            check('ten_concurrent_tcp_payloads_and_masquerade',all_payloads)
            check('ten_sessions_renew',lambda:(time.sleep(35),active()))
            check('payloads_after_renewal',all_payloads)
    except BaseException as error:
        failed=True
        print('Path test stopped: '+str(error),file=sys.stderr,flush=True)
    finally:
        for client in reversed(clients):
            try:
                check('cleanup_'+client.name,client.close)
            except BaseException:
                failed=True
        if user:
            try:
                check('delete_path_test_user',lambda:admin.call('/netgo/v2/admin/users/'+user['id'],'DELETE'))
            except BaseException:
                failed=True
        for rid in resources:
            try:
                check('delete_resource_'+rid,lambda key=rid:admin.call('/netgo/v2/admin/resources/'+key,'DELETE'))
            except BaseException:
                failed=True
        try:
            check('host_dns_preserved',lambda:require(hashlib.sha256(Path('/etc/resolv.conf').read_bytes()).digest()==dns,'host DNS changed'))
        except BaseException:
            failed=True
        (args.output/'result.json').write_text(json.dumps({'schema':1,'mode':args.mode,'status':'FAIL' if failed else 'PASS',
            'generated_utc':datetime.now(timezone.utc).isoformat(),'checks':records,
            'limits':['Public VPS high-port auxiliary tested from LAN namespaces; full NAT and TCP-443-only cases remain separate' if args.mode=='auxiliary-relay' else 'Local network namespaces and controlled UDP loss do not establish public NAT/VPS behavior',
                      'Ten sessions are a functional baseline, not a capacity or 72-hour reliability claim']},indent=2)+'\n')
    return 1 if failed else 0


if __name__=='__main__':
    raise SystemExit(main())
