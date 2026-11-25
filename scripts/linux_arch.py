"""Shared Linux server architecture and executable validation."""
import os
import platform
import struct
from pathlib import Path

MACHINES = {'amd64': 62, 'arm64': 183}

def native_arch(machine=None):
    value = (machine or platform.machine()).lower()
    result = {'x86_64': 'amd64', 'amd64': 'amd64', 'aarch64': 'arm64', 'arm64': 'arm64'}.get(value)
    if result is None:
        raise ValueError('Supported Linux architectures: amd64, arm64; observed: ' + value)
    return result

def target_arch():
    arch = os.environ.get('NETGO_ARCH') or native_arch()
    if arch not in MACHINES:
        raise ValueError('Unsupported Linux server architecture: ' + arch)
    return arch

def require_native():
    arch = target_arch()
    if platform.system() != 'Linux' or arch != native_arch():
        raise ValueError('Use a native Linux ' + arch + ' build host (CGO and runtime checks are required)')
    return arch

def verify_elf(path, arch):
    with Path(path).open('rb') as stream:
        header = stream.read(64)
    if (arch not in MACHINES or len(header) != 64 or header[:7] != b'\x7fELF\x02\x01\x01'
            or struct.unpack_from('<H', header, 16)[0] not in (2, 3)
            or struct.unpack_from('<H', header, 18)[0] != MACHINES[arch]):
        raise ValueError('Expected Linux ELF64 ' + arch + ' executable: ' + str(path))

if __name__ == '__main__':
    print(require_native())
