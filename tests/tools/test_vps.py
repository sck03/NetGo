import copy
from datetime import datetime, timedelta, timezone
import hashlib
import ipaddress
import json
import os
from pathlib import Path
import socket
import sys
import tempfile
import types
import unittest
from unittest.mock import patch

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0,str(ROOT / 'scripts'))
from deploy_config import render
from export_vps import export
import vps_setup as vps

class VpsTests(unittest.TestCase):
    def setUp(self):
        (ROOT / '.cache').mkdir(exist_ok=True)
        self.temporary = tempfile.TemporaryDirectory(dir=ROOT / '.cache')
        self.addCleanup(self.temporary.cleanup); self.root = Path(self.temporary.name)
        self.config = json.loads((ROOT / 'examples/site.json').read_text())
        site = self.config['Site']
        a = {'ID':'vps','Host':'control.vps.example','Relay':'rels://relay.vps.example:28443',
             'STUN':'stun:relay.vps.example:3478','TunnelHost':'tunnel.vps.example',
             'TokenFile':'/etc/netgo/frp.token','Mode':'diagnostic','ControlPort':24443,'TunnelPort':27000}
        self.config['Auxiliaries'] = [a]
        site['Candidates'].append({'ID':'vps','Kind':'auxiliary','Address':'control.vps.example:24443'})
        site['AuxiliaryRelay'] = a['Relay']

    def bundle(self, mode='diagnostic'):
        config = copy.deepcopy(self.config)
        if mode == '443':
            a=config['Auxiliaries'][0];a.update(Mode='443',ControlPort=443,TunnelPort=443,Relay='rels://relay.vps.example:443')
            config['Site']['AuxiliaryRelay']=a['Relay'];config['Site']['Candidates'][-1]['Address']='control.vps.example:443'
        source=self.root / 'site.json';source.write_text(json.dumps(config))
        rendered=self.root / 'rendered';render(source,rendered)
        tls=self.root / 'tls';tls.mkdir()
        from cryptography import x509
        from cryptography.hazmat.primitives import hashes, serialization
        from cryptography.hazmat.primitives.asymmetric import rsa
        for name,host in [('relay','relay.vps.example'),('tunnel','tunnel.vps.example')]:
            key=rsa.generate_private_key(public_exponent=65537,key_size=2048)
            subject=x509.Name([x509.NameAttribute(x509.oid.NameOID.COMMON_NAME,host)])
            now=datetime.now(timezone.utc)
            cert=x509.CertificateBuilder().subject_name(subject).issuer_name(subject).public_key(key.public_key()).serial_number(x509.random_serial_number()).not_valid_before(now-timedelta(minutes=1)).not_valid_after(now+timedelta(days=1)).add_extension(x509.SubjectAlternativeName([x509.DNSName(host)]),critical=False).sign(key,hashes.SHA256())
            (tls / (name+'.crt')).write_bytes(cert.public_bytes(serialization.Encoding.PEM))
            (tls / (name+'.key')).write_bytes(key.private_bytes(serialization.Encoding.PEM,serialization.PrivateFormat.PKCS8,serialization.NoEncryption()))
        (tls / 'ca.key').write_text('DO NOT EXPORT')
        output=self.root / 'bundle';export(rendered,tls,output)
        return output

    def test_export_contains_no_control_key_or_native_secret(self):
        folder=self.bundle();data=vps.load_bundle(folder)
        names={p.relative_to(folder).as_posix() for p in folder.rglob('*') if p.is_file()}
        self.assertEqual(names,set(data['files']) | {'vps.json'})
        self.assertNotIn('tls/ca.key',names)
        self.assertNotIn('secrets.json',names)
        self.assertFalse(any(n.startswith('local/') for n in names))

    def test_tamper_and_arbitrary_frps_settings_are_rejected(self):
        folder=self.bundle();path=folder / 'auxiliary/frps.toml'
        path.write_text(path.read_text()+'\nwebServer.port = 9999\n')
        with self.assertRaisesRegex(ValueError,'digest mismatch'):vps.load_bundle(folder)
        state=json.loads((folder / 'vps.json').read_text());state['files']['auxiliary/frps.toml']=vps.sha(path)
        (folder / 'vps.json').write_text(json.dumps(state))
        with self.assertRaisesRegex(ValueError,'frps configuration'):vps.load_bundle(folder)

    def test_443_exact_sni_and_nft_configuration(self):
        folder=self.bundle('443');vps.load_bundle(folder)
        path=folder / 'auxiliary/haproxy.cfg';path.write_text(path.read_text()+'\nglobal\n lua-load /tmp/foreign.lua\n')
        state=json.loads((folder / 'vps.json').read_text());state['files']['auxiliary/haproxy.cfg']=vps.sha(path)
        (folder / 'vps.json').write_text(json.dumps(state))
        with self.assertRaisesRegex(ValueError,'HAProxy'):vps.load_bundle(folder)

    def test_busy_port_is_rejected(self):
        with socket.socket() as probe:
            probe.bind(('0.0.0.0',0));probe.listen();number=probe.getsockname()[1]
            data={'auxiliary':{'mode':'diagnostic','tunnel_bind_port':number,'relay_bind_port':28443,'control_backend_port':24443,'relay_port':28443,'stun_port':3478}}
            with self.assertRaisesRegex(ValueError,'already in use'):vps.check_ports(data)

    @unittest.skipIf(os.name=='nt','POSIX symlinks and ownership')
    def test_install_uninstall_reinstall_and_modified_unit_guard(self):
        import grp, pwd
        folder=self.bundle();base=self.root / 'opt';etc=self.root / 'etc';units=self.root / 'units';units.mkdir()
        source=self.root / 'source';(source / 'dist/vps/bin').mkdir(parents=True);(source / 'scripts').mkdir()
        elf=bytearray(64);elf[:5]=b'\x7fELF\x02';elf[18:20]=b'\x3e\x00'
        for name in ('frps','netbird-relay'):(source / 'dist/vps/bin' / name).write_bytes(elf)
        for name in ('install-vps.sh','scripts/vps_setup.py'):(source / name).write_bytes((ROOT / name).read_bytes())
        calls=[];fake_run=lambda args:calls.append(list(map(str,args))) or ''
        with patch.multiple(vps,BASE=base,ETC=etc,UNIT_DIR=units,LINK=self.root / 'netgo-vpsctl'),patch.object(vps,'run',fake_run),patch.object(vps,'check_ports'),patch.object(vps,'doctor'),patch('pwd.getpwnam',return_value=types.SimpleNamespace(pw_uid=999,pw_shell='/usr/sbin/nologin')),patch('grp.getgrnam',return_value=types.SimpleNamespace(gr_gid=os.getgid())),patch('os.chown'):
            previous_umask=os.umask(0o077)
            try: state=vps.install(source);vps.configure(state,folder)
            finally: os.umask(previous_umask)
            self.assertEqual(state['status'],'active')
            self.assertEqual((base / 'bin/frps').stat().st_mode & 0o777,0o755)
            self.assertEqual((base / 'bin').stat().st_mode & 0o777,0o755)
            self.assertEqual(base.stat().st_mode & 0o777,0o755)
            self.assertEqual((etc / 'tls').stat().st_mode & 0o777,0o750)
            self.assertEqual((etc / 'tls/relay.key').stat().st_mode & 0o777,0o640)
            unit=units / 'netgo-vps-relay.service';original=unit.read_bytes();unit.write_bytes(original+b'# foreign edit\n')
            before=len(calls)
            with self.assertRaisesRegex(ValueError,'Managed file changed'):vps.uninstall(state)
            self.assertEqual(before,len(calls))
            unit.write_bytes(original);vps.uninstall(state)
            self.assertTrue((etc / 'tls/relay.key').exists());self.assertFalse(unit.exists())
            vps.uninstall(state)
            state=vps.install(source);vps.configure(state,folder)
            self.assertEqual(state['status'],'active')

if __name__=='__main__':unittest.main()
