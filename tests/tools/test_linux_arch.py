import json
import os
from pathlib import Path
import struct
import sys
import tempfile
import unittest
from unittest.mock import patch
from contextlib import contextmanager

@contextmanager
def requested_arch(value):
    old=os.environ.get("NETGO_ARCH")
    os.environ["NETGO_ARCH"]=value
    try:yield
    finally:
        if old is None:os.environ.pop("NETGO_ARCH",None)
        else:os.environ["NETGO_ARCH"]=old

sys.path.insert(0,str(Path(__file__).resolve().parents[2]/'scripts'))
from linux_arch import native_arch, require_native, verify_elf
from build_config import config
from verification import artifact_paths
from linux_preflight import inspect_environment

class LinuxArchitectureTests(unittest.TestCase):
    def test_native_arm_and_cross_build_guard(self):
        self.assertEqual(native_arch('aarch64'),'arm64')
        self.assertEqual(native_arch('x86_64'),'amd64')
        with requested_arch('arm64'),patch('platform.system',return_value='Linux'),patch('platform.machine',return_value='aarch64'):
            self.assertEqual(require_native(),'arm64')
            self.assertEqual(config('linux-server')['arch'],'arm64')
            files=artifact_paths('linux')
            self.assertTrue(all('amd64' not in p for p in files))
            self.assertIn('dist/upstream-linux-arm64/netbird-server',files)
        with requested_arch('arm64'),patch('platform.machine',return_value='x86_64'):
            with self.assertRaises(ValueError):require_native()
        for machine in ('armv7l','riscv64'):
            with self.assertRaises(ValueError):native_arch(machine)

    def test_elf_prevents_mixed_architecture_package(self):
        with tempfile.TemporaryDirectory() as folder:
            path=Path(folder)/'binary'
            for arch,machine in [('amd64',62),('arm64',183)]:
                header=bytearray(64);header[:7]=b'\x7fELF\x02\x01\x01';struct.pack_into('<HH',header,16,2,machine);path.write_bytes(header)
                verify_elf(path,arch)
                with self.assertRaises(ValueError):verify_elf(path,'arm64' if arch=='amd64' else 'amd64')
                header[5]=2;path.write_bytes(header)
                with self.assertRaises(ValueError):verify_elf(path,arch)

    def test_arm_install_preflight_and_pinned_tools(self):
        report=inspect_environment('install',{'ID':'debian','VERSION_ID':'13'},'Linux','aarch64',lambda _: '/bin/tool')
        self.assertEqual(report['status'],'PASS')
        lock=json.loads((Path(__file__).resolve().parents[2]/'upstream/versions.lock.json').read_text())
        for arch in ('amd64','arm64'):
            for item in lock['linux_toolchain_archives'][arch].values():
                self.assertEqual(len(item['sha256']),64)
                self.assertIn('https://',item['url'])
                if arch=='arm64':self.assertIn('arm64',item['filename'])
