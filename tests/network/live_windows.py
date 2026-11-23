#!/usr/bin/env python3
"""Opt-in Windows lab test of the native agent and authorized privileged tunnel."""
import argparse
import csv
from datetime import datetime, timezone
import hashlib
import io
import json
import os
from pathlib import Path
import socket
import struct
import subprocess
import sys
import time
import urllib.error
import urllib.request


def require(condition, message):
    if not condition:
        raise AssertionError(message)


def owner_sid():
    output = subprocess.check_output(['whoami.exe', '/user', '/fo', 'csv', '/nh'], text=True)
    return next(csv.reader(io.StringIO(output)))[1]


def ipc(request):
    path = r'\\.\pipe\netgo-native-' + owner_sid()
    deadline = time.monotonic() + 10
    while True:
        try:
            stream = open(path, 'r+b', buffering=0)
            break
        except OSError:
            if time.monotonic() > deadline:
                raise
            time.sleep(.1)
    with stream:
        data = json.dumps(request, separators=(',', ':')).encode()
        stream.write(struct.pack('<I', len(data)) + data)
        def read(count):
            result = b''
            while len(result) < count:
                block = stream.read(count - len(result))
                if not block:
                    raise RuntimeError('native IPC closed')
                result += block
            return result
        size = struct.unpack('<I', read(4))[0]
        require(0 < size <= 1024 * 1024, 'invalid native IPC frame')
        result = json.loads(read(size))
        stream.write(b'\x01')
        if isinstance(result, dict) and set(result) == {'error'}:
            raise RuntimeError('native IPC: ' + result['error'])
        return result


def network_snapshot():
    command = r"""[Console]::OutputEncoding=[Text.UTF8Encoding]::new();
@{defaults=@(Get-NetRoute | Where-Object {$_.DestinationPrefix -in @('0.0.0.0/0','::/0')} |
Select-Object DestinationPrefix,NextHop,InterfaceIndex,RouteMetric | Sort-Object DestinationPrefix,InterfaceIndex);
dns=@(Get-DnsClientServerAddress | Where-Object {$_.ServerAddresses.Count -gt 0} |
Select-Object InterfaceIndex,AddressFamily,ServerAddresses | Sort-Object InterfaceIndex,AddressFamily);
target=@(Get-NetRoute -DestinationPrefix '198.19.240.2/32' -ErrorAction SilentlyContinue |
Select-Object DestinationPrefix,NextHop,InterfaceIndex | Sort-Object InterfaceIndex)} | ConvertTo-Json -Depth 5
"""
    powershell = str(Path(os.environ['WINDIR']) / 'System32/WindowsPowerShell/v1.0/powershell.exe')
    return json.loads(subprocess.check_output([powershell, '-NoProfile', '-NonInteractive', '-Command', command],
                                            encoding='utf-8', timeout=40))


def probe(kind='http', port=18080, success=True):
    target = '198.19.240.2'
    result = False
    reachable = False
    source = ''
    try:
        if kind == 'http':
            opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
            with opener.open('http://' + target + ':' + str(port), timeout=3) as response:
                reachable = True
                data = response.read(1024 * 1024)
                result = hashlib.sha256(data).digest() == hashlib.sha256(bytes(range(256)) * 1024).digest()
                source = response.headers.get('X-NetGo-Test-Source', '')
        else:
            with socket.socket(socket.AF_INET, socket.SOCK_DGRAM) as sock:
                sock.settimeout(3)
                payload = b'netgo-windows-udp-integrity' * 64
                sock.sendto(payload, (target, port))
                reply = sock.recv(8192)
                reachable = True
                address, data = reply.split(b'\n', 1)
                result = data == payload
                source = address.decode().removeprefix('netgo-source:')
    except urllib.error.HTTPError:
        reachable = True
    except (OSError, ValueError):
        pass
    require(reachable == success, 'unexpected Windows ' + kind + ' reachability on port ' + str(port))
    if success:
        require(result, 'Windows LAN target payload/protocol mismatch')
        require(source == '198.19.240.1', 'traffic did not use the Linux routing peer masquerade')


