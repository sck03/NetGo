#!/usr/bin/env python3
"""Opt-in maintenance rehearsal for an explicitly owned NetGo Linux laboratory."""
import argparse
import copy
from datetime import datetime, timezone
import hashlib
import json
import os
from pathlib import Path
import pty
import secrets
import shutil
import subprocess
import sys
import termios
import time

from live_api import Session, require, rejected

CTL = '/opt/netgo/current/netgoctl'
CONFIG = Path('/etc/netgo/site.json')
SERVICES = ('netbird-server', 'netgo-router', 'netgo-helper', 'netgo-server')
ROOTS = tuple(map(Path, ('/etc/netgo', '/var/lib/netgo', '/var/lib/netbird-server', '/var/lib/netgo-router')))
PRODUCTS = ('netgo-server', 'netgo-helper', 'netgoctl', 'netgo-client-service', 'netgo-ddns')


def run(args, *, input=None, expect=0, password=None, timeout=180):
    if password is None:
        result = subprocess.run(args, input=input, capture_output=True, timeout=timeout)
        code, stdout, stderr = result.returncode, result.stdout, result.stderr
    else:
        master, slave = pty.openpty()
        process = subprocess.Popen(args, stdin=slave, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        try:
            deadline = time.monotonic() + 20
            while termios.tcgetattr(slave)[3] & termios.ECHO:
                if process.poll() is not None or time.monotonic() > deadline:
                    raise RuntimeError('maintenance command did not request protected terminal input')
                time.sleep(.02)
            os.write(master, password.encode() + b'\n')
            stdout, stderr = process.communicate(timeout=timeout)
            code = process.returncode
        finally:
            if process.poll() is None:
                process.kill()
                process.wait(timeout=10)
            os.close(master)
            os.close(slave)
    if (expect == 0 and code != 0) or (expect != 0 and code == 0):
        detail = stderr.decode('utf-8', errors='replace').strip()[:1200]
        for secret in (password, input.decode('utf-8', errors='replace').strip() if input else None):
            if secret:
                detail = detail.replace(secret, '[redacted]')
        raise RuntimeError('unexpected exit ' + str(code) + ' from ' + Path(args[0]).name + ' ' + (args[1] if len(args) > 1 else '') + ': ' + detail)
    return stdout


def hashes(root):
    result = {}
    for path in root.rglob('*'):
        require(not path.is_symlink(), 'unexpected symlink in lab backup root')
        if path.is_file():
            result[path.relative_to(root).as_posix()] = hashlib.sha256(path.read_bytes()).hexdigest()
    return result


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--lab-root', type=Path, required=True)
    parser.add_argument('--output', type=Path, required=True)
    parser.add_argument('--alias', required=True)
    parser.add_argument('--password-stdin', action='store_true', required=True)
    parser.add_argument('--run', action='store_true', required=True)
    args = parser.parse_args()
    if sys.platform != 'linux' or os.geteuid() != 0:
        parser.error('root on the authorized Linux lab is required')
    lab, output = args.lab_root.resolve(), args.output.resolve()
    require(json.loads((lab / 'topology.json').read_text())['owner'] == 'NetGo acceptance lab', 'lab ownership marker missing')
    require(output.is_relative_to(lab) and output != lab and not output.exists(), 'use a new evidence directory inside the lab')
    require(shutil.disk_usage(lab).free > 1024**3, 'maintenance rehearsal requires 1 GiB free disk space')
    os.umask(0o077)
    output.mkdir(mode=0o700)
    password = sys.stdin.readline().rstrip('\r\n')
    profile = json.loads(Path('/etc/netgo/client-site.json').read_text())
    require(profile['ID'].startswith('netgo-lab-'), 'dedicated laboratory identity required')
    admin = Session(profile)
    records, releases = [], []
    original = Path('/opt/netgo/current').resolve()
    previous = Path('/opt/netgo/previous-release.json')
    previous_bytes = previous.read_bytes() if previous.exists() else None
    user = None
    stopped = False
    failure = False
    passphrase = secrets.token_urlsafe(32)
    (output / 'private-recovery.json').write_text(json.dumps({'backup_password': passphrase}) + '\n')
    def check(name, action):
        record = {'name': name, 'status': 'FAIL'}
        started = time.monotonic()
        try:
            result = action()
            record['status'] = 'PASS'
            return result
        except BaseException as error:
            record['error'] = str(error).replace(password, '[redacted]').replace(passphrase, '[redacted]')
            raise
        finally:
            record['seconds'] = round(time.monotonic() - started, 3)
            records.append(record)
            print(record['status'] + ' ' + name, flush=True)
    def healthy():
        deadline = time.monotonic() + 45
        while time.monotonic() < deadline:
            try:
                if admin.call('/netgo/v1/health')['site_id'] == profile['ID']:
                    return
            except (OSError, RuntimeError):
                pass
            time.sleep(.5)
        raise RuntimeError('trusted product health did not recover')
    def doctor_recovered():
        # HTTP health can recover before the routing peer recreates its tunnel.
        # Keep every observation; bounded convergence does not waive any check.
        deadline = time.monotonic() + 45
        attempts = []
        while True:
            result = subprocess.run([CTL, 'doctor'], capture_output=True, timeout=20)
            report = json.loads(result.stdout)
            require(result.returncode in (0, 1) and isinstance(report.get('checks'), list), 'invalid doctor result')
            attempts.append(report)
            (output / 'doctor-recovery.json').write_text(json.dumps(attempts, indent=2) + '\n')
            if result.returncode == 0 and report.get('ready') is True:
                return
            if time.monotonic() >= deadline:
                failed = [c['name'] for c in report['checks'] if c['status'] != 'PASS']
                raise RuntimeError('doctor did not recover: ' + ', '.join(failed))
            time.sleep(.5)
    def make_release(label, broken=False):
        version = '0.2.4-lab-' + label + '-' + secrets.token_hex(3)
        folder = output / version
        folder.mkdir()
        for name in PRODUCTS:
            shutil.copy2(original / name, folder / name)
        if broken:
            (folder / 'netgo-server').write_text('#!/bin/sh\nexit 19\n')
            (folder / 'netgo-server').chmod(0o755)
        (folder / 'release.json').write_text(json.dumps({'version': version, 'protocol': 1, 'schema': 1, 'os': 'linux', 'arch': 'amd64'}) + '\n')
        (folder / 'SHA256SUMS').write_text(''.join(hashlib.sha256((folder / name).read_bytes()).hexdigest() + '  ' + name + '\n' for name in (*PRODUCTS, 'release.json')))
        releases.append(Path('/opt/netgo/releases') / version)
        return folder, hashlib.sha256((folder / 'SHA256SUMS').read_bytes()).hexdigest()
    try:
        check('administrator_login', lambda: admin.login(args.alias, password))
        check('doctor_before_maintenance', lambda: require(json.loads(run([CTL, 'doctor']))['ready'], 'doctor not ready'))
        initial = json.loads(CONFIG.read_text())
        changed = copy.deepcopy(initial)
        changed['Site']['Name'] = initial['Site']['Name'] + ' maintenance rehearsal'
        changed['Site']['ConfigRevision'] = initial['Site'].get('ConfigRevision', 0) + 1
        proposal = output / 'proposal.json'
        proposal.write_text(json.dumps(changed) + '\n')
        check('configuration_plan', lambda: require(json.loads(run([CTL, 'config', 'plan', '--input', str(proposal)]))['requires_reconnect'], 'change was not planned'))
        check('configuration_apply_and_restart', lambda: run([CTL, 'config', 'apply', '--input', str(proposal)]))
        check('published_new_configuration', lambda: require(admin.call('/netgo/v1/site')['Name'] == changed['Site']['Name'], 'configuration not published'))
        restored = copy.deepcopy(initial)
        restored['Site']['ConfigRevision'] = changed['Site']['ConfigRevision'] + 1
        proposal.write_text(json.dumps(restored) + '\n')
        check('restore_name_with_increasing_revision', lambda: run([CTL, 'config', 'apply', '--input', str(proposal)]))
        check('active_writer_backup_refused', lambda: run([CTL, 'backup', '--output', str(output / 'must-not-exist.netgo')], expect=1))
        require(not (output / 'must-not-exist.netgo').exists(), 'unsafe live backup was written')
        stopped = True
        check('stop_owned_writers', lambda: run(['systemctl', 'stop', *SERVICES]))
        before = {str(root): hashes(root) for root in ROOTS}
        backup = output / 'consistent.netgo'
        check('encrypted_consistent_backup', lambda: run([CTL, 'backup', '--output', str(backup)], password=passphrase))
        check('wrong_backup_password_rejected', lambda: run([CTL, 'restore', '--input', str(backup), '--output', str(output / 'wrong-password')], password='wrong-test-password', expect=1))
        require(not (output / 'wrong-password').exists(), 'wrong password left extracted files')
        damaged = bytearray(backup.read_bytes())
        damaged[-1] ^= 1
        tampered = output / 'tampered.netgo'
        tampered.write_bytes(damaged)
        check('tampered_backup_rejected', lambda: run([CTL, 'restore', '--input', str(tampered), '--output', str(output / 'tampered-restore')], password=passphrase, expect=1))
        destination = output / 'restored'
        check('restore_into_new_directory', lambda: run([CTL, 'restore', '--input', str(backup), '--output', str(destination)], password=passphrase))
        manifest = json.loads((destination / 'manifest.json').read_text())
        def compare():
            mapping = manifest['archive_root_map']
            require(set(mapping.values()) == set(before), 'backup root mapping changed')
            for prefix, source in mapping.items():
                require(hashes(destination / prefix) == before[source], 'restored file bytes differ')
        check('every_configuration_identity_and_database_byte_restored', compare)
        check('restart_after_backup', lambda: run(['systemctl', 'start', *SERVICES]))
        stopped = False
        check('trusted_health_after_backup', healthy)
        check('administrator_identity_survives_restart', lambda: admin.login(args.alias, password))
        suffix = secrets.token_hex(4)
        created = admin.call('/netgo/v1/admin/users', 'POST', {'Alias': 'ops'+suffix, 'Email': 'ops'+suffix+'@netgo.invalid', 'Name': 'Maintenance acceptance user'})
        user = created['user']
        ordinary = Session(profile, '')
        check('password_test_user_can_login', lambda: ordinary.login(user['alias'], created['initial_password']))
        check('weak_password_refused_before_maintenance', lambda: run([CTL, 'reset-password', '--user-id', user['id'], '--password-stdin'], input=b'NoSpecial123\n', expect=1))
        check('weak_password_preserves_health_and_credential', lambda: (healthy(), ordinary.call('/netgo/v1/me')))
        replacement = secrets.token_urlsafe(24) + 'aA1!'
        (output / 'private-password-test.json').write_text(json.dumps({'user_id': user['id'], 'new_password': replacement})+'\n')
        check('native_idp_password_reset', lambda: run([CTL, 'reset-password', '--user-id', user['id'], '--password-stdin'], input=(replacement+'\n').encode()))
        check('password_reset_recovers_health', healthy)
        check('password_reset_revokes_old_product_credential', lambda: rejected(lambda: ordinary.call('/netgo/v1/me'), 401))
        check('old_password_no_longer_authenticates', lambda: rejected(lambda: Session(profile, '').login(user['alias'], created['initial_password']), 401))
        check('replacement_password_authenticates', lambda: ordinary.login(user['alias'], replacement))
        good, digest = make_release('upgrade')
        check('wrong_release_digest_refused', lambda: run([CTL, 'release', 'verify', '--input', str(good), '--sha256', '0'*64], expect=1))
        check('verified_release_manifest', lambda: run([CTL, 'release', 'verify', '--input', str(good), '--sha256', digest]))
        check('upgrade_transaction', lambda: run([CTL, 'upgrade', '--input', str(good), '--sha256', digest, '--output', str(output / 'upgrade.netgo')], password=passphrase))
        check('upgrade_link_and_live_health', lambda: (require(Path('/opt/netgo/current').resolve() == releases[-1], 'new release not active'), healthy()))
        check('explicit_rollback', lambda: run([CTL, 'rollback']))
        check('rollback_restores_original_release', lambda: (require(Path('/opt/netgo/current').resolve() == original, 'original release not restored'), healthy()))
        bad, digest = make_release('failure', broken=True)
        check('unhealthy_release_automatically_rolls_back', lambda: run([CTL, 'upgrade', '--input', str(bad), '--sha256', digest, '--output', str(output / 'failed-upgrade.netgo')], password=passphrase, expect=1))
        check('failed_upgrade_preserves_original_service', lambda: (require(Path('/opt/netgo/current').resolve() == original, 'bad release remained active'), healthy()))
        check('doctor_after_maintenance', doctor_recovered)
    except BaseException as error:
        failure = True
        print('Maintenance test stopped: ' + str(error).replace(password, '[redacted]').replace(passphrase, '[redacted]'), file=sys.stderr)
    finally:
        if stopped:
            try:
                run(['systemctl', 'start', *SERVICES])
            except BaseException:
                failure = True
        if user:
            try:
                check('cleanup_maintenance_user', lambda: admin.call('/netgo/v1/admin/users/'+user['id'], 'DELETE'))
            except BaseException:
                failure = True
        if Path('/opt/netgo/current').resolve() == original:
            if previous_bytes is None:
                previous.unlink(missing_ok=True)
            else:
                previous.write_bytes(previous_bytes)
                previous.chmod(0o600)
            allowed = set((*PRODUCTS, 'release.json', 'SHA256SUMS'))
            for path in releases:
                if path.is_dir() and not path.is_symlink() and path.resolve().parent == Path('/opt/netgo/releases'):
                    require(set(p.name for p in path.iterdir()) <= allowed, 'unexpected file in test release; retained')
                    for file in path.iterdir():
                        require(file.is_file() and not file.is_symlink(), 'unexpected object in test release; retained')
                        file.unlink()
                    path.rmdir()
        (output / 'result.json').write_text(json.dumps({'schema': 1, 'status': 'FAIL' if failure else 'PASS',
            'generated_utc': datetime.now(timezone.utc).isoformat(), 'checks': records,
            'limits': ['Restore is verified into a separate directory; original data stays in place',
                       'Release switching uses the same schema-1 binaries and an intentional failing lab image, not an upstream migration']}, indent=2)+'\n')
    return 1 if failure else 0


if __name__ == '__main__':
    raise SystemExit(main())
