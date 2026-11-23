import copy
import io
import json
import os
from pathlib import Path
import shutil
import socket
import subprocess
import sys
import tarfile
import tempfile
import unittest

ROOT=Path(__file__).resolve().parents[2]
sys.path.insert(0,str(ROOT/'scripts'))
from archive_utils import extract_tar
from server_setup import validate_setup, check_ports, disabled_default_policy
from nft_owner import legacy_owner


class InstallerTests(unittest.TestCase):
    def setUp(self):
        temporary=tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.root=Path(temporary.name)

    def archive(self,entries):
        path=self.root/'package.tar'
        with tarfile.open(path,'w') as archive:
            for name,kind,data in entries:
                item=tarfile.TarInfo(name)
                if kind=='file':
                    item.size=len(data); item.mode=0o755
                    archive.addfile(item,io.BytesIO(data))
                else:
                    item.type=tarfile.SYMTYPE if kind=='link' else tarfile.FIFOTYPE
                    item.linkname=data
                    archive.addfile(item)
        return path

    def test_legacy_nft_comment_cannot_be_forged_by_a_nested_object(self):
        self.assertTrue(legacy_owner('table inet netgo {\n comment "NetGo lease gate v1"\n}', 'netgo', 'NetGo lease gate v1'))
        self.assertFalse(legacy_owner('table inet netgo {\n chain x {\n comment "NetGo lease gate v1"\n}\n}', 'netgo', 'NetGo lease gate v1'))
        self.assertFalse(legacy_owner('table inet foreign {\n comment "NetGo lease gate v1"\n}', 'netgo', 'NetGo lease gate v1'))

    def test_traversal_links_devices_and_duplicates_do_not_extract(self):
        cases=[ [('outside/../../escape','file',b'bad')], [('tool/bin','link','../../escape')],
                [('tool/pipe','fifo','')], [('tool/a','file',b'a'),('tool/a','file',b'b')],
                [('tool/link','link','dir'),('tool/link/write','file',b'bad')] ]
        for index,entries in enumerate(cases):
            out=self.root/str(index)
            with self.subTest(index=index), self.assertRaises(ValueError):
                extract_tar(self.archive(entries),out,allow_links=True)
            self.assertEqual(list(out.iterdir()),[])

    def test_regular_archive_extracts_without_tarfile_filter_support(self):
        out=self.root/'out'
        extract_tar(self.archive([('tool/bin/run','file',b'#!/bin/sh\nexit 0\n')]),out)
        self.assertEqual((out/'tool/bin/run').read_bytes(),b'#!/bin/sh\nexit 0\n')

    @unittest.skipIf(os.name=='nt','Linux symlink semantics')
    def test_official_toolchain_style_relative_link_stays_inside_archive(self):
        out=self.root/'out'
        extract_tar(self.archive([('node/lib/npm.js','file',b'console.log(1)'),('node/bin/npm','link','../lib/npm.js')]),out,allow_links=True)
        self.assertEqual((out/'node/bin/npm').read_bytes(),b'console.log(1)')

    def test_setup_rejects_external_management_and_path_injection(self):
        base=json.loads((ROOT/'examples/site.json').read_text())
        validate_setup(base)
        for mutate in (lambda c:c.update(ManagementURL='http://external.invalid:8080'),
                       lambda c:c.update(Database='/etc/passwd'),
                       lambda c:c.update(LANInterface='eth0; reboot'),
                       lambda c:c.update(OverlayCIDR='192.168.50.0/24'),
                       lambda c:c.update(Auxiliaries=[{'ID':'unknown'}])):
            config=copy.deepcopy(base);mutate(config)
            with self.assertRaises((ValueError,KeyError)):
                validate_setup(config)

    def test_busy_port_is_detected_before_service_activation(self):
        config=json.loads((ROOT/'examples/site.json').read_text())
        with socket.socket(socket.AF_INET,socket.SOCK_STREAM) as listener:
            if os.name=='nt': listener.setsockopt(socket.SOL_SOCKET,socket.SO_EXCLUSIVEADDRUSE,1)
            listener.bind(('0.0.0.0',0));listener.listen()
            busy=listener.getsockname()[1]
            config['Deployment']['ManagementHealthPort']=busy
            _,manifest=validate_setup(config)
            with self.assertRaisesRegex(ValueError,'already in use'):
                check_ports(config,manifest)

    def test_default_policy_read_model_is_converted_to_write_contract(self):
        policy={'id':'default','name':'Default','enabled':True,'rules':[{'id':'rule-id','name':'All',
            'enabled':True,'action':'accept','bidirectional':True,'protocol':'all',
            'sources':[{'id':'all','name':'All','peers_count':0}],'destinations':[{'id':'all','name':'All'}]}]}
        disabled=disabled_default_policy(policy)
        self.assertFalse(disabled['enabled'])
        self.assertEqual(disabled['rules'][0]['sources'],['all'])
        self.assertEqual(disabled['rules'][0]['destinations'],['all'])
        self.assertNotIn('id',disabled['rules'][0])
        self.assertTrue(policy['enabled'])

    def test_server_entry_plans_and_rejects_invalid_arguments_without_root(self):
        bash=shutil.which('bash')
        if not bash and Path('D:/msys64/usr/bin/bash.exe').is_file():
            bash='D:/msys64/usr/bin/bash.exe'
        if not bash:self.skipTest('bash unavailable')
        for action in ('install','uninstall','configure'):
            result=subprocess.run([bash,str(ROOT/'install-server.sh'),action,'--plan'],capture_output=True,text=True,timeout=15)
            self.assertEqual(result.returncode,0,result.stderr)
            self.assertIn('No downloads',result.stdout)
        for args in (['install','--unknown','--plan'],['install','uninstall','--plan'],
                     ['install','--version','../../bad','--plan'],['install','--ref','--upload-pack=bad','--plan'],
                     ['install','--package','a','--source','b','--plan']):
            result=subprocess.run([bash,str(ROOT/'install-server.sh'),*args],capture_output=True,timeout=15)
            self.assertNotEqual(result.returncode,0)


if __name__=='__main__':unittest.main()