def connected(credentials, password=True, remember=False):
    ipc({'action': 'connect', 'site': credentials['profile']['ID'], 'alias': credentials['alias'],
         'password': credentials['password'] if password else '', 'remember': remember})
    deadline = time.monotonic() + 100
    while time.monotonic() < deadline:
        state = ipc({'action': 'status'})
        if state['state'] == 'failed':
            raise RuntimeError('native Windows connection: ' + state.get('error', 'unknown'))
        if state['state'] == 'connected':
            return state
        time.sleep(.5)
    raise RuntimeError('native Windows connection timed out')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--credentials', type=Path, required=True, help='protected lab-only JSON file; never printed')
    parser.add_argument('--output', type=Path, required=True)
    parser.add_argument('--run', action='store_true', required=True)
    parser.add_argument('--foreground-core', action='store_true', help='project-directory foreground core instead of installed system service')
    args = parser.parse_args()
    if sys.platform != 'win32':
        parser.error('run on the authorized Windows desktop user')
    if args.output.exists():
        parser.error('choose a new evidence directory')
    args.output.mkdir(parents=True)
    credentials = json.loads(args.credentials.read_text(encoding='utf-8-sig'))
    profile = credentials['profile']
    require(profile['ID'].startswith('netgo-lab-'), 'dedicated laboratory profile required')
    require(ipc({'action': 'status'})['state'] in ('disconnected', 'failed'), 'disconnect existing user connection first')
    require(all(item['ID'] != profile['ID'] for item in ipc({'action': 'profiles'})), 'refusing to replace an existing profile or credentials')
    before = network_snapshot()
    records = []
    imported = False
    failed = False
    def check(name, action):
        record = {'name': name, 'status': 'FAIL'}
        start = time.monotonic()
        try:
            result = action()
            record['status'] = 'PASS'
            return result
        except BaseException as error:
            record['error'] = str(error).replace(credentials['password'], '[redacted]')
            raise
        finally:
            record['seconds'] = round(time.monotonic() - start, 3)
            records.append(record)
            print(record['status'] + ' ' + name, flush=True)
    try:
        check('no_lan_target_without_vpn', lambda: probe(success=False))
        check('import_public_site_through_native_ipc', lambda: ipc({'action': 'import', 'profile': profile}))
        imported = True
        first = check('project_foreground_core_native_login_and_connection' if args.foreground_core else 'installed_windows_service_native_login_and_connection', lambda: connected(credentials, remember=True))
        check('windows_tcp_payload_and_masquerade', probe)
        check('windows_udp_payload_and_masquerade', lambda: probe('udp', 18081))
        check('windows_ungranted_port_denied', lambda: probe(port=18082, success=False))
        check('password_saved_for_only_this_profile', lambda: require(ipc({'action': 'credential-info', 'site': profile['ID']})['remembered'], 'remembered credential missing'))
        check('native_diagnostics_redacted', lambda: require(credentials['password'] not in json.dumps(ipc({'action': 'diagnostic'})), 'password in diagnostic'))
        during = network_snapshot()
        check('internet_default_routes_preserved', lambda: require(before['defaults'] == during['defaults'], 'VPN changed Internet defaults'))
        check('windows_disconnect', lambda: ipc({'action': 'disconnect'}))
        check('lan_target_blocked_after_disconnect', lambda: probe(success=False))
        second = check('saved_credential_reconnect', lambda: connected(credentials, password=False, remember=True))
        check('stable_peer_and_fresh_generation', lambda: require(first['lease']['peer_id'] == second['lease']['peer_id'] and second['generation'] > first['generation'], 'reconnect changed peer or reused generation'))
        check('windows_reconnected_payload', probe)
        check('second_disconnect', lambda: ipc({'action': 'disconnect'}))
        check('forget_test_password', lambda: ipc({'action': 'forget', 'site': profile['ID']}))
        check('forgotten_password_not_retained', lambda: require(not ipc({'action': 'credential-info', 'site': profile['ID']})['remembered'], 'password retained'))
        check('begin_cancellable_connection', lambda: ipc({'action': 'connect', 'site': profile['ID'], 'alias': credentials['alias'], 'password': credentials['password']}))
        check('cancel_connection', lambda: ipc({'action': 'disconnect'}))
        time.sleep(3)
        check('cancelled_generation_does_not_reconnect', lambda: require(ipc({'action': 'status'})['state'] == 'disconnected', 'cancelled connection returned'))
    except BaseException as error:
        failed = True
        print('Windows lab test stopped: ' + str(error).replace(credentials['password'], '[redacted]'), file=sys.stderr)
    finally:
        if imported:
            for name, action in (
                ('cleanup_disconnect', lambda: ipc({'action': 'disconnect'})),
                ('cleanup_test_profile_and_keys', lambda: ipc({'action': 'delete', 'site': profile['ID']})),
            ):
                try:
                    check(name, action)
                except BaseException:
                    failed = True
        try:
            after = network_snapshot()
            check('routes_and_dns_restored', lambda: require(before == after, 'routing/DNS differs from pre-test state'))
        except BaseException:
            failed = True
        (args.output / 'result.json').write_text(json.dumps({'schema': 1, 'status': 'FAIL' if failed else 'PASS',
            'generated_utc': datetime.now(timezone.utc).isoformat(), 'checks': records,
            'limits': ['Project foreground core; no system service installed' if args.foreground_core else 'Existing Windows service; OS elevation and service uninstall tested separately',
                       'LAN lab path does not establish public NAT, VPS, sleep/wake or Windows 11 support']}, indent=2) + '\n', encoding='utf-8')
    return 1 if failed else 0


if __name__ == '__main__':
    raise SystemExit(main())
