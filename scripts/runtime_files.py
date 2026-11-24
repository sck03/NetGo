"""Explicit Linux server runtime and maintenance payload, excluding build inputs."""
from pathlib import Path

VPS_FILES = ('install-vps.sh',)
SERVER_BINARIES=('netgo-server','netgo-helper','netgoctl','netgo-ddns')
SERVER_UPSTREAM=('netbird-server','netbird','netbird-relay','caddy','frpc')

def server_files(root):
    return ['install-server.sh']
