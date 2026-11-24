#!/usr/bin/env bash
set -euo pipefail
if [[ ${1:-} == --plan ]];then printf '%s\n' 'Plan only: Go uninstaller; remove owned services and keep data.';exit 0;fi
exec /opt/netgo/current/netgoctl server uninstall --yes
