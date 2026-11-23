#!/usr/bin/env python3
"""Build and exercise isolated remote foundations; never capture a desktop."""
import argparse
import json
import os
from pathlib import Path
import shutil
import shlex
import subprocess
import sys

ROOT = Path(__file__).resolve().parents[1]


def run(args, **kwargs):
    print('+ ' + ' '.join(map(str, args)), flush=True)
    subprocess.run(list(map(str, args)), cwd=ROOT, check=True, **kwargs)


def verify(output):
    output = output.resolve()
    output.mkdir(parents=True, exist_ok=False)
    cxx, cc = shutil.which('g++'), shutil.which('gcc')
    if not cxx or not cc:
        raise RuntimeError('GCC C/C++ toolchain required (MinGW on Windows)')
    core = ROOT / 'packages/remote-core'
    flags = ['-std=c++17', '-Wall', '-Wextra', '-Wpedantic', '-Werror', '-pthread', '-I', str(core / 'include')]
    suffix = '.exe' if os.name == 'nt' else ''
    link = ['-static', '-static-libgcc', '-static-libstdc++'] if os.name == 'nt' else []
    run([cxx, *flags, '-O2', '-c', core / 'src/session.cpp', '-o', output / 'session.o'])
    run([cxx, *flags, '-O2', core / 'tests/session_tests.cpp', output / 'session.o', *link, '-o', output / ('session-tests' + suffix)])
    run([cc, '-std=c11', '-Wall', '-Wextra', '-Wpedantic', '-Werror', '-I', core / 'include', '-c', core / 'tests/c_abi_test.c', '-o', output / 'caller.o'])
    run([cxx, '-pthread', output / 'session.o', output / 'caller.o', *link, '-o', output / ('c-abi-test' + suffix)])
    run([output / ('session-tests' + suffix)])
    run([output / ('c-abi-test' + suffix)])
    ticket = ROOT / 'packages/remote-ticket'
    if os.name == 'nt':
        native_crypto = ROOT / '.tools/openssl-native'
        crypto_headers = ['-I', native_crypto / 'include']
        crypto = [native_crypto / 'lib64/libcrypto.a', '-lws2_32', '-lcrypt32', '-lbcrypt']
        crypto_version = json.loads((ROOT / 'upstream/versions.lock.json').read_text())['openssl']['version']
    else:
        pkg_config = shutil.which('pkg-config')
        if not pkg_config:
            raise RuntimeError('pkg-config and OpenSSL Crypto development files are required for signed-ticket tests')
        # These environment settings also support extracted project-local packages.
        crypto_headers = shlex.split(subprocess.check_output([pkg_config, '--cflags', 'libcrypto'], text=True).strip())
        crypto = shlex.split(subprocess.check_output([pkg_config, '--libs', 'libcrypto'], text=True).strip())
        crypto_version = subprocess.check_output([pkg_config, '--modversion', 'libcrypto'], text=True).strip()
    ticket_flags = [*flags, '-I', str(ticket / 'include'), *crypto_headers]
    vectors = ROOT / 'tests/contracts/remote/tickets.txt'
    run([cxx, *ticket_flags, '-O2', '-c', ticket / 'src/ticket.cpp', '-o', output / 'ticket.o'])
    run([cxx, *ticket_flags, '-O2', ticket / 'tests/ticket_tests.cpp', output / 'ticket.o', output / 'session.o',
         *link, *crypto, '-o', output / ('ticket-tests' + suffix)])
    run([cc, '-std=c11', '-Wall', '-Wextra', '-Wpedantic', '-Werror', '-I', core / 'include', '-I', ticket / 'include',
         '-c', ticket / 'tests/c_abi_test.c', '-o', output / 'ticket-caller.o'])
    run([cxx, output / 'ticket.o', output / 'ticket-caller.o', *link, *crypto, '-o', output / ('ticket-c-abi' + suffix)])
    run([output / ('ticket-tests' + suffix), vectors, crypto_version])
    run([output / ('ticket-c-abi' + suffix)])
    if os.name == 'nt':
        capture = output / 'capture'
        run(['cmake', '-S', ROOT / 'apps/windows/remote', '-B', capture, '-G', 'Ninja', '-DCMAKE_BUILD_TYPE=Release', '-DNETGO_CAPTURE_PROBE=ON'])
        run(['cmake', '--build', capture])
        run([capture / 'netgo-capture-probe.exe', '--check'])
    elif sys.platform.startswith('linux'):
        run([cxx, *flags, '-g', '-O1', '-fsanitize=address,undefined', '-fno-omit-frame-pointer',
             core / 'src/session.cpp', core / 'tests/session_tests.cpp', '-o', output / 'session-sanitized'])
        env = os.environ.copy()
        env['ASAN_OPTIONS'] = 'detect_leaks=1:halt_on_error=1'
        env['UBSAN_OPTIONS'] = 'halt_on_error=1:print_stacktrace=1'
        run([output / 'session-sanitized'], env=env)
        run([cxx, *ticket_flags, '-g', '-O1', '-fsanitize=address,undefined', '-fno-omit-frame-pointer',
             core / 'src/session.cpp', ticket / 'src/ticket.cpp', ticket / 'tests/ticket_tests.cpp',
             *crypto, '-o', output / 'ticket-sanitized'])
        run([output / 'ticket-sanitized', vectors, crypto_version], env=env)
    print('PASS remote gate, signed tickets and C11 ABI; no host registry, proof of possession, media or remote network acceptance', flush=True)


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--output', required=True, type=Path)
    args = parser.parse_args()
    verify(args.output)
